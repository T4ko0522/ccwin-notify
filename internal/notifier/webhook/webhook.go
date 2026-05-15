// Package webhook は Discord / Slack Webhook Notifier を提供する (D-05)。
package webhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/notifier"
	"github.com/t4ko0522/ccwin-notify/internal/secret"
)

// newDefaultClient は Webhook 送信用の http.Client を返す (M-01)。
// - CheckRedirect: リダイレクト禁止 (SSRF 耐性)
// - TLSClientConfig.MinVersion: TLS 1.2 強制
// cfg.HTTPClient が nil のときに使う。テストで Server.Client() を渡された場合は
// テスト側の Transport (TLS なし or InsecureSkipVerify) をそのまま使う。
func newDefaultClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
}

// redactedURLError は url.Error 由来のメッセージから URL 文字列を除去した wrapper。
// errors.Is / errors.As でチェーンを保持しつつ、Error() からは URL を出さない (H-03)。
type redactedURLError struct {
	inner error
	msg   string
}

func (e *redactedURLError) Error() string { return e.msg }
func (e *redactedURLError) Unwrap() error { return e.inner }

// scrubURLError は hc.Do が返す *url.Error を URL を含まないメッセージで包む。
// URL 以外のエラー (json marshal 等) はそのまま返す。
func scrubURLError(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	inner := urlErr.Err
	if inner == nil {
		return &redactedURLError{inner: err, msg: fmt.Sprintf("%s failed (url redacted)", urlErr.Op)}
	}
	return &redactedURLError{
		inner: inner,
		msg:   fmt.Sprintf("%s failed (url redacted): %s", urlErr.Op, inner.Error()),
	}
}

// Config は Webhook Notifier の設定 (Discord / Slack 共通)。
type Config struct {
	URL        secret.SecretString
	Timeout    time.Duration            // per-attempt timeout
	MaxRetries int                      // 0..10
	KindMask   map[event.EventKind]bool
	HTTPClient *http.Client             // nil で http.DefaultClient 相当を使う (テスト差替用)
}

type discordNotifier struct {
	cfg  Config
	http *http.Client
}

type slackNotifier struct {
	cfg  Config
	http *http.Client
}

var _ notifier.Notifier = (*discordNotifier)(nil)
var _ notifier.Notifier = (*slackNotifier)(nil)

// NewDiscord は Discord Webhook Notifier を返す (D-05)。
// ペイロード: {"content": "<title>\n<body>"}
func NewDiscord(cfg Config) notifier.Notifier {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = newDefaultClient()
	}
	return &discordNotifier{cfg: cfg, http: hc}
}

// NewSlack は Slack Webhook Notifier を返す (D-05)。
// ペイロード: {"text": "<title>\n<body>"}
func NewSlack(cfg Config) notifier.Notifier {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = newDefaultClient()
	}
	return &slackNotifier{cfg: cfg, http: hc}
}

func (n *discordNotifier) Name() string { return "webhook.discord" }

func (n *discordNotifier) Wants(kind event.EventKind) bool {
	return len(n.cfg.KindMask) == 0 || n.cfg.KindMask[kind]
}

func (n *discordNotifier) Notify(ctx context.Context, ev event.Event) error {
	if len(n.cfg.KindMask) > 0 && !n.cfg.KindMask[ev.Kind] {
		return nil
	}
	body := map[string]string{"content": ev.Title + "\n" + ev.Body}
	return postWithRetry(ctx, n.http, n.cfg, body)
}

func (n *slackNotifier) Name() string { return "webhook.slack" }

func (n *slackNotifier) Wants(kind event.EventKind) bool {
	return len(n.cfg.KindMask) == 0 || n.cfg.KindMask[kind]
}

func (n *slackNotifier) Notify(ctx context.Context, ev event.Event) error {
	if len(n.cfg.KindMask) > 0 && !n.cfg.KindMask[ev.Kind] {
		return nil
	}
	body := map[string]string{"text": ev.Title + "\n" + ev.Body}
	return postWithRetry(ctx, n.http, n.cfg, body)
}

// postWithRetry は指数バックオフ + Retry-After 対応で HTTP POST を試みる。
// MaxRetries 回失敗後は最後のエラーを返す。
func postWithRetry(ctx context.Context, hc *http.Client, cfg Config, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhook: marshal: %w", err)
	}

	url := cfg.URL.Reveal()
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	backoff := time.Second
	var lastErr error
	maxAttempts := cfg.MaxRetries + 1
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}

		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(data))
		if err != nil {
			cancel()
			return fmt.Errorf("webhook: new request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := hc.Do(req)
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("webhook: post attempt %d: %w", attempt+1, scrubURLError(err))
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			// Retry-After を尊重
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil {
					retryAfter := time.Duration(secs) * time.Second
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(retryAfter):
					}
				}
			}
			lastErr = fmt.Errorf("webhook: rate limited (429), attempt %d", attempt+1)
			continue
		}

		// 3xx はリダイレクト拒否 (M-01 / SSRF 耐性) で resp が返るが成功扱いしない
		if resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("webhook: HTTP %d on attempt %d", resp.StatusCode, attempt+1)
			continue
		}

		return nil
	}

	return lastErr
}

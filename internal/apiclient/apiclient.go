// Package apiclient は TUI / send CLI 共有の Daemon API クライアントを提供する。
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	ipclient "github.com/t4ko0522/ccwin-notify/internal/ipc/client"
	"github.com/t4ko0522/ccwin-notify/internal/secret"
)

// sentinel errors
var (
	ErrDaemonNotRunning = errors.New("apiclient: daemon not running")
	ErrUnauthorized     = errors.New("apiclient: unauthorized (401)")
	ErrServer           = errors.New("apiclient: server error (5xx)")
	ErrConnect          = errors.New("apiclient: connection failed")
)

// Healthz は GET /v1/healthz のレスポンス型。
type Healthz struct {
	App       string    `json:"app"`
	Version   string    `json:"version"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	UptimeSec int       `json:"uptime_seconds"`
}

// QueueStat は Queue の統計情報。
type QueueStat struct {
	Len      int              `json:"len"`
	Capacity int              `json:"capacity"`
	Policy   event.DropPolicy `json:"policy"`
}

// SourceStat は EventSource の統計情報。
type SourceStat struct {
	Enabled     bool   `json:"enabled"`
	State       string `json:"state"`
	LastSeenPID int    `json:"last_seen_pid,omitempty"`
}

// NotifierStat は Notifier の統計情報。
type NotifierStat struct {
	Enabled   bool   `json:"enabled"`
	LastError string `json:"last_error"`
}

// AuthStat は認証情報の統計。
type AuthStat struct {
	TokenPresent bool                `json:"token_present"`
	TokenValue   secret.SecretString `json:"token_value"`
}

// Status は GET /v1/status のレスポンス型。
type Status struct {
	PID       int                     `json:"pid"`
	Version   string                  `json:"version"`
	StartedAt time.Time               `json:"started_at"`
	PortFile  string                  `json:"port_file"`
	Queue     QueueStat               `json:"queue"`
	Sources   map[string]SourceStat   `json:"sources"`
	Notifiers map[string]NotifierStat `json:"notifiers"`
	Auth      AuthStat                `json:"auth"`
	LastPanic string                  `json:"last_panic"`
}

// TestRequest は POST /v1/test のリクエスト型。
type TestRequest struct {
	Kind           event.EventKind `json:"kind"`
	TargetNotifier string          `json:"target_notifier"`
	Title          string          `json:"title"`
	Body           string          `json:"body"`
}

// NotifyOutcome は POST /v1/test の結果。
type NotifyOutcome struct {
	Notifier string `json:"notifier"`
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
}

// TestResult は POST /v1/test のレスポンス型。
type TestResult struct {
	EventID string          `json:"event_id"`
	Results []NotifyOutcome `json:"results"`
}

// Client は TUI / send CLI 双方が使う Daemon API クライアント。
type Client interface {
	PostEvent(ctx context.Context, e event.Event) error
	GetHealthz(ctx context.Context) (Healthz, error)
	GetStatus(ctx context.Context) (Status, error)
	// StreamEvents は内部 goroutine で SSE を読み、チャネルに Event を push する。
	// buffer=64。ctx cancel / server 切断でチャネルを close する。
	// 内部で指数バックオフ再接続を行い、ctx alive な間は継続する。
	StreamEvents(ctx context.Context) (<-chan event.Event, error)
	TestNotifier(ctx context.Context, req TestRequest) (TestResult, error)
}

// client は Client の実装。
// Bearer トークン自体は httpClient (bearerTransport 内) が SecretString として保持する。
type client struct {
	baseURL    string
	httpClient *http.Client
}

// New は portfile から接続情報を読み Bearer トークンを secret.token から取得して Client を返す。
// portfile 不在 / stale (PID 不一致 / healthz 不一致) → ErrDaemonNotRunning。
func New(portfilePath, tokenPath string) (Client, error) {
	pf, err := ipclient.ReadPortfile(portfilePath)
	if err != nil {
		return nil, ErrDaemonNotRunning
	}

	tokenData, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("apiclient: failed to read token: %w", err)
	}
	// 平文 string 変数を残さない (M-02 / CWE-316)。SecretString として httpClient に渡す。
	token := secret.SecretString(strings.TrimSpace(string(tokenData)))

	// PID が現存するかを確認
	if !isPIDAlive(pf.PID) {
		return nil, ErrDaemonNotRunning
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", pf.Port)
	httpClient := ipclient.NewHTTPClient(token)

	c := &client{
		baseURL:    baseURL,
		httpClient: httpClient,
	}

	// healthz で生存確認 — 200ms タイムアウト × 3 回、バックオフ 100/300/500ms (plan §4.1 MUST)
	backoffs := []time.Duration{100 * time.Millisecond, 300 * time.Millisecond, 500 * time.Millisecond}
	var healthzOK bool
	for attempt := 0; attempt < 3; attempt++ {
		hctx, hcancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		h, herr := c.GetHealthz(hctx)
		hcancel()
		if herr == nil && h.App == "ccwin-notify" {
			healthzOK = true
			break
		}
		if attempt < len(backoffs) {
			time.Sleep(backoffs[attempt])
		}
	}
	if !healthzOK {
		return nil, ErrDaemonNotRunning
	}

	return c, nil
}

// PostEvent は POST /v1/events でイベントをデーモンに送信する。
func (c *client) PostEvent(ctx context.Context, e event.Event) error {
	body, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("apiclient: marshal event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/events", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("apiclient: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrConnect, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode >= 500:
		return ErrServer
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("apiclient: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// GetHealthz は GET /v1/healthz でヘルス情報を取得する。
func (c *client) GetHealthz(ctx context.Context) (Healthz, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/healthz", nil)
	if err != nil {
		return Healthz{}, fmt.Errorf("apiclient: create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Healthz{}, fmt.Errorf("%w: %v", ErrConnect, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Healthz{}, fmt.Errorf("apiclient: healthz status %d", resp.StatusCode)
	}

	var h Healthz
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return Healthz{}, fmt.Errorf("apiclient: decode healthz: %w", err)
	}
	return h, nil
}

// GetStatus は GET /v1/status でステータス情報を取得する。
func (c *client) GetStatus(ctx context.Context) (Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/status", nil)
	if err != nil {
		return Status{}, fmt.Errorf("apiclient: create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Status{}, fmt.Errorf("%w: %v", ErrConnect, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return Status{}, ErrUnauthorized
	case resp.StatusCode >= 500:
		return Status{}, ErrServer
	case resp.StatusCode != http.StatusOK:
		return Status{}, fmt.Errorf("apiclient: status %d", resp.StatusCode)
	}

	var s Status
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return Status{}, fmt.Errorf("apiclient: decode status: %w", err)
	}
	return s, nil
}

// TestNotifier は POST /v1/test で指定 Notifier を手動発火する。
func (c *client) TestNotifier(ctx context.Context, req TestRequest) (TestResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return TestResult{}, fmt.Errorf("apiclient: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/test", bytes.NewReader(body))
	if err != nil {
		return TestResult{}, fmt.Errorf("apiclient: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return TestResult{}, fmt.Errorf("%w: %v", ErrConnect, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return TestResult{}, ErrUnauthorized
	case resp.StatusCode >= 500:
		return TestResult{}, ErrServer
	case resp.StatusCode != http.StatusOK:
		return TestResult{}, fmt.Errorf("apiclient: unexpected status %d", resp.StatusCode)
	}

	var result TestResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return TestResult{}, fmt.Errorf("apiclient: decode result: %w", err)
	}
	return result, nil
}

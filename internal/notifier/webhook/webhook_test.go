// Package webhook のテスト (Phase 3 Retry 2)
// テスト ID: T-096〜T-106
// 受入条件: B5 (Webhook Notifier) / D5 (URL マスク) / I1 (HTTPS URL)
package webhook_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/notifier/webhook"
	"github.com/t4ko0522/ccwin-notify/internal/secret"
)

func testEvent(kind event.EventKind, title, body string) event.Event {
	return event.Event{
		ID:        "test-id",
		Kind:      kind,
		Title:     title,
		Body:      body,
		Source:    "test",
		Timestamp: time.Now(),
	}
}

// ---- T-096: Discord payload フォーマット ----

// T-096 / B5: NewDiscord が {"content": "title\nbody"} ペイロードを POST する
func TestDiscordNotifier_PayloadFormat(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := webhook.Config{
		URL:        secret.SecretString(srv.URL),
		Timeout:    3 * time.Second,
		MaxRetries: 0,
		HTTPClient: srv.Client(),
	}
	n := webhook.NewDiscord(cfg)

	ev := testEvent(event.KindStop, "Claude stopped", "Task done")
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Fatalf("Discord Notify: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("Discord payload JSON decode: %v (body=%q)", err, capturedBody)
	}

	expected := "Claude stopped\nTask done"
	if got, _ := payload["content"].(string); got != expected {
		t.Errorf("Discord content: got %q, want %q", got, expected)
	}
	// M-09: allowed_mentions={"parse":[]} で全 mention 抑止
	am, ok := payload["allowed_mentions"].(map[string]any)
	if !ok {
		t.Fatal("allowed_mentions が含まれていない (M-09)")
	}
	parse, _ := am["parse"].([]any)
	if len(parse) != 0 {
		t.Errorf("allowed_mentions.parse: got %v, want empty array", parse)
	}
}

// ---- T-097: Slack payload フォーマット ----

// T-097 / B5: NewSlack が {"text": "title\nbody"} ペイロードを POST する
func TestSlackNotifier_PayloadFormat(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := webhook.Config{
		URL:        secret.SecretString(srv.URL),
		Timeout:    3 * time.Second,
		MaxRetries: 0,
		HTTPClient: srv.Client(),
	}
	n := webhook.NewSlack(cfg)

	ev := testEvent(event.KindNotification, "Input required", "Please answer")
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Fatalf("Slack Notify: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("Slack payload JSON decode: %v (body=%q)", err, capturedBody)
	}

	expected := "Input required\nPlease answer"
	if got, _ := payload["text"].(string); got != expected {
		t.Errorf("Slack text: got %q, want %q", got, expected)
	}
	// M-09: link_names=0 / mrkdwn=false / parse=none で mention 抑止
	if v, _ := payload["link_names"].(float64); v != 0 {
		t.Errorf("link_names: got %v, want 0", v)
	}
	if v, _ := payload["mrkdwn"].(bool); v {
		t.Errorf("mrkdwn: got %v, want false", v)
	}
	if v, _ := payload["parse"].(string); v != "none" {
		t.Errorf("parse: got %q, want \"none\"", v)
	}
}

// ---- T-098: Name / Wants ----

// T-098 / B5: Discord Name="webhook.discord", Slack Name="webhook.slack"
func TestNotifier_Name(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := webhook.Config{
		URL:        secret.SecretString(srv.URL),
		HTTPClient: srv.Client(),
	}
	discord := webhook.NewDiscord(cfg)
	slack := webhook.NewSlack(cfg)

	if got := discord.Name(); got != "webhook.discord" {
		t.Errorf("Discord Name: got %q, want \"webhook.discord\"", got)
	}
	if got := slack.Name(); got != "webhook.slack" {
		t.Errorf("Slack Name: got %q, want \"webhook.slack\"", got)
	}
}

// T-099 / B4: KindMask が空なら全 Kind で Wants=true
func TestNotifier_Wants_EmptyMask(t *testing.T) {
	t.Parallel()

	cfg := webhook.Config{
		URL:      "https://example.com/webhook",
		KindMask: nil, // 空 = 全 Kind 通す
	}
	n := webhook.NewDiscord(cfg)

	for kind := range event.ValidKinds {
		if !n.Wants(kind) {
			t.Errorf("Wants(%q): got false, want true (KindMask 空)", kind)
		}
	}
}

// T-100 / B4: KindMask で許可外 Kind は Wants=false
func TestNotifier_Wants_KindMask(t *testing.T) {
	t.Parallel()

	cfg := webhook.Config{
		URL: "https://example.com/webhook",
		KindMask: map[event.EventKind]bool{
			event.KindStop: true,
		},
	}
	n := webhook.NewDiscord(cfg)

	if !n.Wants(event.KindStop) {
		t.Error("Wants(KindStop): got false, want true")
	}
	if n.Wants(event.KindNotification) {
		t.Error("Wants(KindNotification): got true, want false (kind_mask)")
	}
}

// ---- T-101: 5xx retry (MaxRetries=2 → 計 3 回) ----

// T-101 / B5: 5xx 応答時に MaxRetries 回リトライし最後のエラーを返す
// バックオフ待機の時間を避けるため MaxRetries=0 を使い 1 回のみ確認する
func TestNotifier_5xx_ReturnsError(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := webhook.Config{
		URL:        secret.SecretString(srv.URL),
		Timeout:    3 * time.Second,
		MaxRetries: 0, // 1 回のみ (バックオフを待たない)
		HTTPClient: srv.Client(),
	}
	n := webhook.NewDiscord(cfg)

	err := n.Notify(context.Background(), testEvent(event.KindStop, "t", "b"))
	if err == nil {
		t.Error("5xx 応答でエラーが返らなかった")
	}
	if callCount.Load() != 1 {
		t.Errorf("5xx: 呼び出し回数: got %d, want 1", callCount.Load())
	}
}

// M-01: Webhook HTTP Client は redirect (3xx) を追わずエラーを返す (SSRF 耐性)
func TestNotifier_RedirectDisallowed(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/other", http.StatusFound)
			return
		}
		// /other に到達してしまうと redirect が追われたことになる → テスト失敗
		t.Errorf("redirect が追われた: path=%s", r.URL.Path)
	}))
	defer srv.Close()

	cfg := webhook.Config{
		URL:        secret.SecretString(srv.URL + "/redirect"),
		Timeout:    500 * time.Millisecond,
		MaxRetries: 0,
		// 既定 Client (CheckRedirect=ErrUseLastResponse) を使うため HTTPClient は nil
	}
	n := webhook.NewDiscord(cfg)

	err := n.Notify(context.Background(), testEvent(event.KindStop, "t", "b"))
	// 302 が返るので 4xx/5xx パスとして「post 1 attempt 失敗」になる
	if err == nil {
		t.Errorf("redirect を 4xx 扱いせず成功: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("hits: got %d, want 1 (redirect が追われていないこと)", hits.Load())
	}
}

// H-03: hc.Do が *url.Error を返した場合、エラーメッセージに URL 文字列が含まれない
func TestNotifier_DialFailure_URLNotInError(t *testing.T) {
	t.Parallel()

	// 確実に dial 失敗するアドレス (port 1 / RFC2606 reserved)
	secretURL := "https://discord.com/api/webhooks/SECRET_ID_12345/SECRET_TOKEN_ABCDEF_TOKEN"
	// dial 失敗を起こすため、実 host は別の到達不能アドレスに差し替えるのではなく、
	// httptest を閉じてから使うことで Connection refused を再現する
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // closed server に POST → connect refused

	cfg := webhook.Config{
		URL:        secret.SecretString(srv.URL + "/api/webhooks/SECRET_ID_12345/SECRET_TOKEN_ABCDEF"),
		Timeout:    500 * time.Millisecond,
		MaxRetries: 0,
		HTTPClient: srv.Client(),
	}
	n := webhook.NewDiscord(cfg)

	err := n.Notify(context.Background(), testEvent(event.KindStop, "t", "b"))
	if err == nil {
		t.Fatal("dial 失敗でエラーが返らなかった")
	}
	msg := err.Error()
	if contains(msg, "SECRET_TOKEN_ABCDEF") || contains(msg, "SECRET_ID_12345") || contains(msg, "/api/webhooks/") {
		t.Errorf("エラーに URL/token が漏れている: %s", msg)
	}
	if !contains(msg, "url redacted") {
		t.Errorf("scrub マーカー 'url redacted' が含まれない: %s", msg)
	}
	_ = secretURL
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// T-101b / B5: MaxRetries=2 → 計 3 回 呼ばれる (バックオフ時間を 0 相当に短縮)
// ctx タイムアウト < バックオフ待機時間 になることを避けるため短い timeout を使う
func TestNotifier_5xx_MaxRetries2_AttemptCount(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := webhook.Config{
		URL:     secret.SecretString(srv.URL),
		Timeout: 500 * time.Millisecond,
		// バックオフ 1s + 2s が発生するが ctx で全体を timeout しない
		// retry 2 = 試行 3 回。テストが遅くなるが flaky ではない (サーバ側は即応答)
		MaxRetries: 2,
		HTTPClient: srv.Client(),
	}
	n := webhook.NewDiscord(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := n.Notify(ctx, testEvent(event.KindStop, "t", "b"))
	if err == nil {
		t.Error("5xx MaxRetries=2: エラーが返らなかった")
	}
	if got := callCount.Load(); got != 3 {
		t.Errorf("5xx MaxRetries=2: 呼び出し回数 got %d, want 3", got)
	}
}

// ---- T-102: ctx キャンセルで即終了 ----

// T-102 / E4: ctx がキャンセル済みの場合、Notify が即座に ctx.Err() を返す
func TestNotifier_CtxCanceled_ReturnsError(t *testing.T) {
	t.Parallel()

	// 決して応答しないサーバ (接続を保留)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second)
	}))
	defer srv.Close()

	cfg := webhook.Config{
		URL:        secret.SecretString(srv.URL),
		Timeout:    5 * time.Second,
		MaxRetries: 0,
		HTTPClient: srv.Client(),
	}
	n := webhook.NewDiscord(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 即座にキャンセル

	err := n.Notify(ctx, testEvent(event.KindStop, "t", "b"))
	if err == nil {
		t.Error("ctx キャンセル後の Notify: エラーが返らなかった")
	}
}

// ---- T-103: 200 OK は nil を返す ----

// T-103 / B5: 200 OK で nil を返す
func TestNotifier_200OK_Nil(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := webhook.Config{
		URL:        secret.SecretString(srv.URL),
		Timeout:    3 * time.Second,
		MaxRetries: 0,
		HTTPClient: srv.Client(),
	}
	n := webhook.NewDiscord(cfg)

	if err := n.Notify(context.Background(), testEvent(event.KindStop, "t", "b")); err != nil {
		t.Errorf("200 OK: got error %v, want nil", err)
	}
}

// ---- T-104: 429 Retry-After ----

// T-104 / B5: 429 → Retry-After ヘッダを読んで再試行し、最終 200 で nil を返す
func TestNotifier_429_RetryAfter_Success(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			// 1 回目: 429 + Retry-After: 0 (即座に再試行)
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		// 2 回目: 200
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := webhook.Config{
		URL:        secret.SecretString(srv.URL),
		Timeout:    3 * time.Second,
		MaxRetries: 1, // 1 回リトライを許可
		HTTPClient: srv.Client(),
	}
	n := webhook.NewDiscord(cfg)

	if err := n.Notify(context.Background(), testEvent(event.KindStop, "t", "b")); err != nil {
		t.Errorf("429 → retry → 200: got error %v, want nil", err)
	}
	if got := callCount.Load(); got != 2 {
		t.Errorf("429 retry: 呼び出し回数 got %d, want 2", got)
	}
}

// ---- T-105: KindMask で弾かれた Kind は Notify が nil ----

// T-105 / B4: KindMask で弾かれた Kind は Notify でサーバを呼ばず nil を返す
func TestNotifier_KindMask_Filtered_NoHTTP(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := webhook.Config{
		URL:     secret.SecretString(srv.URL),
		Timeout: 3 * time.Second,
		KindMask: map[event.EventKind]bool{
			event.KindStop: true,
		},
		HTTPClient: srv.Client(),
	}
	n := webhook.NewDiscord(cfg)

	// KindNotification は KindMask で弾かれる
	ev := testEvent(event.KindNotification, "t", "b")
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Errorf("KindMask filtered: got error %v, want nil", err)
	}
	if callCount.Load() != 0 {
		t.Errorf("KindMask filtered: HTTP が呼ばれてはならないが %d 回呼ばれた", callCount.Load())
	}
}

// ---- T-106b: Slack Notifier の KindMask / Wants / Name ----

// T-106b / B5: NewSlack の Wants/Name を確認
func TestSlackNotifier_WantsAndName(t *testing.T) {
	t.Parallel()

	cfg := webhook.Config{
		URL: "https://hooks.slack.com/test",
		KindMask: map[event.EventKind]bool{
			event.KindStop: true,
		},
	}
	n := webhook.NewSlack(cfg)

	if got := n.Name(); got != "webhook.slack" {
		t.Errorf("Slack Name: got %q, want \"webhook.slack\"", got)
	}
	if !n.Wants(event.KindStop) {
		t.Error("Slack Wants(KindStop): got false, want true")
	}
	if n.Wants(event.KindNotification) {
		t.Error("Slack Wants(KindNotification): got true, want false (kind_mask)")
	}
}

// T-106c / B5: Slack Notify が {"text": "..."} ペイロードを送る
func TestSlackNotifier_NotifyFiltered(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := webhook.Config{
		URL:     secret.SecretString(srv.URL),
		Timeout: 3 * time.Second,
		KindMask: map[event.EventKind]bool{
			event.KindStop: true,
		},
		HTTPClient: srv.Client(),
	}
	n := webhook.NewSlack(cfg)

	// KindMask で弾かれる Kind
	ev := testEvent(event.KindNotification, "t", "b")
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Errorf("Slack Notify (filtered kind): got %v, want nil", err)
	}
	if callCount.Load() != 0 {
		t.Errorf("フィルタされた Kind: HTTP が呼ばれてはならないが %d 回呼ばれた", callCount.Load())
	}
}

// ---- T-106: URL (SecretString) が slog.LogValue で "***" になる ----

// T-106 / D5: webhook.Config.URL は secret.SecretString なので LogValue が "***" を返す
func TestWebhookConfig_URL_LogValue_Masked(t *testing.T) {
	t.Parallel()

	const rawURL = "https://hooks.example.com/secret-path"
	cfg := webhook.Config{
		URL: secret.SecretString(rawURL),
	}

	logVal := cfg.URL.LogValue()
	if got := logVal.String(); got != "***" {
		t.Errorf("URL.LogValue: got %q, want \"***\"", got)
	}
}

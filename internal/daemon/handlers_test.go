// internal/daemon パッケージのハンドラ単体テスト (Phase 3 Retry 2)
// テスト ID: T-083h〜T-095h (handlers 専用; dispatcher_test.go の T-083 ShutdownDrain と区別)
// 受入条件: D5 (マスク) / C4 (409 Conflict) / B3 (503 Shutdown) / A6 (認証)
package daemon_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/apiclient"
	"github.com/t4ko0522/ccwin-notify/internal/config"
	"github.com/t4ko0522/ccwin-notify/internal/daemon"
	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/notifier"
	"github.com/t4ko0522/ccwin-notify/internal/secret"
)

// ---- ヘルパー ----

func defaultTestConfig() *config.Config {
	return &config.Config{
		Queue: config.QueueConfig{Capacity: 256, Policy: event.DropOldest},
		Sources: config.SourcesConfig{
			Hooks:   config.HooksConfig{Enabled: true},
			Process: config.ProcessConfig{Enabled: false},
		},
		Notifiers: config.NotifiersConfig{
			Toast: config.ToastConfig{Enabled: true},
			Sound: config.SoundConfig{Enabled: false},
			Webhook: config.WebhookConfig{
				Discord: config.WebhookEndpoint{Enabled: false},
				Slack:   config.WebhookEndpoint{Enabled: false},
			},
		},
	}
}

// ---- T-083: HandleHealthz 200 + JSON フィールド ----

// T-083 / D5: HandleHealthz が 200 + {status:ok, app:ccwin-notify, version, pid, started_at, uptime_seconds} を返す
func TestHandleHealthz_OK(t *testing.T) {
	t.Parallel()

	startedAt := time.Now().Add(-5 * time.Second)
	handler := daemon.HandleHealthz(startedAt, "0.1.0-test")

	req := httptest.NewRequest(http.MethodGet, "/v1/healthz", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("HandleHealthz status: got %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("HandleHealthz JSON decode: %v (body=%q)", err, w.Body.String())
	}

	if got := resp["status"]; got != "ok" {
		t.Errorf("status: got %v, want \"ok\"", got)
	}
	if got := resp["app"]; got != "ccwin-notify" {
		t.Errorf("app: got %v, want \"ccwin-notify\"", got)
	}
	if got := resp["version"]; got != "0.1.0-test" {
		t.Errorf("version: got %v, want \"0.1.0-test\"", got)
	}
	if _, ok := resp["pid"]; !ok {
		t.Error("pid フィールドが存在しない")
	}
	if _, ok := resp["started_at"]; !ok {
		t.Error("started_at フィールドが存在しない")
	}
	if uptime, ok := resp["uptime_seconds"].(float64); !ok || uptime < 0 {
		t.Errorf("uptime_seconds: got %v, want >= 0", resp["uptime_seconds"])
	}
}

// T-084 / D5: HandleHealthz Content-Type が application/json
func TestHandleHealthz_ContentType(t *testing.T) {
	t.Parallel()

	handler := daemon.HandleHealthz(time.Now(), "0.1.0")
	req := httptest.NewRequest(http.MethodGet, "/v1/healthz", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type: got %q, want \"application/json\"", ct)
	}
}

// ---- T-085〜T-087: HandleStatus ----

// T-085 / D5 / I4: HandleStatus の token_value が "***" でマスクされる (MarshalJSON 経由)
func TestHandleStatus_TokenValueMasked(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	const rawToken = "super-secret-token-abc123"
	authToken := secret.SecretString(rawToken)
	startedAt := time.Now().Add(-2 * time.Second)

	handler := daemon.HandleStatus(d, bus, hub, cfg, authToken, startedAt, "/tmp/daemon.port")

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("HandleStatus status: got %d, want 200", w.Code)
	}

	// JSON 生出力に実値が含まれていないことを確認 (D5)
	body := w.Body.String()
	if bytes.Contains([]byte(body), []byte(rawToken)) {
		t.Errorf("D5 FAIL: レスポンスに実 token 値 %q が漏洩している: %s", rawToken, body)
	}

	// "***" がマスク文字列として含まれることを確認
	if !bytes.Contains([]byte(body), []byte(`"***"`)) {
		t.Errorf("D5 FAIL: \"***\" がレスポンスに含まれていない: %s", body)
	}

	// apiclient.Status として正しくデコードできる
	var status apiclient.Status
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("HandleStatus JSON decode: %v (body=%q)", err, body)
	}
}

// T-086 / D2: HandleStatus が sources / notifiers / queue を含む
func TestHandleStatus_Fields(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	handler := daemon.HandleStatus(d, bus, hub, cfg, "tok", time.Now(), "port.file")

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("HandleStatus: %d %s", w.Code, w.Body.String())
	}

	var status apiclient.Status
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if status.Version == "" {
		t.Error("version フィールドが空")
	}
	if status.PID <= 0 {
		t.Errorf("pid: got %d, want > 0", status.PID)
	}
	if _, ok := status.Sources["hooks"]; !ok {
		t.Error("sources[hooks] が存在しない")
	}
	if _, ok := status.Sources["process"]; !ok {
		t.Error("sources[process] が存在しない")
	}
	if _, ok := status.Notifiers["toast"]; !ok {
		t.Error("notifiers[toast] が存在しない")
	}
	if status.Queue.Capacity != cfg.Queue.Capacity {
		t.Errorf("queue.capacity: got %d, want %d", status.Queue.Capacity, cfg.Queue.Capacity)
	}
}

// T-087 / D2: HandleStatus の sources[hooks].state は enabled=true → "running"
func TestHandleStatus_SourceState(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	cfg.Sources.Hooks.Enabled = true
	cfg.Sources.Process.Enabled = false

	handler := daemon.HandleStatus(d, bus, hub, cfg, "tok", time.Now(), "port")
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	var status apiclient.Status
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got := status.Sources["hooks"].State; got != "running" {
		t.Errorf("sources[hooks].state: got %q, want \"running\"", got)
	}
	if got := status.Sources["process"].State; got != "disabled" {
		t.Errorf("sources[process].state: got %q, want \"disabled\"", got)
	}
}

// ---- T-088〜T-093: HandleTest ----

// T-088 / D-35: HandleTest 正常系 — FakeNotifier が呼ばれ results に OK=true が含まれる
func TestHandleTest_OK(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	handler := daemon.HandleTest(d, []notifier.Notifier{fakeN}, cfg.Notifiers)

	body, _ := json.Marshal(apiclient.TestRequest{
		Kind:           event.KindStop,
		TargetNotifier: "all",
		Title:          "test title",
		Body:           "test body",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("HandleTest status: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var result apiclient.TestResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.EventID == "" {
		t.Error("event_id が空")
	}
	if len(result.Results) == 0 {
		t.Error("results が空")
	}
}

// T-089 / D-35: HandleTest 不正JSON → 400 invalid_json
func TestHandleTest_InvalidJSON(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	handler := daemon.HandleTest(d, []notifier.Notifier{fakeN}, cfg.Notifiers)

	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewReader([]byte(`{invalid`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("HandleTest invalid JSON: got %d, want 400", w.Code)
	}

	var errResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errResp); err == nil {
		if errObj, ok := errResp["error"].(map[string]interface{}); ok {
			if code := errObj["code"]; code != "invalid_json" {
				t.Errorf("error.code: got %v, want \"invalid_json\"", code)
			}
		}
	}
}

// T-090 / D-35: HandleTest 未知Kind → 400 invalid_kind
func TestHandleTest_InvalidKind(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	handler := daemon.HandleTest(d, []notifier.Notifier{fakeN}, cfg.Notifiers)

	body, _ := json.Marshal(map[string]interface{}{
		"kind":            "UnknownKindXyz",
		"target_notifier": "all",
		"title":           "test",
		"body":            "body",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("HandleTest invalid kind: got %d, want 400", w.Code)
	}

	var errResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errResp); err == nil {
		if errObj, ok := errResp["error"].(map[string]interface{}); ok {
			if code := errObj["code"]; code != "invalid_kind" {
				t.Errorf("error.code: got %v, want \"invalid_kind\"", code)
			}
		}
	}
}

// T-091 / D-40: HandleTest — 対象 Notifier が disabled → 409 Conflict conflict_disabled
func TestHandleTest_DisabledNotifier_409(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	// sound は disabled
	cfg.Notifiers.Sound.Enabled = false

	handler := daemon.HandleTest(d, []notifier.Notifier{fakeN}, cfg.Notifiers)

	body, _ := json.Marshal(apiclient.TestRequest{
		Kind:           event.KindStop,
		TargetNotifier: "sound",
		Title:          "test",
		Body:           "body",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("HandleTest disabled notifier: got %d, want 409", w.Code)
	}

	var errResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errResp); err == nil {
		if errObj, ok := errResp["error"].(map[string]interface{}); ok {
			if code := errObj["code"]; code != "conflict_disabled" {
				t.Errorf("error.code: got %v, want \"conflict_disabled\"", code)
			}
		}
	}
}

// T-092 / D-40: HandleTest — kind_mask 不一致 → 409 Conflict
func TestHandleTest_KindMaskMismatch_409(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	// toast: KindMask を KindNotification のみ許可 → KindStop は masked
	cfg.Notifiers.Toast.Enabled = true
	cfg.Notifiers.Toast.KindMask = map[event.EventKind]bool{
		event.KindNotification: true,
	}

	handler := daemon.HandleTest(d, []notifier.Notifier{fakeN}, cfg.Notifiers)

	body, _ := json.Marshal(apiclient.TestRequest{
		Kind:           event.KindStop, // KindMask で拒否される
		TargetNotifier: "toast",
		Title:          "test",
		Body:           "body",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("HandleTest kind_mask mismatch: got %d, want 409", w.Code)
	}
}

// T-093 / D-40: HandleTest — 未知 target_notifier → 409 Conflict
func TestHandleTest_UnknownNotifier_409(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	handler := daemon.HandleTest(d, []notifier.Notifier{fakeN}, cfg.Notifiers)

	body, _ := json.Marshal(apiclient.TestRequest{
		Kind:           event.KindStop,
		TargetNotifier: "nonexistent-notifier",
		Title:          "test",
		Body:           "body",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("HandleTest unknown notifier: got %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
}

// T-094 / B3 (503): Dispatcher.Close 後の HandleTest → 503 shutting_down
func TestHandleTest_DispatcherClosing_503(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()

	// Dispatcher を閉じる
	cancel()
	_ = bus.Close(context.Background())
	shutdownCtx, c2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer c2()
	_ = d.Close(shutdownCtx)

	cfg := defaultTestConfig()
	handler := daemon.HandleTest(d, []notifier.Notifier{fakeN}, cfg.Notifiers)

	body, _ := json.Marshal(apiclient.TestRequest{
		Kind:           event.KindStop,
		TargetNotifier: "all",
		Title:          "test",
		Body:           "body",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("HandleTest closing dispatcher: got %d, want 503 (body=%s)", w.Code, w.Body.String())
	}

	var errResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errResp); err == nil {
		if errObj, ok := errResp["error"].(map[string]interface{}); ok {
			if code := errObj["code"]; code != "shutting_down" {
				t.Errorf("error.code: got %v, want \"shutting_down\"", code)
			}
		}
	}
}

// T-094b / D-40: HandleTest — webhook.discord が disabled → 409
func TestHandleTest_WebhookDiscordDisabled_409(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	cfg.Notifiers.Webhook.Discord.Enabled = false

	handler := daemon.HandleTest(d, []notifier.Notifier{fakeN}, cfg.Notifiers)

	body, _ := json.Marshal(apiclient.TestRequest{
		Kind:           event.KindStop,
		TargetNotifier: "webhook.discord",
		Title:          "test",
		Body:           "body",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("HandleTest webhook.discord disabled: got %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
}

// T-094c / D-40: HandleTest — webhook.slack が disabled → 409
func TestHandleTest_WebhookSlackDisabled_409(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	cfg.Notifiers.Webhook.Slack.Enabled = false

	handler := daemon.HandleTest(d, []notifier.Notifier{fakeN}, cfg.Notifiers)

	body, _ := json.Marshal(apiclient.TestRequest{
		Kind:           event.KindStop,
		TargetNotifier: "webhook.slack",
		Title:          "test",
		Body:           "body",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("HandleTest webhook.slack disabled: got %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
}

// T-094d / D-40: HandleTest — webhook.discord KindMask 不一致 → 409
func TestHandleTest_WebhookDiscordKindMask_409(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	cfg.Notifiers.Webhook.Discord.Enabled = true
	cfg.Notifiers.Webhook.Discord.KindMask = map[event.EventKind]bool{
		event.KindNotification: true,
	}

	handler := daemon.HandleTest(d, []notifier.Notifier{fakeN}, cfg.Notifiers)

	body, _ := json.Marshal(apiclient.TestRequest{
		Kind:           event.KindStop, // マスクされる
		TargetNotifier: "webhook.discord",
		Title:          "test",
		Body:           "body",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("HandleTest webhook.discord kind_mask: got %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
}

// T-094e / D-40: HandleTest — webhook.slack KindMask 不一致 → 409
func TestHandleTest_WebhookSlackKindMask_409(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	cfg.Notifiers.Webhook.Slack.Enabled = true
	cfg.Notifiers.Webhook.Slack.KindMask = map[event.EventKind]bool{
		event.KindNotification: true,
	}

	handler := daemon.HandleTest(d, []notifier.Notifier{fakeN}, cfg.Notifiers)

	body, _ := json.Marshal(apiclient.TestRequest{
		Kind:           event.KindStop, // マスクされる
		TargetNotifier: "webhook.slack",
		Title:          "test",
		Body:           "body",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("HandleTest webhook.slack kind_mask: got %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
}

// T-095 / D-35: HandleTest の target_notifier="" (空) → "all" と同様に全体に dispatch
func TestHandleTest_EmptyTargetNotifier_DispatchAll(t *testing.T) {
	t.Parallel()

	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()
	acceptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeN := notifier.NewFakeNotifier("toast")
	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{fakeN}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		c, c2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer c2()
		_ = d.Close(c)
	}()

	cfg := defaultTestConfig()
	handler := daemon.HandleTest(d, []notifier.Notifier{fakeN}, cfg.Notifiers)

	body, _ := json.Marshal(apiclient.TestRequest{
		Kind:           event.KindStop,
		TargetNotifier: "", // 空 = all と同じ扱い
		Title:          "test",
		Body:           "body",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	// 400/409/503 でなければ 200 扱い (dispatch が成功した)
	if w.Code != http.StatusOK {
		t.Errorf("HandleTest empty target_notifier: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
}

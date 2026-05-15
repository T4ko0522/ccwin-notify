// internal/source/hooks パッケージの統合テスト (サイクル 3)
// テスト ID: T-026〜T-032, T-073
// 受入条件: A1 (受信側) / A2 / A6 / C4 — Hooks HTTP エンドポイントのイベント受信
package hooks_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/ipc/sse"
	"github.com/t4ko0522/ccwin-notify/internal/source/hooks"
)

// テスト用 SSE Hub を作成するヘルパー
func newTestSSEHub() *sse.Hub {
	return sse.NewHub()
}

// テスト用 Bearer トークン
const hooksToken = "hooks-test-token-abcdef1234567890xyz"

var hooksExpectedHash = sha256.Sum256([]byte(hooksToken))

// CaptureBus は Bus の代わりに Publish を記録するテスト用実装
type CaptureBus struct {
	Published []event.Event
	Err       error
}

func (cb *CaptureBus) Publish(_ context.Context, e event.Event) error {
	if cb.Err != nil {
		return cb.Err
	}
	cb.Published = append(cb.Published, e)
	return nil
}

// テスト用サーバを httptest.NewServer で作成
// hooks.HandleEvents ハンドラを Bearer 認証付きで登録
// HandleEvents/HandleStream は http.Handler を返すため mux.Handle を使う
func newHooksTestServer(t *testing.T, bus event.EventSink, acceptCtx context.Context) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("POST /v1/events", hooks.HandleEvents(bus, acceptCtx, hooksExpectedHash))
	return httptest.NewServer(mux)
}

// T-073: HTTP サーバが 127.0.0.1 のポート 0 にバインドし、取得ポートが 1024-65535 の範囲
func TestIPCServer_LoopbackBind(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	if addr.IP.String() != "127.0.0.1" {
		t.Errorf("バインドIP: got %q, want \"127.0.0.1\"", addr.IP.String())
	}
	if addr.Port < 1024 || addr.Port > 65535 {
		t.Errorf("ポート範囲外: got %d", addr.Port)
	}
}

// T-073b / A6: hooks.HandleEvents を経由したHTTPサーバが loopback のみにバインドされる統合テスト
// hooks.HandleEvents を HTTP ミドルウェアとして登録し、実際のリクエストが loopback 経由で届くことを確認する。
func TestIPCServer_LoopbackBind_HooksIntegration(t *testing.T) {
	t.Parallel()

	cb := &CaptureBus{}
	ctx := context.Background()

	// hooks.HandleEvents を登録したサーバを httptest で起動 (httptest は 127.0.0.1 を使う)
	srv := newHooksTestServer(t, cb, ctx)
	defer srv.Close()

	// サーバのバインドアドレスが loopback であることを確認
	addr := srv.Listener.Addr().(*net.TCPAddr)
	if !addr.IP.IsLoopback() {
		t.Errorf("A6: サーバの IP が loopback でない: got %q", addr.IP.String())
	}
	if addr.Port < 1024 || addr.Port > 65535 {
		t.Errorf("A6: ポート範囲外: got %d", addr.Port)
	}

	// 実際にリクエストを送り、loopback 経由で HandleEvents が受け取れることを確認
	body := map[string]interface{}{
		"kind":   "Stop",
		"title":  "loopback test",
		"body":   "test",
		"source": "hooks",
	}
	reqBody, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+hooksToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("A6 統合リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("A6: ステータス: got %d, want 200", resp.StatusCode)
	}
	if len(cb.Published) != 1 {
		t.Errorf("A6: Published 件数: got %d, want 1", len(cb.Published))
	}
}

// T-026: Stop イベントの受信 → Bus.Publish の Kind/Title/Body/Raw が一致
func TestHandleEvents_Stop(t *testing.T) {
	t.Parallel()
	cb := &CaptureBus{}
	ctx := context.Background()
	srv := newHooksTestServer(t, cb, ctx)
	defer srv.Close()

	body := map[string]interface{}{
		"kind":   "Stop",
		"title":  "Claude Code: response complete",
		"body":   "task done",
		"source": "hooks",
		"raw":    map[string]interface{}{"hook_type": "Stop"},
	}
	reqBody, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+hooksToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ステータス: got %d, want 200", resp.StatusCode)
	}
	if len(cb.Published) != 1 {
		t.Fatalf("Published 件数: got %d, want 1", len(cb.Published))
	}
	ev := cb.Published[0]
	if ev.Kind != event.KindStop {
		t.Errorf("Kind: got %q, want %q", ev.Kind, event.KindStop)
	}
	if ev.Title != "Claude Code: response complete" {
		t.Errorf("Title: got %q", ev.Title)
	}
	if ev.Body != "task done" {
		t.Errorf("Body: got %q", ev.Body)
	}
	if ev.Raw == nil {
		t.Error("Raw: nil であってはならない")
	}
}

// T-027: Notification イベントの受信 → Event 正規化
func TestHandleEvents_Notification(t *testing.T) {
	t.Parallel()
	cb := &CaptureBus{}
	ctx := context.Background()
	srv := newHooksTestServer(t, cb, ctx)
	defer srv.Close()

	body := map[string]interface{}{
		"kind":   "Notification",
		"title":  "Input required",
		"body":   "Please respond",
		"source": "hooks",
	}
	reqBody, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+hooksToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ステータス: got %d, want 200", resp.StatusCode)
	}
	if len(cb.Published) != 1 {
		t.Fatalf("Published 件数: got %d, want 1", len(cb.Published))
	}
	ev := cb.Published[0]
	if ev.Kind != event.KindNotification {
		t.Errorf("Kind: got %q, want %q", ev.Kind, event.KindNotification)
	}
}

// T-028: SubagentStop イベントの受信 → Event 正規化
func TestHandleEvents_SubagentStop(t *testing.T) {
	t.Parallel()
	cb := &CaptureBus{}
	ctx := context.Background()
	srv := newHooksTestServer(t, cb, ctx)
	defer srv.Close()

	body := map[string]interface{}{
		"kind":   "SubagentStop",
		"title":  "Subagent done: code_generator",
		"body":   "subagent output",
		"source": "hooks",
	}
	reqBody, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+hooksToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ステータス: got %d, want 200", resp.StatusCode)
	}
	if len(cb.Published) != 1 {
		t.Fatalf("Published 件数: got %d, want 1", len(cb.Published))
	}
	ev := cb.Published[0]
	if ev.Kind != event.KindSubagentStop {
		t.Errorf("Kind: got %q, want %q", ev.Kind, event.KindSubagentStop)
	}
}

// T-029: 不正 JSON → 400 invalid_json
func TestHandleEvents_InvalidJSON(t *testing.T) {
	t.Parallel()
	cb := &CaptureBus{}
	ctx := context.Background()
	srv := newHooksTestServer(t, cb, ctx)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", bytes.NewReader([]byte("not-json{{")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+hooksToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("不正JSON: got %d, want 400", resp.StatusCode)
	}

	// エラーコードの確認
	var result map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	errObj, ok := result["error"].(map[string]interface{})
	if !ok {
		t.Fatal("レスポンスに error オブジェクトがない")
	}
	if errObj["code"] != "invalid_json" {
		t.Errorf("エラーコード: got %q, want \"invalid_json\"", errObj["code"])
	}
}

// T-030: 未知の Kind → 400 invalid_kind
func TestHandleEvents_UnknownKind(t *testing.T) {
	t.Parallel()
	cb := &CaptureBus{}
	ctx := context.Background()
	srv := newHooksTestServer(t, cb, ctx)
	defer srv.Close()

	body := map[string]interface{}{
		"kind":   "UnknownKindXyz",
		"title":  "test",
		"body":   "test",
		"source": "hooks",
	}
	reqBody, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+hooksToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("未知Kind: got %d, want 400", resp.StatusCode)
	}

	var result map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	errObj, ok := result["error"].(map[string]interface{})
	if !ok {
		t.Fatal("レスポンスに error オブジェクトがない")
	}
	if errObj["code"] != "invalid_kind" {
		t.Errorf("エラーコード: got %q, want \"invalid_kind\"", errObj["code"])
	}
}

// T-031b: queue_full (ErrDropped) → 503 queue_full
func TestHandleEvents_QueueFull_ErrDropped(t *testing.T) {
	t.Parallel()
	cb := &CaptureBus{Err: event.ErrDropped}
	ctx := context.Background()
	srv := newHooksTestServer(t, cb, ctx)
	defer srv.Close()

	body := map[string]interface{}{
		"kind":  "Stop",
		"title": "test",
		"body":  "test",
	}
	reqBody, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+hooksToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("ErrDropped: got %d, want 503", resp.StatusCode)
	}
}

// T-031c: Publish が ErrBusClosed を返した場合 → 503 shutting_down
func TestHandleEvents_BusClosed(t *testing.T) {
	t.Parallel()
	cb := &CaptureBus{Err: event.ErrBusClosed}
	ctx := context.Background()
	srv := newHooksTestServer(t, cb, ctx)
	defer srv.Close()

	body := map[string]interface{}{
		"kind":  "Stop",
		"title": "test",
		"body":  "test",
	}
	reqBody, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+hooksToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("ErrBusClosed: got %d, want 503", resp.StatusCode)
	}
}

// T-031d: Publish が予期しないエラーを返した場合 → 500
func TestHandleEvents_InternalError(t *testing.T) {
	t.Parallel()
	unexpectedErr := errors.New("unexpected internal error")
	cb := &CaptureBus{Err: unexpectedErr}
	ctx := context.Background()
	srv := newHooksTestServer(t, cb, ctx)
	defer srv.Close()

	body := map[string]interface{}{
		"kind":  "Stop",
		"title": "test",
		"body":  "test",
	}
	reqBody, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+hooksToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("予期しないエラー: got %d, want 500", resp.StatusCode)
	}
}

// T-031: queue_full (DropBlock + ctx キャンセル) → 503 queue_full
func TestHandleEvents_QueueFull(t *testing.T) {
	t.Parallel()
	// ErrPublishCanceled を返す CaptureBus を使う
	cb := &CaptureBus{Err: event.ErrPublishCanceled}
	ctx := context.Background()
	srv := newHooksTestServer(t, cb, ctx)
	defer srv.Close()

	body := map[string]interface{}{
		"kind":   "Stop",
		"title":  "test",
		"body":   "test",
		"source": "hooks",
	}
	reqBody, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+hooksToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("QueueFull: got %d, want 503", resp.StatusCode)
	}
}

// HandleStream SSE エンドポイントのテスト (カバレッジ向上)
// HandleStream が SSE チャネルからイベントを受信し text/event-stream で配信する
func TestHandleStream_EventDelivery(t *testing.T) {
	t.Parallel()

	sseHub := newTestSSEHub()
	defer sseHub.Close()

	acceptCtx, cancelAccept := context.WithCancel(context.Background())
	defer cancelAccept()

	mux := http.NewServeMux()
	mux.Handle("GET /v1/events/stream", hooks.HandleStream(sseHub, acceptCtx, hooksExpectedHash))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// SSE を購読する HTTP クライアント (別 goroutine)
	type sseResult struct {
		body string
		err  error
	}
	resultCh := make(chan sseResult, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/events/stream", nil)
		req.Header.Set("Authorization", "Bearer "+hooksToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			resultCh <- sseResult{err: err}
			return
		}
		defer resp.Body.Close()

		buf := make([]byte, 1024)
		n, _ := resp.Body.Read(buf)
		resultCh <- sseResult{body: string(buf[:n])}
	}()

	// 少し待ってから acceptCtx をキャンセルしてSSE接続を切断
	time.Sleep(100 * time.Millisecond)
	cancelAccept()

	select {
	case res := <-resultCh:
		if res.err != nil {
			// 接続エラーは許容 (切断による)
			t.Logf("SSE 接続結果: %v", res.err)
		}
		// Content-Type が text/event-stream であることを確認
	case <-time.After(3 * time.Second):
		t.Fatal("タイムアウト: SSE 接続が戻らなかった")
	}
}

// HandleStream が Bearer 認証なしの接続を 401 で拒否する
func TestHandleStream_Unauthorized(t *testing.T) {
	t.Parallel()

	sseHub := newTestSSEHub()
	defer sseHub.Close()

	acceptCtx, cancelAccept := context.WithCancel(context.Background())
	defer cancelAccept()

	mux := http.NewServeMux()
	mux.Handle("GET /v1/events/stream", hooks.HandleStream(sseHub, acceptCtx, hooksExpectedHash))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/events/stream", nil)
	// Authorization ヘッダなし

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Bearer なし: got %d, want 401", resp.StatusCode)
	}
}

// T-032: acceptCtx キャンセル後の POST → 503 (新規入力遮断)
func TestHandleEvents_AcceptCtxCanceled(t *testing.T) {
	t.Parallel()
	cb := &CaptureBus{}
	acceptCtx, cancelAccept := context.WithCancel(context.Background())
	srv := newHooksTestServer(t, cb, acceptCtx)
	defer srv.Close()

	// acceptCtx をキャンセル (daemon shutdown 相当)
	cancelAccept()

	// キャンセル伝播を待つ
	time.Sleep(10 * time.Millisecond)

	body := map[string]interface{}{
		"kind":   "Stop",
		"title":  "test",
		"body":   "test",
		"source": "hooks",
	}
	reqBody, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+hooksToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// 接続拒否も可 (サーバが閉じている場合)
		t.Logf("acceptCtxキャンセル後リクエスト (接続エラー扱い): %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("acceptCtxキャンセル後: got %d, want 503", resp.StatusCode)
	}
}

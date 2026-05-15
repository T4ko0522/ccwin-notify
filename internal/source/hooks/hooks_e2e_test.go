// E2E 統合テスト: Hooks HTTP → event.Bus → FakeNotifier
// テスト ID: T-E01 / T-E02
// 受入条件: E3 — Hooks POST → Event → Notifier の end-to-end フロー
//
// このテストは production コードの重要な統合パスを検証する:
//   1. hooks.HandleEvents が POST を受け取り Bus.Publish する
//   2. event.Bus が Subscribe チャネルに Event を配信する
//   3. Consumer goroutine が FakeNotifier.Notify を呼ぶ
//   4. sse.Hub 経由で SSE 購読者に "event-published" が届く
//
// NOTE: daemon.Dispatcher を使わず event.Bus を直接読む Consumer goroutine を使う。
// これにより daemon パッケージのビルド状態に依存せず E2E フローを検証できる。
package hooks_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/ipc/sse"
	"github.com/t4ko0522/ccwin-notify/internal/notifier"
	"github.com/t4ko0522/ccwin-notify/internal/source/hooks"
)

const e2eToken = "e2e-test-token-abcdef1234567890xyz"

var e2eExpectedHash = sha256.Sum256([]byte(e2eToken))

// T-E01 / E3: Hooks POST → event.Bus → Consumer → FakeNotifier の end-to-end フロー
func TestHooks_E2E_PostToNotifier(t *testing.T) {
	t.Parallel()

	// event.Bus (capacity=16, DropOldest)
	bus := event.NewBus(16, event.DropOldest)

	// FakeNotifier
	fakeNotifier := notifier.NewFakeNotifier("toast")
	waitCh := fakeNotifier.WaitForN(1) // 1件通知を待つチャネル

	// acceptCtx
	acceptCtx, cancelAccept := context.WithCancel(context.Background())
	defer cancelAccept()

	// Consumer goroutine: bus.Subscribe() → FakeNotifier.Notify
	go func() {
		ch := bus.Subscribe()
		for ev := range ch {
			_ = fakeNotifier.Notify(acceptCtx, ev)
		}
	}()

	// hooks.HandleEvents を HTTP サーバに登録
	mux := http.NewServeMux()
	mux.Handle("POST /v1/events", hooks.HandleEvents(bus, acceptCtx, e2eExpectedHash))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// POST /v1/events を送信
	body := map[string]interface{}{
		"kind":   "Stop",
		"title":  "E2E test: response complete",
		"body":   "e2e task done",
		"source": "hooks",
	}
	reqBody, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e2eToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("E2E POST 失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("E2E POST ステータス: got %d, want 200", resp.StatusCode)
	}

	// FakeNotifier に届くまで待つ (WaitForN チャネル)
	select {
	case <-waitCh:
		// 1件通知された
	case <-time.After(5 * time.Second):
		t.Fatalf("E2E: タイムアウト — FakeNotifier に Event が届かなかった (count=%d)", fakeNotifier.Len())
	}

	// 検証
	if fakeNotifier.Len() != 1 {
		t.Errorf("E2E: FakeNotifier.Records 件数: got %d, want 1", fakeNotifier.Len())
	}

	ev, ok := fakeNotifier.Get(0)
	if !ok {
		t.Fatal("E2E: FakeNotifier.Get(0) が false (Records に1件あるはず)")
	}

	if ev.Kind != event.KindStop {
		t.Errorf("E2E: Event.Kind: got %q, want %q", ev.Kind, event.KindStop)
	}
	if ev.Title != "E2E test: response complete" {
		t.Errorf("E2E: Event.Title: got %q", ev.Title)
	}
	if ev.Body != "e2e task done" {
		t.Errorf("E2E: Event.Body: got %q", ev.Body)
	}

	// クリーンアップ
	cancelAccept()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := bus.Close(shutdownCtx); err != nil {
		t.Logf("E2E Bus.Close: %v", err)
	}
}

// T-E02 / E3: SSE Hub を経由して購読者に "event-published" が届く
func TestHooks_E2E_SSEEventPublished(t *testing.T) {
	t.Parallel()

	// event.Bus
	bus := event.NewBus(16, event.DropOldest)

	// sse.Hub
	sseHub := sse.NewHub()
	defer sseHub.Close()

	// SSE を購読
	sseCtx, sseCancel := context.WithCancel(context.Background())
	defer sseCancel()
	sseCh, _ := sseHub.Subscribe(sseCtx)

	// acceptCtx
	acceptCtx, cancelAccept := context.WithCancel(context.Background())
	defer cancelAccept()

	// Consumer goroutine: bus.Subscribe() → sseHub.Publish("event-published")
	go func() {
		ch := bus.Subscribe()
		for ev := range ch {
			sseHub.Publish(sse.SSEMessage{
				Kind:    "event-published",
				Payload: ev,
			})
		}
	}()

	// hooks.HandleEvents サーバを起動
	mux := http.NewServeMux()
	mux.Handle("POST /v1/events", hooks.HandleEvents(bus, acceptCtx, e2eExpectedHash))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// POST /v1/events
	body := map[string]interface{}{
		"kind":   "Notification",
		"title":  "SSE test",
		"body":   "check SSE delivery",
		"source": "hooks",
	}
	reqBody, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e2eToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("E2E SSE POST 失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("E2E SSE POST ステータス: got %d, want 200", resp.StatusCode)
	}

	// SSE Hub から "event-published" メッセージを受信するまで待つ
	select {
	case msg, ok := <-sseCh:
		if !ok {
			t.Fatal("E2E SSE: チャネルが close された (Hub が終了?)")
		}
		if msg.Kind != "event-published" {
			t.Errorf("E2E SSE: msg.Kind: got %q, want \"event-published\"", msg.Kind)
		}
		// Payload が event.Event であることを確認
		ev, ok := msg.Payload.(event.Event)
		if !ok {
			t.Errorf("E2E SSE: Payload の型が event.Event でない: %T", msg.Payload)
		} else if ev.Kind != event.KindNotification {
			t.Errorf("E2E SSE: Event.Kind: got %q, want %q", ev.Kind, event.KindNotification)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("E2E SSE: タイムアウト — SSE Hub から event-published が届かなかった")
	}

	// クリーンアップ
	cancelAccept()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := bus.Close(shutdownCtx); err != nil {
		t.Logf("E2E SSE Bus.Close: %v", err)
	}
}

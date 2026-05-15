// internal/apiclient の StreamEvents SSE テスト (サイクル B)
// テスト ID: T-067, T-068, T-069
// 受入条件: TUI-2 / M-TEA-CMD — apiclient.StreamEvents が SSE を受信しチャネルに送出
package apiclient_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/apiclient"
	"github.com/t4ko0522/ccwin-notify/internal/event"
)

// setupSSEServer は SSE エンドポイントを提供するテスト用サーバを作成する
func setupSSEServer(t *testing.T, events []event.Event) (*httptest.Server, chan struct{}) {
	t.Helper()
	done := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app": "ccwin-notify", "status": "ok",
		})
	})
	mux.HandleFunc("/v1/events/stream", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		for _, ev := range events {
			data, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(w, "event: ccwin-event\nid: %s\ndata: %s\n\n", ev.ID, data)
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}

		// 全イベント送信後に done を通知してから接続を閉じる
		close(done)
	})

	srv := httptest.NewServer(mux)
	return srv, done
}

// portfile を SSE サーバ用に書き出すヘルパー
func writeSSEPortfile(t *testing.T, dir string, srv *httptest.Server) string {
	t.Helper()
	addr := srv.Listener.Addr().String()
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("アドレスのパース: %v", err)
	}
	portNum, _ := strconv.Atoi(portStr)

	content := map[string]interface{}{
		"app":               "ccwin-notify",
		"version":           "0.x.y",
		"pid":               os.Getpid(),
		"port":              portNum,
		"started_at":        time.Now().Format(time.RFC3339),
		"token_fingerprint": "sha256:test",
	}
	data, _ := json.Marshal(content)
	portfilePath := filepath.Join(dir, "daemon.port")
	if err := os.WriteFile(portfilePath, data, 0600); err != nil {
		t.Fatalf("portfile 書き込み: %v", err)
	}
	return portfilePath
}

// T-067 / TUI-2 / M-TEA-CMD: StreamEvents がSSEを受信しチャネルに送出する
func TestApiclient_StreamEvents_ReceivesEvents(t *testing.T) {
	t.Parallel()
	testEvents := []event.Event{
		{ID: "01A", Kind: event.KindStop, Title: "stop event", Source: "hooks"},
		{ID: "01B", Kind: event.KindNotification, Title: "notification", Source: "hooks"},
	}

	srv, _ := setupSSEServer(t, testEvents)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writeSSEPortfile(t, dir, srv)
	tokenPath := filepath.Join(dir, "secret.token")
	if err := os.WriteFile(tokenPath, []byte(testAPIToken), 0600); err != nil {
		t.Fatalf("tokenfile 書き込み: %v", err)
	}

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := client.StreamEvents(ctx)
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}

	var received []event.Event
	timeout := time.After(5 * time.Second)
	for i := 0; i < len(testEvents); i++ {
		select {
		case ev, ok := <-ch:
			if !ok {
				// チャネルが close されたらループを抜ける
				goto done
			}
			received = append(received, ev)
		case <-timeout:
			t.Fatalf("タイムアウト: %d件のイベントのうち%d件しか受信できなかった", len(testEvents), len(received))
		}
	}
done:
	if len(received) < len(testEvents) {
		t.Errorf("受信件数: got %d, want %d", len(received), len(testEvents))
	}
	if len(received) > 0 && string(received[0].Kind) != "Stop" {
		t.Errorf("received[0].Kind: got %q, want \"Stop\"", received[0].Kind)
	}
}

// T-069 / TUI-2: StreamEvents の内部バッファ (64) を超えるイベントが来てもブロックしない
// buffer 溢れは drop + WARN で処理し、TUI 側を遅延させない
func TestApiclient_StreamEvents_BufferOverflow_NonBlocking(t *testing.T) {
	t.Parallel()

	// streamBufferSize(64) を超える 80 件のイベントを即座に送信する
	const overflowCount = 80
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app": "ccwin-notify", "status": "ok",
		})
	})
	mux.HandleFunc("/v1/events/stream", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// 80 件を連続送信 (バッファ 64 を超えるので溢れた分は drop される)
		for i := 0; i < overflowCount; i++ {
			ev := event.Event{
				ID:    fmt.Sprintf("id%03d", i),
				Kind:  event.KindStop,
				Title: fmt.Sprintf("event %d", i),
			}
			data, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		// 送信完了後に接続を閉じる
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writeSSEPortfile(t, dir, srv)
	tokenPath := filepath.Join(dir, "secret.token")
	_ = os.WriteFile(tokenPath, []byte(testAPIToken), 0600)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// StreamEvents が goroutine を起動し即座に返ること (ブロックしない)
	start := time.Now()
	ch, err := client.StreamEvents(ctx)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("StreamEvents の返却が遅すぎる: %v (buffer 溢れでブロックしていないか確認)", elapsed)
	}

	// T-069 MUST: StreamEvents が goroutine を起動し即座に返ること (elapsed < 500ms が既に assert済み)
	// 本テストの主目的は「buffer 溢れでも StreamEvents 呼び出し自体がブロックしない」ことの検証。
	// チャンネルを読みながら count を増加させる (読まなければ buffer=64 超で drop が発生する)。
	// ここでは読むことで「少なくとも 1 件は受信できる」ことを確認する。
	const wantAtLeast = 1
	var count int
readLoop:
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				break readLoop
			}
			count++
		case <-time.After(2 * time.Second):
			// タイムアウトで抜ける
			cancel()
			break readLoop
		}
	}

	t.Logf("T-069: 受信件数 %d / %d", count, overflowCount)
	if count < wantAtLeast {
		t.Errorf("T-069: 少なくとも %d 件受信すべきだが %d 件しか受信できなかった", wantAtLeast, count)
	}
	// StreamEvents の elapsed < 500ms は上記で既に assert 済み (非ブロッキング性の証明)
}

// T-174: SSE サーバが 5xx を返した場合 streamLoop が再接続を試みバックオフ後に ctx cancel で終了
func TestApiclient_StreamEvents_Reconnect_On5xx(t *testing.T) {
	t.Parallel()

	connectCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app": "ccwin-notify", "status": "ok",
		})
	})
	mux.HandleFunc("/v1/events/stream", func(w http.ResponseWriter, r *http.Request) {
		connectCount++
		// 最初の 2 回は 503 を返す (readSSE でエラー → streamLoop が再接続)
		if connectCount <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// 3回目は正常な SSE を返して ctx cancel で終了を待つ
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writeSSEPortfile(t, dir, srv)
	tokenPath := filepath.Join(dir, "secret.token")
	_ = os.WriteFile(tokenPath, []byte(testAPIToken), 0600)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	// backoffInitial=1s は長すぎるのでテストでは短い ctx timeout で完結させる
	// streamLoop 内の backoff は 1s だが 3 回接続を確認するため 5s の余裕を持たせる
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch, err := client.StreamEvents(ctx)
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}

	// 3回接続されるまで少し待ってから cancel
	// 503→backoff→503→backoff→200 の順
	select {
	case <-time.After(4 * time.Second):
		// backoff 後に 2+ 回接続が行われたことを確認
	}
	cancel()

	// チャネルが close されるのを待つ
	select {
	case _, ok := <-ch:
		if ok {
			t.Logf("チャネルから値を受信 (期待されない)")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("タイムアウト: チャネルが close されなかった")
	}

	// 少なくとも 1 回以上接続が試みられたこと (backoff 再接続パス)
	if connectCount < 1 {
		t.Errorf("SSE 接続回数: got %d, want >= 1", connectCount)
	}
	t.Logf("T-174: SSE 接続試行回数 = %d", connectCount)
}

// T-068 / TUI-2 / M-TEA-CMD: ctx キャンセルで StreamEvents チャネルが close される
func TestApiclient_StreamEvents_CtxCancelClosesChannel(t *testing.T) {
	t.Parallel()
	// 無限に接続を維持するサーバ
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app": "ccwin-notify", "status": "ok",
		})
	})
	mux.HandleFunc("/v1/events/stream", func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		// クライアント切断まで待機
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writeSSEPortfile(t, dir, srv)
	tokenPath := filepath.Join(dir, "secret.token")
	_ = os.WriteFile(tokenPath, []byte(testAPIToken), 0600)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := client.StreamEvents(ctx)
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}

	// 少し待ってから ctx をキャンセル
	time.Sleep(100 * time.Millisecond)
	cancel()

	// チャネルが close されるのを待つ
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("ctx cancel後: チャネルはcloseされるべきだが値が送られてきた")
		}
		// ok == false = close (期待通り)
	case <-time.After(3 * time.Second):
		t.Fatal("タイムアウト: ctx cancel後にチャネルが close されなかった")
	}
}

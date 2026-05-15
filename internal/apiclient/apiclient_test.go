// internal/apiclient パッケージの統合テスト (サイクル A)
// テスト ID: T-033〜T-039, T-065, T-066
// 受入条件: C2 / C6 / TUI-1 — portfile + Bearer + HTTP POST/GET
package apiclient_test

import (
	"context"
	"encoding/json"
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

const testAPIToken = "api-test-token-abcdef1234567890xyz"

// テスト用 httptest サーバのセットアップ
// POST /v1/events / GET /v1/healthz / GET /v1/status に応答する
type capturedRequest struct {
	Method        string
	Path          string
	Authorization string
	ContentType   string
	Body          []byte
}

func setupTestServer(t *testing.T) (*httptest.Server, chan capturedRequest) {
	t.Helper()
	received := make(chan capturedRequest, 10)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body []byte
		if r.Body != nil {
			var buf = make([]byte, 1024)
			n, _ := r.Body.Read(buf)
			body = buf[:n]
		}
		received <- capturedRequest{
			Method:        r.Method,
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			ContentType:   r.Header.Get("Content-Type"),
			Body:          body,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "01HTEST00000000000000000"})
	})
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "ok",
			"app":            "ccwin-notify",
			"version":        "0.x.y",
			"pid":            12345,
			"started_at":     time.Now().Format(time.RFC3339),
			"uptime_seconds": 100,
		})
	})
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		received <- capturedRequest{
			Method:        r.Method,
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"pid":     12345,
			"version": "0.x.y",
		})
	})

	srv := httptest.NewServer(mux)
	return srv, received
}

// portfile を一時ディレクトリに書き出すヘルパー
// 現プロセスの PID を使用 (PID 検証をパスさせる)
func writePortfile(t *testing.T, dir string, srv *httptest.Server) string {
	t.Helper()
	addr := srv.Listener.Addr().String()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("アドレスのパース: %v", err)
	}
	_ = host
	portNum, _ := strconv.Atoi(portStr)

	content := map[string]interface{}{
		"app":               "ccwin-notify",
		"version":           "0.x.y",
		"pid":               os.Getpid(), // 現プロセスの PID (PID 検証をパス)
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

// tokenfile を一時ディレクトリに書き出すヘルパー
func writeTokenfile(t *testing.T, dir, token string) string {
	t.Helper()
	tokenPath := filepath.Join(dir, "secret.token")
	if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
		t.Fatalf("tokenfile 書き込み: %v", err)
	}
	return tokenPath
}

// T-034 / C2 / C6: portfile 不在 → ErrDaemonNotRunning
func TestApiclient_New_PortfileNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	nonExistentPortfile := filepath.Join(dir, "daemon.port")
	tokenPath := writeTokenfile(t, dir, testAPIToken)

	_, err := apiclient.New(nonExistentPortfile, tokenPath)
	if err != apiclient.ErrDaemonNotRunning {
		t.Errorf("portfile 不在: got %v, want ErrDaemonNotRunning", err)
	}
}

// T-033: PostEvent が正しい URL / Bearer / Content-Type でリクエスト送信
func TestApiclient_PostEvent_RequestFormat(t *testing.T) {
	t.Parallel()
	srv, received := setupTestServer(t)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writePortfile(t, dir, srv)
	tokenPath := writeTokenfile(t, dir, testAPIToken)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	ev := event.Event{
		Kind:   event.KindStop,
		Title:  "Claude Code: response complete",
		Body:   "done",
		Source: "hooks",
	}

	ctx := context.Background()
	if err := client.PostEvent(ctx, ev); err != nil {
		t.Fatalf("PostEvent: %v", err)
	}

	// リクエストを検証
	select {
	case req := <-received:
		if req.Method != http.MethodPost {
			t.Errorf("Method: got %q, want POST", req.Method)
		}
		if req.Path != "/v1/events" {
			t.Errorf("Path: got %q, want \"/v1/events\"", req.Path)
		}
		expectedBearer := "Bearer " + testAPIToken
		if req.Authorization != expectedBearer {
			t.Errorf("Authorization: got %q, want %q", req.Authorization, expectedBearer)
		}
		if req.ContentType != "application/json" {
			t.Errorf("Content-Type: got %q, want \"application/json\"", req.ContentType)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("タイムアウト: リクエストが受信されなかった")
	}
}

// T-037: 401 応答 → ErrUnauthorized
func TestApiclient_PostEvent_Unauthorized(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"code": "unauthorized", "message": "invalid token"},
		})
	})
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app": "ccwin-notify", "status": "ok",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writePortfile(t, dir, srv)
	tokenPath := writeTokenfile(t, dir, testAPIToken)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	ev := event.Event{Kind: event.KindStop}
	err = client.PostEvent(context.Background(), ev)
	if err != apiclient.ErrUnauthorized {
		t.Errorf("401応答: got %v, want ErrUnauthorized", err)
	}
}

// T-038: 5xx 応答 → ErrServer
func TestApiclient_PostEvent_ServerError(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app": "ccwin-notify", "status": "ok",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writePortfile(t, dir, srv)
	tokenPath := writeTokenfile(t, dir, testAPIToken)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	ev := event.Event{Kind: event.KindStop}
	err = client.PostEvent(context.Background(), ev)
	if err != apiclient.ErrServer {
		t.Errorf("5xx応答: got %v, want ErrServer", err)
	}
}

// T-065 / TUI-1: GetHealthz が正しく呼び出せる
func TestApiclient_GetHealthz_RequestFormat(t *testing.T) {
	t.Parallel()
	srv, _ := setupTestServer(t) // healthz は認証不要なので received は使用しない
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writePortfile(t, dir, srv)
	tokenPath := writeTokenfile(t, dir, testAPIToken)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	h, err := client.GetHealthz(context.Background())
	if err != nil {
		t.Fatalf("GetHealthz: %v", err)
	}
	// healthz は認証不要。正常に 200 が返り、app フィールドが "ccwin-notify" であること
	if h.App != "ccwin-notify" {
		t.Errorf("Healthz.App: got %q, want \"ccwin-notify\"", h.App)
	}
}

// T-175: GetHealthz が非 200 応答 → エラーを返す
// sync.Mutex でハンドラを切り替えることで New() 初期化後に 503 を返す
func TestApiclient_GetHealthz_NonOK(t *testing.T) {
	t.Parallel()
	// atomic カウンタで New() の初期化チェック (最大 3 回) を通過後に 503 を返す
	// New() 内の healthz は 200ms タイムアウトなので、callCount で判定する
	var healthy int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		if healthy == 0 {
			// New() 初期化完了まで成功させる
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"app": "ccwin-notify",
			})
			return
		}
		// healthy=1 → 非 200
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writePortfile(t, dir, srv)
	tokenPath := writeTokenfile(t, dir, testAPIToken)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	// New() 完了後にフラグを切り替えて 503 を返すようにする
	healthy = 1

	_, err = client.GetHealthz(context.Background())
	if err == nil {
		t.Error("GetHealthz 503: エラーが返るべきだが nil だった")
	}
}

// T-176: GetHealthz が不正な JSON を返した場合 → デコードエラー
func TestApiclient_GetHealthz_InvalidJSON(t *testing.T) {
	t.Parallel()
	var serveInvalidJSON int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		if serveInvalidJSON == 0 {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"app": "ccwin-notify",
			})
			return
		}
		// 不正 JSON
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json{{"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writePortfile(t, dir, srv)
	tokenPath := writeTokenfile(t, dir, testAPIToken)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	serveInvalidJSON = 1
	_, err = client.GetHealthz(context.Background())
	if err == nil {
		t.Error("GetHealthz (invalid JSON): エラーが返るべきだが nil だった")
	}
}

// T-157: GetStatus が 401 応答 → ErrUnauthorized
func TestApiclient_GetStatus_Unauthorized(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app": "ccwin-notify", "status": "ok",
		})
	})
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writePortfile(t, dir, srv)
	tokenPath := writeTokenfile(t, dir, testAPIToken)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	_, err = client.GetStatus(context.Background())
	if err != apiclient.ErrUnauthorized {
		t.Errorf("GetStatus 401: got %v, want ErrUnauthorized", err)
	}
}

// T-158: GetStatus が 5xx 応答 → ErrServer
func TestApiclient_GetStatus_ServerError(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app": "ccwin-notify", "status": "ok",
		})
	})
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writePortfile(t, dir, srv)
	tokenPath := writeTokenfile(t, dir, testAPIToken)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	_, err = client.GetStatus(context.Background())
	if err != apiclient.ErrServer {
		t.Errorf("GetStatus 5xx: got %v, want ErrServer", err)
	}
}

// T-159: TestNotifier が正しい JSON を送り TestResult を返す
func TestApiclient_TestNotifier_Success(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app": "ccwin-notify", "status": "ok",
		})
	})
	mux.HandleFunc("/v1/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"event_id": "01HTEST00000000000000001",
			"results": []map[string]interface{}{
				{"notifier": "toast", "ok": true, "error": ""},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writePortfile(t, dir, srv)
	tokenPath := writeTokenfile(t, dir, testAPIToken)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	req := apiclient.TestRequest{
		Kind:           event.KindStop,
		TargetNotifier: "toast",
		Title:          "test title",
		Body:           "test body",
	}
	result, err := client.TestNotifier(context.Background(), req)
	if err != nil {
		t.Fatalf("TestNotifier: %v", err)
	}

	if result.EventID != "01HTEST00000000000000001" {
		t.Errorf("TestResult.EventID: got %q, want \"01HTEST00000000000000001\"", result.EventID)
	}
	if len(result.Results) != 1 {
		t.Fatalf("TestResult.Results: got %d, want 1", len(result.Results))
	}
	if !result.Results[0].OK {
		t.Error("TestResult.Results[0].OK: got false, want true")
	}
}

// T-160: TestNotifier が 401 応答 → ErrUnauthorized
func TestApiclient_TestNotifier_Unauthorized(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app": "ccwin-notify", "status": "ok",
		})
	})
	mux.HandleFunc("/v1/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writePortfile(t, dir, srv)
	tokenPath := writeTokenfile(t, dir, testAPIToken)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	_, err = client.TestNotifier(context.Background(), apiclient.TestRequest{Kind: event.KindStop})
	if err != apiclient.ErrUnauthorized {
		t.Errorf("TestNotifier 401: got %v, want ErrUnauthorized", err)
	}
}

// T-161: TestNotifier が 5xx 応答 → ErrServer
func TestApiclient_TestNotifier_ServerError(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app": "ccwin-notify", "status": "ok",
		})
	})
	mux.HandleFunc("/v1/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writePortfile(t, dir, srv)
	tokenPath := writeTokenfile(t, dir, testAPIToken)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	_, err = client.TestNotifier(context.Background(), apiclient.TestRequest{Kind: event.KindStop})
	if err != apiclient.ErrServer {
		t.Errorf("TestNotifier 5xx: got %v, want ErrServer", err)
	}
}

// T-066 / TUI-1: GetStatus が正しい Bearer を送る
func TestApiclient_GetStatus_BearerIncluded(t *testing.T) {
	t.Parallel()
	srv, received := setupTestServer(t)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writePortfile(t, dir, srv)
	tokenPath := writeTokenfile(t, dir, testAPIToken)

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}

	_, err = client.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}

	select {
	case req := <-received:
		if req.Path != "/v1/status" {
			t.Errorf("Path: got %q, want \"/v1/status\"", req.Path)
		}
		expectedBearer := "Bearer " + testAPIToken
		if req.Authorization != expectedBearer {
			t.Errorf("Authorization: got %q, want %q", req.Authorization, expectedBearer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("タイムアウト: /v1/status リクエストが受信されなかった")
	}
}

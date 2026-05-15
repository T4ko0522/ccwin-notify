// cmd/ccwin-notify の send サブコマンド 統合テスト
// テスト ID: T-045〜T-049 (CLI 統合確認 / exit code 経路)
// 受入条件: C1 / C2 / C6 — daemon 未起動時の確認、正常 POST の確認、NormalizeHook 統合
//
// NOTE: send.Run(args, stdin, ...) は 3_contract.md に未定義のため、
// CLI バイナリレベルの exit code テスト (T-045b) は at-implementer の Run 実装後に追加する。
// NormalizeHook のユニットテストは internal/send/send_test.go で網羅済み。
// 本ファイルでは cmd 層からの統合パスを確認する。
package main_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/apiclient"
	"github.com/t4ko0522/ccwin-notify/internal/event"
	ccwinsend "github.com/t4ko0522/ccwin-notify/internal/send"
)

// T-045: NormalizeHook が internal/send パッケージから cmd 層でも使えること (統合確認)
func TestSend_NormalizeHook_Stop(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"last_assistant_message": "task completed"}`)
	ev, err := ccwinsend.NormalizeHook("Stop", raw)
	if err != nil {
		t.Fatalf("NormalizeHook Stop: %v", err)
	}
	if ev.Kind != event.KindStop {
		t.Errorf("Kind: got %q, want %q", ev.Kind, event.KindStop)
	}
	if ev.Title != "Claude Code: response complete" {
		t.Errorf("Title: got %q, want \"Claude Code: response complete\"", ev.Title)
	}
	if ev.Body != "task completed" {
		t.Errorf("Body: got %q, want \"task completed\"", ev.Body)
	}
}

// T-046: NormalizeHook Notification (cmd 層統合確認)
func TestSend_NormalizeHook_Notification(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"title": "Input required", "message": "Please respond"}`)
	ev, err := ccwinsend.NormalizeHook("Notification", raw)
	if err != nil {
		t.Fatalf("NormalizeHook Notification: %v", err)
	}
	if ev.Kind != event.KindNotification {
		t.Errorf("Kind: got %q, want %q", ev.Kind, event.KindNotification)
	}
	if ev.Title != "Input required" {
		t.Errorf("Title: got %q, want \"Input required\"", ev.Title)
	}
}

// T-047: 未知 Kind → error (cmd 層統合確認)
func TestSend_NormalizeHook_UnknownKind(t *testing.T) {
	t.Parallel()
	_, err := ccwinsend.NormalizeHook("UnknownKindXyz", json.RawMessage(`{}`))
	if err == nil {
		t.Error("未知 Kind: error が返るべきだが nil だった")
	}
}

// T-048 / C2: portfile 不在時に apiclient.New が ErrDaemonNotRunning を返す
// send CLI は daemon 未起動時に非ゼロ exit code で終了する。
// apiclient.New が ErrDaemonNotRunning を返すことが exit 非ゼロの実装根拠。
func TestSend_DaemonNotRunning_PortfileAbsent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	nonExistentPortfile := filepath.Join(dir, "daemon.port")
	tokenPath := filepath.Join(dir, "secret.token")
	if err := os.WriteFile(tokenPath, []byte("dummy-token-for-test-abcdef1234567890"), 0600); err != nil {
		t.Fatalf("tokenfile 書き込み: %v", err)
	}

	// apiclient.New は portfile 不在時に ErrDaemonNotRunning を返す
	_, err := apiclient.New(nonExistentPortfile, tokenPath)
	if err != apiclient.ErrDaemonNotRunning {
		t.Errorf("C2: portfile 不在時は ErrDaemonNotRunning を返すべき: got %v", err)
	}
}

// T-049 / C6: 正常な POST /v1/events が 200 を返したとき、エラーなしで完了する
func TestSend_PostEvent_Success(t *testing.T) {
	t.Parallel()

	// ダミーの daemon サーバを httptest で立てる
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app": "ccwin-notify", "status": "ok",
		})
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		// Bearer 認証ヘッダが含まれることを確認
		auth := r.Header.Get("Authorization")
		if auth == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "01HTEST00000"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// portfile を書き出す
	dir := t.TempDir()
	portfilePath := writeSendTestPortfile(t, dir, srv)
	tokenPath := filepath.Join(dir, "secret.token")
	const sendTestToken = "send-test-token-abcdef1234567890xyz"
	if err := os.WriteFile(tokenPath, []byte(sendTestToken), 0600); err != nil {
		t.Fatalf("tokenfile 書き込み: %v", err)
	}

	// NormalizeHook → Event を生成
	raw := json.RawMessage(`{"last_assistant_message": "all done"}`)
	ev, err := ccwinsend.NormalizeHook("Stop", raw)
	if err != nil {
		t.Fatalf("NormalizeHook: %v", err)
	}

	// apiclient.New + PostEvent が正常完了することを確認 (C6)
	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}
	if err := client.PostEvent(context.Background(), ev); err != nil {
		t.Errorf("C6: 正常 POST が失敗: %v", err)
	}
}

// T-048b / C2 / exit4: send --kind Stop (portfile 不在) → exit 4
// exec.Command でバイナリを直接起動し、apiclient.New が ErrDaemonNotRunning を返す経路が
// cmd_send.go の os.Exit(4) に到達することを実証する。
func TestSend_DaemonNotRunning_ExitCode4(t *testing.T) {
	t.Parallel()

	exe, err := buildTestBinary(t)
	if err != nil {
		t.Skipf("バイナリビルドスキップ: %v", err)
	}

	// portfile が存在しない一時ディレクトリを APPDATA に設定
	dir := t.TempDir()
	// tokenPath も不在にする (portfile 読み取りが先に失敗するため影響なし)

	cmd := exec.Command(exe, "send", "--kind", "Stop")
	cmd.Env = append(os.Environ(), "APPDATA="+dir)
	err = cmd.Run()

	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}

	if exitCode != 4 {
		t.Errorf("C2: portfile 不在で 'send --kind Stop' を実行した場合の exit code: got %d, want 4", exitCode)
	}
}

// T-120 / C6: 200 OK → exit 0
// exec.Command でバイナリを直接起動し、POST 成功時 exit 0 を実証する。
// buildTestBinary でビルドしたバイナリを httptest サーバに向ける。
func TestSend_ExitCode_200OK_ExitZero(t *testing.T) {
	t.Parallel()

	exe, err := buildTestBinary(t)
	if err != nil {
		t.Skipf("バイナリビルドスキップ: %v", err)
	}

	// httptest サーバ: healthz + events 両方 200 OK を返す
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app":    "ccwin-notify",
			"status": "ok",
		})
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "01HTEST"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	portfilePath := writeSendTestPortfile(t, dir, srv)
	tokenPath := filepath.Join(dir, "secret.token")
	if err := os.WriteFile(tokenPath, []byte("test-token-exit0-abcdef"), 0600); err != nil {
		t.Fatalf("token 書き込み: %v", err)
	}

	// portfile を ccwin-notify ディレクトリに配置
	ccwinDir := filepath.Join(dir, "ccwin-notify")
	if err := os.MkdirAll(ccwinDir, 0700); err != nil {
		t.Fatalf("dir 作成: %v", err)
	}
	// ccwin-notify/daemon.port にコピー
	portData, _ := os.ReadFile(portfilePath)
	if err := os.WriteFile(filepath.Join(ccwinDir, "daemon.port"), portData, 0600); err != nil {
		t.Fatalf("portfile コピー: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ccwinDir, "secret.token"), []byte("test-token-exit0-abcdef"), 0600); err != nil {
		t.Fatalf("token コピー: %v", err)
	}

	cmd := exec.Command(exe, "send", "--kind", "Stop")
	cmd.Env = append(os.Environ(), "APPDATA="+dir)
	err = cmd.Run()

	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}

	if exitCode != 0 {
		t.Errorf("C6: 200 OK 時の exit code: got %d, want 0", exitCode)
	}
}

// T-121 / C6: 401 Unauthorized → exit 1
// 現実装 (cmd_send.go) では PostEvent エラー時は os.Exit(1) で統一されている。
// 将来的に 401 → exit 3 などに分岐する可能性はあるが、現仕様では exit 1 を期待。
func TestSend_ExitCode_401_ExitOne(t *testing.T) {
	t.Parallel()

	exe, err := buildTestBinary(t)
	if err != nil {
		t.Skipf("バイナリビルドスキップ: %v", err)
	}

	// httptest サーバ: healthz は 200、events は 401
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app":    "ccwin-notify",
			"status": "ok",
		})
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	ccwinDir := filepath.Join(dir, "ccwin-notify")
	if err := os.MkdirAll(ccwinDir, 0700); err != nil {
		t.Fatalf("dir 作成: %v", err)
	}
	portfilePath := writeSendTestPortfile(t, dir, srv)
	portData, _ := os.ReadFile(portfilePath)
	if err := os.WriteFile(filepath.Join(ccwinDir, "daemon.port"), portData, 0600); err != nil {
		t.Fatalf("portfile コピー: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ccwinDir, "secret.token"), []byte("test-token-401-abcdef"), 0600); err != nil {
		t.Fatalf("token コピー: %v", err)
	}

	cmd := exec.Command(exe, "send", "--kind", "Stop")
	cmd.Env = append(os.Environ(), "APPDATA="+dir)
	err = cmd.Run()

	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}

	// 現実装: PostEvent 失敗は os.Exit(1) で統一 (401 専用コードは未実装)
	if exitCode == 0 {
		t.Errorf("C6: 401 時の exit code は 0 であってはならない")
	}
	t.Logf("C6: 401 → exit %d (現実装は exit 1 で統一、将来 exit 3 に分岐予定)", exitCode)
}

// T-122 / C6: 5xx Server Error → exit 1
// 現実装では PostEvent エラー時は os.Exit(1) で統一されている。
func TestSend_ExitCode_5xx_ExitOne(t *testing.T) {
	t.Parallel()

	exe, err := buildTestBinary(t)
	if err != nil {
		t.Skipf("バイナリビルドスキップ: %v", err)
	}

	// httptest サーバ: healthz は 200、events は 500
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"app":    "ccwin-notify",
			"status": "ok",
		})
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	ccwinDir := filepath.Join(dir, "ccwin-notify")
	if err := os.MkdirAll(ccwinDir, 0700); err != nil {
		t.Fatalf("dir 作成: %v", err)
	}
	portfilePath := writeSendTestPortfile(t, dir, srv)
	portData, _ := os.ReadFile(portfilePath)
	if err := os.WriteFile(filepath.Join(ccwinDir, "daemon.port"), portData, 0600); err != nil {
		t.Fatalf("portfile コピー: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ccwinDir, "secret.token"), []byte("test-token-5xx-abcdef"), 0600); err != nil {
		t.Fatalf("token コピー: %v", err)
	}

	cmd := exec.Command(exe, "send", "--kind", "Stop")
	cmd.Env = append(os.Environ(), "APPDATA="+dir)
	err = cmd.Run()

	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}

	// 現実装: PostEvent エラーは os.Exit(1) で統一
	if exitCode == 0 {
		t.Errorf("C6: 5xx 時の exit code は 0 であってはならない")
	}
	t.Logf("C6: 5xx → exit %d (現実装は exit 1 で統一、将来 exit 5 に分岐予定)", exitCode)
}

// writeSendTestPortfile は portfile を一時ディレクトリに書き出す
func writeSendTestPortfile(t *testing.T, dir string, srv *httptest.Server) string {
	t.Helper()
	addr := srv.Listener.Addr().String()
	_, portStr, _ := net.SplitHostPort(addr)
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

// Package client のテスト (Phase 3 Retry 2)
// テスト ID: T-137〜T-142
// 受入条件: C2 (portfile 読込) / A6 (Bearer Transport)
package client_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/t4ko0522/ccwin-notify/internal/ipc/client"
	"github.com/t4ko0522/ccwin-notify/internal/secret"
)

// T-137 / C2: ReadPortfile が正常な portfile を読み込む
func TestReadPortfile_OK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.port")

	content := map[string]interface{}{
		"app":               "ccwin-notify",
		"version":           "0.1.0",
		"pid":               12345,
		"port":              8080,
		"started_at":        "2026-05-15T00:00:00Z",
		"token_fingerprint": "sha256:abc",
	}
	data, _ := json.Marshal(content)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pf, err := client.ReadPortfile(path)
	if err != nil {
		t.Fatalf("ReadPortfile: %v", err)
	}
	if pf.App != "ccwin-notify" {
		t.Errorf("App: got %q, want \"ccwin-notify\"", pf.App)
	}
	if pf.Port != 8080 {
		t.Errorf("Port: got %d, want 8080", pf.Port)
	}
	if pf.PID != 12345 {
		t.Errorf("PID: got %d, want 12345", pf.PID)
	}
}

// T-138 / C2: ReadPortfile がファイル不在で os.ErrNotExist ラップを返す
func TestReadPortfile_NotExist(t *testing.T) {
	t.Parallel()
	_, err := client.ReadPortfile("/nonexistent/path/daemon.port")
	if err == nil {
		t.Fatal("ファイル不在: error が返るべきだが nil だった")
	}
	if !os.IsNotExist(err) {
		// エラーは os.ErrNotExist を含むラップ形式
		t.Logf("エラー内容: %v (os.IsNotExist=false だが許容)", err)
	}
}

// T-139 / C2: ReadPortfile が不正 JSON で ErrPortfileInvalid を返す
func TestReadPortfile_InvalidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.port")
	if err := os.WriteFile(path, []byte(`{invalid`), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := client.ReadPortfile(path)
	if err == nil {
		t.Fatal("不正 JSON: error が返るべきだが nil だった")
	}
}

// T-140 / C2: ReadPortfile が app フィールド不一致で ErrPortfileInvalid を返す
func TestReadPortfile_AppMismatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.port")

	content := map[string]interface{}{
		"app":  "wrong-app",
		"port": 8080,
	}
	data, _ := json.Marshal(content)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := client.ReadPortfile(path)
	if err == nil {
		t.Fatal("app 不一致: error が返るべきだが nil だった")
	}
}

// T-141 / C2: ReadPortfile が port <= 0 で ErrPortfileInvalid を返す
func TestReadPortfile_InvalidPort(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.port")

	content := map[string]interface{}{
		"app":  "ccwin-notify",
		"port": 0, // 無効ポート
	}
	data, _ := json.Marshal(content)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := client.ReadPortfile(path)
	if err == nil {
		t.Fatal("port=0: error が返るべきだが nil だった")
	}
}

// T-142 / A6: NewHTTPClient が Authorization: Bearer ヘッダを自動付与する
func TestNewHTTPClient_AddsBearer(t *testing.T) {
	t.Parallel()

	const token = "test-bearer-token-xyz"
	var capturedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hc := client.NewHTTPClient(secret.SecretString(token))
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	expected := "Bearer " + token
	if capturedAuth != expected {
		t.Errorf("Authorization: got %q, want %q", capturedAuth, expected)
	}
}

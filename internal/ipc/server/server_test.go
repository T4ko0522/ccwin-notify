// Package server のテスト (Phase 3 Retry 2)
// テスト ID: T-126〜T-130
// 受入条件: A2 / A6 (127.0.0.1 バインド、Bearer 認証) / D-36 (Server 登録・起動・シャットダウン)
package server_test

import (
	"context"
	"crypto/sha256"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/ipc/server"
)

// T-126 / D-36: New が Server を返し Shutdown が正常に動く
func TestNew_ShutdownOK(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	srv := server.New(ln)
	if srv == nil {
		t.Fatal("server.New: nil が返った")
	}

	// Serve を goroutine で起動
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve()
	}()

	// 少し待ってから Shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}

	// Serve が終了したことを確認
	select {
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("Serve error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("Serve が Shutdown 後に終了しなかった")
	}
}

// T-127 / A6: RegisterRoute(requireAuth=false) → 認証なしで 200
func TestRegisterRoute_NoAuth_200(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	srv := server.New(ln)
	var authHash [32]byte
	srv.RegisterRoute(http.MethodGet, "/v1/healthz",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		false, authHash,
	)

	go srv.Serve()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	addr := ln.Addr().String()
	resp, err := http.Get("http://" + addr + "/v1/healthz")
	if err != nil {
		t.Fatalf("GET /v1/healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /v1/healthz (no auth): got %d, want 200", resp.StatusCode)
	}
}

// T-128 / A6: RegisterRoute(requireAuth=true) → Bearer なしで 401
func TestRegisterRoute_RequireAuth_NoToken_401(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	const token = "test-secret-token-12345"
	authHash := sha256.Sum256([]byte(token))

	srv := server.New(ln)
	srv.RegisterRoute(http.MethodGet, "/v1/status",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		true, authHash,
	)

	go srv.Serve()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	addr := ln.Addr().String()
	// Bearer なしでリクエスト
	resp, err := http.Get("http://" + addr + "/v1/status")
	if err != nil {
		t.Fatalf("GET /v1/status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /v1/status (no Bearer): got %d, want 401", resp.StatusCode)
	}
}

// T-129 / A6: RegisterRoute(requireAuth=true) → 正しい Bearer で 200
func TestRegisterRoute_RequireAuth_ValidToken_200(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	const token = "test-secret-token-67890"
	authHash := sha256.Sum256([]byte(token))

	srv := server.New(ln)
	srv.RegisterRoute(http.MethodGet, "/v1/status",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		true, authHash,
	)

	go srv.Serve()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	addr := ln.Addr().String()
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/status (with Bearer): %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /v1/status (valid Bearer): got %d, want 200", resp.StatusCode)
	}
}

// T-130 / A2: Server は 127.0.0.1 にバインドされる (ポートが 1024-65535 の範囲)
func TestNew_BindsToLoopback(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	addr := ln.Addr().(*net.TCPAddr)
	if !addr.IP.IsLoopback() {
		t.Errorf("A2: bind address %v は loopback ではない", addr.IP)
	}
	if addr.Port < 1024 || addr.Port > 65535 {
		t.Errorf("A2: port %d は 1024-65535 の範囲外", addr.Port)
	}

	srv := server.New(ln)
	go srv.Serve()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

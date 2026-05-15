// Package server は ccwin-notify の HTTP サーバを提供する (D-36)。
// Daemon が所有し RegisterRoute で各ハンドラを登録する。
package server

import (
	"context"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/netutil"

	"github.com/t4ko0522/ccwin-notify/internal/ipc/middleware"
)

// maxConcurrentConns は HTTP server が同時に受け入れる接続数の上限 (M-06)。
// 超過時、Accept() は前のコネクションが Close されるまでブロックする。
const maxConcurrentConns = 16

// Server は HTTP server の本体 (D-36)。
type Server struct {
	ln  net.Listener
	mux *http.ServeMux
	srv *http.Server
}

// New は net.Listener を受け取り Server を構築する。
// WriteTimeout=0 (SSE 対応)、ReadHeaderTimeout=5s、ReadTimeout=10s、IdleTimeout=120s、MaxHeaderBytes=64KiB (D-37)。
// Listener は netutil.LimitListener で同時接続数を maxConcurrentConns に制限する (M-06)。
func New(ln net.Listener) *Server {
	ln = netutil.LimitListener(ln, maxConcurrentConns)
	mux := http.NewServeMux()
	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      0, // SSE 長寿命接続のため無制限 (D-37)
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16, // 64 KiB
		Handler:           mux,
	}
	return &Server{ln: ln, mux: mux, srv: srv}
}

// RegisterRoute はルートを登録する。
// requireAuth=true のとき auth ハッシュの Bearer ミドルウェアが適用される。
// auth は [32]byte の SHA-256 ダイジェスト (D-39)。requireAuth=false のとき auth は無視。
// handler は http.Handler または http.HandlerFunc を渡せる。
func (s *Server) RegisterRoute(method, path string, handler http.Handler, requireAuth bool, auth [32]byte) {
	var h http.Handler
	if requireAuth {
		h = middleware.RequireBearer(auth)(handler)
	} else {
		h = handler
	}
	s.mux.Handle(method+" "+path, h)
}

// Serve は HTTP サーバを起動する (ブロッキング)。http.ErrServerClosed で正常終了。
func (s *Server) Serve() error {
	return s.srv.Serve(s.ln)
}

// Shutdown はグレースフルシャットダウンを行う。
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

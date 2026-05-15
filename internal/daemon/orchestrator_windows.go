//go:build windows

package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"

	"github.com/t4ko0522/ccwin-notify/internal/auth"
	"github.com/t4ko0522/ccwin-notify/internal/config"
	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/ipc/server"
	"github.com/t4ko0522/ccwin-notify/internal/ipc/sse"
	"github.com/t4ko0522/ccwin-notify/internal/notifier"
	"github.com/t4ko0522/ccwin-notify/internal/notifier/sound"
	"github.com/t4ko0522/ccwin-notify/internal/notifier/toast"
	"github.com/t4ko0522/ccwin-notify/internal/notifier/webhook"
	"github.com/t4ko0522/ccwin-notify/internal/source/hooks"
)

func pidSelf() int {
	return os.Getpid()
}

// Run は §5.1 起動シーケンス + §5.2 停止シーケンスを実行する。
// daemon サブコマンドから呼ばれる。
func Run(ctx context.Context, cfg *config.Config) error {
	logger := slog.Default()

	// step 2: ディレクトリ ACL
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		return fmt.Errorf("daemon: APPDATA environment variable not set")
	}
	ccwinDir := filepath.Join(appdata, "ccwin-notify")
	if err := auth.EnsureDirACL(ccwinDir); err != nil {
		return fmt.Errorf("daemon: EnsureDirACL: %w (exit 4)", err)
	}

	// step 4: Windows Mutex (多重起動防止 / D-22 / C5)
	// SID 取得
	tok, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return fmt.Errorf("daemon: OpenCurrentProcessToken: %w", err)
	}
	tu, err := tok.GetTokenUser()
	tok.Close()
	if err != nil {
		return fmt.Errorf("daemon: GetTokenUser: %w", err)
	}
	sidStr := tu.User.Sid.String()
	mutexName := "Local\\ccwin-notify-daemon-" + sidStr
	mutexNamePtr, _ := windows.UTF16PtrFromString(mutexName)
	mutexHandle, err := windows.CreateMutex(nil, false, mutexNamePtr)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return fmt.Errorf("daemon: CreateMutex: %w", err)
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(mutexHandle)
		return fmt.Errorf("daemon: already running (exit 3)")
	}
	defer func() {
		windows.ReleaseMutex(mutexHandle)
		windows.CloseHandle(mutexHandle)
	}()

	// step 5: auth.LoadOrCreate
	tokenPath := filepath.Join(ccwinDir, "secret.token")
	plainToken, err := auth.LoadOrCreate(ctx, tokenPath)
	if err != nil {
		return fmt.Errorf("daemon: auth.LoadOrCreate: %w (exit 4)", err)
	}
	// SHA-256 化してメモリに保持
	tokenHash := sha256.Sum256([]byte(plainToken.Reveal()))

	// step 6-7: rootCtx + acceptCtx
	rootCtx, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignal()
	acceptCtx, cancelAccept := context.WithCancel(rootCtx)
	defer cancelAccept()

	// step 8: Bus
	bus := event.NewBus(cfg.Queue.Capacity, cfg.Queue.Policy)

	// step 9: Notifiers
	var notifiers []notifier.Notifier
	if cfg.Notifiers.Toast.Enabled {
		// 環境変数 CCWIN_NOTIFY_USE_FAKE_TOASTER=1 のとき FakeToaster を使う (CI / テスト用)。
		// それ以外は NewDefaultToaster() で build tag に応じた実装を使う。
		var toaster toast.Toaster
		if os.Getenv("CCWIN_NOTIFY_USE_FAKE_TOASTER") == "1" {
			toaster = toast.NewFakeToaster()
		} else {
			toaster = toast.NewDefaultToaster()
		}
		n := toast.NewWithKindMask(toaster, cfg.Notifiers.Toast.KindMask)
		notifiers = append(notifiers, n)
	}
	if cfg.Notifiers.Sound.Enabled {
		n := sound.New(sound.Config{
			WavPath:  cfg.Notifiers.Sound.WavPath,
			KindMask: cfg.Notifiers.Sound.KindMask,
		})
		notifiers = append(notifiers, n)
	}
	if cfg.Notifiers.Webhook.Discord.Enabled {
		n := webhook.NewDiscord(webhook.Config{
			URL:        cfg.Notifiers.Webhook.Discord.URL,
			Timeout:    cfg.Notifiers.Webhook.Discord.Timeout,
			MaxRetries: cfg.Notifiers.Webhook.Discord.MaxRetries,
			KindMask:   cfg.Notifiers.Webhook.Discord.KindMask,
		})
		notifiers = append(notifiers, n)
	}
	if cfg.Notifiers.Webhook.Slack.Enabled {
		n := webhook.NewSlack(webhook.Config{
			URL:        cfg.Notifiers.Webhook.Slack.URL,
			Timeout:    cfg.Notifiers.Webhook.Slack.Timeout,
			MaxRetries: cfg.Notifiers.Webhook.Slack.MaxRetries,
			KindMask:   cfg.Notifiers.Webhook.Slack.KindMask,
		})
		notifiers = append(notifiers, n)
	}

	// step 11: SSE Hub
	sseHub := sse.NewHub()
	defer sseHub.Close()

	// step 12: Dispatcher
	dispCfg := DispatcherConfig{
		MaxConcurrentPerNotifier: cfg.Dispatcher.MaxConcurrentPerNotifier,
		NotifierTimeout:          cfg.Dispatcher.NotifierTimeout,
	}
	dispatcher := NewDispatcher(acceptCtx, bus, notifiers, sseHub, dispCfg, logger)
	go dispatcher.Run()

	// step 13: HTTP server + route 登録
	ln, err := net.Listen("tcp", cfg.IPC.BindAddress+":0")
	if err != nil {
		return fmt.Errorf("daemon: listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	httpServer := server.New(ln)
	startedAt := time.Now()
	portFilePath := filepath.Join(ccwinDir, "daemon.port")

	// M-08: hooks ソースが disabled のときは /v1/events と /v1/events/stream を登録しない。
	// 認証突破経由でも events を投げ込めないことを保証する。
	if cfg.Sources.Hooks.Enabled {
		httpServer.RegisterRoute("POST", "/v1/events",
			hooks.HandleEvents(bus, acceptCtx, tokenHash),
			false, [32]byte{})
		httpServer.RegisterRoute("GET", "/v1/events/stream",
			hooks.HandleStream(sseHub, acceptCtx, tokenHash),
			false, [32]byte{})
		logger.Info("hooks source enabled — /v1/events and /v1/events/stream registered")
	} else {
		logger.Info("hooks source disabled — /v1/events route NOT registered")
	}
	httpServer.RegisterRoute("GET", "/v1/status",
		HandleStatus(dispatcher, bus, sseHub, cfg, plainToken, startedAt, portFilePath),
		true, tokenHash)
	httpServer.RegisterRoute("POST", "/v1/test",
		HandleTest(dispatcher, notifiers, cfg.Notifiers),
		true, tokenHash)
	httpServer.RegisterRoute("GET", "/v1/healthz",
		HandleHealthz(startedAt, version),
		false, [32]byte{})

	// step 13.5: HTTP server goroutine
	go func() {
		if serveErr := httpServer.Serve(); serveErr != nil && serveErr.Error() != "http: Server closed" {
			logger.Error("http server failed", "err", serveErr)
			os.Exit(1)
		}
	}()

	// step 14: portfile atomic write
	if err := writePortfile(portFilePath, port, tokenHash); err != nil {
		return fmt.Errorf("daemon: portfile: %w", err)
	}
	defer os.Remove(portFilePath)

	// step 16: メインループ — rootCtx.Done() を待つ
	<-rootCtx.Done()

	// §5.2 停止シーケンス
	cancelAccept()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()

	_ = httpServer.Shutdown(shutdownCtx)
	_ = bus.Close(shutdownCtx)

	if err := dispatcher.Close(shutdownCtx); err != nil {
		logger.Warn("dispatcher close timeout", "err", err)
	}

	return nil
}

// writePortfile は portfile を atomic write で書き出す (§4.1)。
func writePortfile(path string, port int, tokenHash [32]byte) error {
	pid := os.Getpid()
	fingerprint := fmt.Sprintf("sha256:%x", tokenHash)
	data, err := json.Marshal(map[string]interface{}{
		"app":               "ccwin-notify",
		"version":           version,
		"pid":               pid,
		"port":              port,
		"started_at":        time.Now().Format(time.RFC3339),
		"token_fingerprint": fingerprint,
	})
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, fmt.Sprintf("daemon.port.tmp.%d", pid))
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

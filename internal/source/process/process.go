// Package process は対象プロセスを定期ポーリングし、PID 消失を Stop event として
// Bus に流すソース実装 (受入条件 A3 / Hooks 経路のフォールバック)。
//
// Hooks 経由の Stop / Notification が稀に発火しない場合でも、claude プロセスが
// 終了したことを検知して通知できるよう冗長化する。
package process

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	gopsproc "github.com/shirou/gopsutil/v4/process"

	"github.com/t4ko0522/ccwin-notify/internal/event"
)

// EventPublisher は event.Bus.Publish の抽象 (Bus 直接依存を避けてテスト容易にする)。
type EventPublisher interface {
	Publish(ctx context.Context, e event.Event) error
}

// ProcessLister は対象プロセス名に一致する PID 一覧を返す。
// テストで gopsutil を差し替えるためのフック。
type ProcessLister func(name string) ([]int32, error)

// Config は Source の挙動を制御する設定。internal/config.ProcessConfig からは
// orchestrator が変換して渡す (循環依存を避けるため独立)。
type Config struct {
	Interval    time.Duration
	ProcessName string
	// Lister が nil のときは defaultLister (gopsutil) を使う
	Lister ProcessLister
}

// Source は claude プロセスを定期ポーリングし、PID 消失を Stop event として
// publisher に流す。
type Source struct {
	publisher EventPublisher
	cfg       Config
	logger    *slog.Logger

	mu        sync.Mutex
	knownPIDs map[int32]struct{}
}

// New は Source を生成する。
// cfg.Interval <= 0 は 2 秒、cfg.ProcessName が空は "claude.exe" を既定値とする。
// logger が nil のときは slog.Default()。
func New(publisher EventPublisher, cfg Config, logger *slog.Logger) *Source {
	if cfg.Interval <= 0 {
		cfg.Interval = 2 * time.Second
	}
	if cfg.ProcessName == "" {
		cfg.ProcessName = "claude.exe"
	}
	if cfg.Lister == nil {
		cfg.Lister = defaultLister
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Source{
		publisher: publisher,
		cfg:       cfg,
		logger:    logger,
		knownPIDs: map[int32]struct{}{},
	}
}

// Run は ctx が cancel されるまでブロックし、Interval ごとにポーリングする。
// 初回スキャンでは「消失」イベントを発火しない (既知集合の初期化のみ)。
// 2 回目以降のスキャンで「前回居たが今回居ない PID」を Stop event として Publish する。
func (s *Source) Run(ctx context.Context) error {
	// 初回スキャン: 既知集合を初期化するだけ。消失イベントは発火しない。
	s.scan(ctx, true)

	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.scan(ctx, false)
		}
	}
}

// scan は 1 回のポーリングを実行する。
// initial=true のときは消失イベントを発火せず既知集合の初期化のみ行う。
func (s *Source) scan(ctx context.Context, initial bool) {
	pids, err := s.cfg.Lister(s.cfg.ProcessName)
	if err != nil {
		s.logger.Warn("process source: list failed", "err", err.Error(),
			"process_name", s.cfg.ProcessName)
		return
	}

	now := map[int32]struct{}{}
	for _, pid := range pids {
		now[pid] = struct{}{}
	}

	s.mu.Lock()
	prev := s.knownPIDs
	s.knownPIDs = now
	s.mu.Unlock()

	if initial {
		s.logger.Info("process source: initial scan", "found", len(now),
			"process_name", s.cfg.ProcessName)
		return
	}

	// 前回居たが今回居ない PID = 消失
	for pid := range prev {
		if _, alive := now[pid]; alive {
			continue
		}
		ev := event.Event{
			ID:        event.NewID(),
			Kind:      event.KindStop,
			Title:     "Process exited: " + s.cfg.ProcessName,
			Body:      "PID " + itoa(pid) + " is no longer running",
			Source:    "process",
			Timestamp: time.Now(),
		}
		if err := s.publisher.Publish(ctx, ev); err != nil {
			s.logger.Warn("process source: publish failed",
				"err", err.Error(), "pid", pid)
		}
	}
}

// itoa は int32 → 10 進文字列。strconv.Itoa(int(...)) を避け fmt にも依存しない。
func itoa(n int32) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// defaultLister は gopsutil 経由で対象プロセス名 (大文字小文字無視) の PID 一覧を返す。
func defaultLister(name string) ([]int32, error) {
	procs, err := gopsproc.Processes()
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(name)
	var pids []int32
	for _, p := range procs {
		n, err := p.Name()
		if err != nil {
			continue
		}
		if strings.EqualFold(n, target) {
			pids = append(pids, p.Pid)
		}
	}
	return pids, nil
}

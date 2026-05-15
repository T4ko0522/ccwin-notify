package process_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/source/process"
)

// fakePublisher は EventPublisher のテスト実装。Publish 呼び出しを記録する。
type fakePublisher struct {
	mu       sync.Mutex
	received []event.Event
}

func (f *fakePublisher) Publish(_ context.Context, ev event.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.received = append(f.received, ev)
	return nil
}

func (f *fakePublisher) Events() []event.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]event.Event, len(f.received))
	copy(out, f.received)
	return out
}

// scriptedLister は呼び出しごとに異なる PID 集合を返すテスト用 Lister。
// channel に書き込まれた slice を順番に返す。
type scriptedLister struct {
	scripts [][]int32
	calls   int
	mu      sync.Mutex
	called  chan struct{}
}

func (s *scriptedLister) list(_ string) ([]int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.calls
	if idx >= len(s.scripts) {
		// scripts を使い切ったら最後の集合を返し続ける (差分を発生させない)
		idx = len(s.scripts) - 1
	}
	out := s.scripts[idx]
	s.calls++
	select {
	case s.called <- struct{}{}:
	default:
	}
	return out, nil
}

// A3: 初回スキャンでは消失イベントを発火しない (既知集合の初期化のみ)
func TestSource_InitialScan_DoesNotPublish(t *testing.T) {
	t.Parallel()
	pub := &fakePublisher{}
	lister := &scriptedLister{
		scripts: [][]int32{
			{100, 200}, // 初回スキャン
		},
		called: make(chan struct{}, 8),
	}
	src := process.New(pub, process.Config{
		Interval:    50 * time.Millisecond,
		ProcessName: "claude.exe",
		Lister:      lister.list,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = src.Run(ctx) }()
	defer cancel()

	// 初回スキャン完了まで待つ
	select {
	case <-lister.called:
	case <-time.After(2 * time.Second):
		t.Fatal("初回 list が呼ばれなかった")
	}
	// ticker が一度走らないうちに cancel
	time.Sleep(20 * time.Millisecond)
	cancel()

	if got := len(pub.Events()); got != 0 {
		t.Errorf("初回スキャンで Publish された: %d 件 (want 0)", got)
	}
}

// A3: 前回居た PID が消えたら Stop event が発火する
func TestSource_PIDDisappears_PublishesStop(t *testing.T) {
	t.Parallel()
	pub := &fakePublisher{}
	lister := &scriptedLister{
		scripts: [][]int32{
			{100, 200}, // 初回スキャン: PID 100, 200 が存在
			{200},      // 2 回目: PID 100 が消失
		},
		called: make(chan struct{}, 8),
	}
	src := process.New(pub, process.Config{
		Interval:    50 * time.Millisecond,
		ProcessName: "claude.exe",
		Lister:      lister.list,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = src.Run(ctx) }()
	defer cancel()

	// 2 回呼ばれるまで待つ
	for range 2 {
		select {
		case <-lister.called:
		case <-time.After(2 * time.Second):
			t.Fatal("list が呼ばれなかった")
		}
	}
	// 2 回目スキャン後に Publish が来るまで少し待つ
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(pub.Events()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	events := pub.Events()
	if len(events) != 1 {
		t.Fatalf("Publish 件数: got %d, want 1 (events=%+v)", len(events), events)
	}
	ev := events[0]
	if ev.Kind != event.KindStop {
		t.Errorf("Kind: got %q, want %q", ev.Kind, event.KindStop)
	}
	if ev.Source != "process" {
		t.Errorf("Source: got %q, want \"process\"", ev.Source)
	}
	if ev.ID == "" {
		t.Error("Event.ID が空")
	}
}

// A3: 同じ PID が居続けるなら何もしない
func TestSource_StablePIDs_NoPublish(t *testing.T) {
	t.Parallel()
	pub := &fakePublisher{}
	lister := &scriptedLister{
		scripts: [][]int32{
			{100, 200},
			{100, 200},
			{100, 200},
		},
		called: make(chan struct{}, 8),
	}
	src := process.New(pub, process.Config{
		Interval:    30 * time.Millisecond,
		ProcessName: "claude.exe",
		Lister:      lister.list,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = src.Run(ctx) }()
	defer cancel()

	for range 3 {
		select {
		case <-lister.called:
		case <-time.After(2 * time.Second):
			t.Fatal("list が呼ばれなかった")
		}
	}
	time.Sleep(50 * time.Millisecond)
	cancel()

	if got := len(pub.Events()); got != 0 {
		t.Errorf("変化なしなのに Publish された: %d 件", got)
	}
}

// A3: Lister がエラーを返してもクラッシュせず継続
func TestSource_ListerError_DoesNotCrash(t *testing.T) {
	t.Parallel()
	pub := &fakePublisher{}
	calls := make(chan struct{}, 4)
	failingLister := func(_ string) ([]int32, error) {
		select {
		case calls <- struct{}{}:
		default:
		}
		return nil, errors.New("simulated list failure")
	}
	src := process.New(pub, process.Config{
		Interval:    30 * time.Millisecond,
		ProcessName: "claude.exe",
		Lister:      failingLister,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = src.Run(ctx) }()
	defer cancel()

	// 2 回はエラーでも呼び続けることを確認
	for range 2 {
		select {
		case <-calls:
		case <-time.After(2 * time.Second):
			t.Fatal("エラー時に list が呼ばれなかった")
		}
	}
	cancel()

	if got := len(pub.Events()); got != 0 {
		t.Errorf("エラー時に Publish されてはいけない: %d 件", got)
	}
}

// New が nil ハンドラ群に対して安全なデフォルトを設定する
func TestNew_DefaultsApplied(t *testing.T) {
	t.Parallel()
	pub := &fakePublisher{}
	// Interval=0, ProcessName="", Lister=nil → デフォルトに置き換わる
	src := process.New(pub, process.Config{}, nil)
	if src == nil {
		t.Fatal("New returned nil")
	}
	// Run を即 cancel して default Lister が gopsutil 呼び出しを行うことを確認 (副作用は許容)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := src.Run(ctx); err == nil {
		t.Error("ctx 即 cancel 時に Run は ctx.Err() を返すべき")
	}
}

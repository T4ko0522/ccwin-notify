package clock

import (
	"sync"
	"time"
)

// Clock はテスト可能な時刻抽象 (D-15 / F6)。
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	NewTimer(d time.Duration) Timer
}

// Timer は Clock.NewTimer が返す値。
type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// realClock は time パッケージを直接使う実装。
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (realClock) NewTimer(d time.Duration) Timer { return &realTimer{t: time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (rt *realTimer) C() <-chan time.Time      { return rt.t.C }
func (rt *realTimer) Stop() bool               { return rt.t.Stop() }
func (rt *realTimer) Reset(d time.Duration) bool { return rt.t.Reset(d) }

// Real は実時刻の Clock 実装を返す。
func Real() Clock { return realClock{} }

// FakeClock は Advance で時刻を操作できるテスト用実装。
type FakeClock interface {
	Clock
	Advance(d time.Duration)
}

type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*fakeTimer
}

type fakeTimer struct {
	deadline time.Time
	ch       chan time.Time
	stopped  bool
}

// NewFake は initial を起点にした FakeClock を返す。
func NewFake(initial time.Time) FakeClock {
	return &fakeClock{now: initial}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) After(d time.Duration) <-chan time.Time {
	return f.NewTimer(d).C()
}

func (f *fakeClock) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{
		deadline: f.now.Add(d),
		ch:       make(chan time.Time, 1),
	}
	f.waiters = append(f.waiters, t)
	return t
}

func (ft *fakeTimer) C() <-chan time.Time      { return ft.ch }
func (ft *fakeTimer) Stop() bool               { ft.stopped = true; return true }
func (ft *fakeTimer) Reset(d time.Duration) bool { return true }

// Advance は指定分だけ時刻を進め、deadline を過ぎた Timer に通知する。
func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	now := f.now
	remaining := f.waiters[:0]
	for _, t := range f.waiters {
		if !t.stopped && !t.deadline.After(now) {
			select {
			case t.ch <- now:
			default:
			}
		} else {
			remaining = append(remaining, t)
		}
	}
	f.waiters = remaining
	f.mu.Unlock()
}

// internal/clock パッケージのユニットテスト (サイクル 1)
// テスト ID: T-008, T-009
// 受入条件: F6 — 時刻は internal/clock.Clock 経由 (フレイキー禁止)
package clock_test

import (
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/clock"
)

// T-008: Real() が time.Now() に近い時刻を返す
func TestRealClock_Now(t *testing.T) {
	t.Parallel()
	before := time.Now()
	c := clock.Real()
	got := c.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Errorf("Real.Now() = %v は [%v, %v] の範囲外", got, before, after)
	}
}

// T-008: FakeClock.Now() が initial 値を返す
func TestFakeClock_Now_InitialValue(t *testing.T) {
	t.Parallel()
	initial := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	fc := clock.NewFake(initial)

	got := fc.Now()
	if !got.Equal(initial) {
		t.Errorf("FakeClock.Now() = %v, want %v", got, initial)
	}
}

// T-009: FakeClock.Advance(d) でクロックが d 分進む
func TestFakeClock_Advance(t *testing.T) {
	t.Parallel()
	initial := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	fc := clock.NewFake(initial)

	fc.Advance(5 * time.Minute)

	got := fc.Now()
	want := initial.Add(5 * time.Minute)
	if !got.Equal(want) {
		t.Errorf("Advance後のNow() = %v, want %v", got, want)
	}
}

// T-009: FakeClock.After(d) が Advance で起動される
func TestFakeClock_After(t *testing.T) {
	t.Parallel()
	initial := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	fc := clock.NewFake(initial)

	ch := fc.After(3 * time.Second)

	// まだ発火しないことを確認 (ノンブロッキングチェック)
	select {
	case <-ch:
		t.Fatal("Advance前にAfterが発火した")
	default:
		// 期待通り
	}

	// Advance で時刻を進める
	fc.Advance(3 * time.Second)

	// 発火を待つ
	select {
	case <-ch:
		// 期待通り
	case <-time.After(2 * time.Second):
		t.Fatal("Advance後にAfterが発火しなかった")
	}
}

// T-009: FakeClock.NewTimer(d) が Advance で起動される
func TestFakeClock_NewTimer(t *testing.T) {
	t.Parallel()
	initial := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	fc := clock.NewFake(initial)

	timer := fc.NewTimer(2 * time.Second)

	// 発火前
	select {
	case <-timer.C():
		t.Fatal("Advance前にTimerが発火した")
	default:
	}

	// Advance
	fc.Advance(2 * time.Second)

	select {
	case <-timer.C():
		// 期待通り
	case <-time.After(2 * time.Second):
		t.Fatal("Advance後にTimerが発火しなかった")
	}
}

// T-169: Real().After(d) が非 nil チャネルを返す (realTimer.C をカバー)
func TestRealClock_After_ReturnsChannel(t *testing.T) {
	t.Parallel()
	c := clock.Real()
	ch := c.After(10 * time.Hour) // 発火しない long duration
	if ch == nil {
		t.Error("Real().After(): チャネルが nil")
	}
}

// T-170: Real().NewTimer(d) が非 nil Timer を返し Stop できる (realTimer.Stop をカバー)
func TestRealClock_NewTimer_Stop(t *testing.T) {
	t.Parallel()
	c := clock.Real()
	timer := c.NewTimer(10 * time.Hour) // 発火しない
	if timer == nil {
		t.Fatal("Real().NewTimer(): Timer が nil")
	}
	if timer.C() == nil {
		t.Error("timer.C(): チャネルが nil")
	}
	// Stop を呼んでもパニックしない
	_ = timer.Stop()
}

// T-171: Real().NewTimer(d).Reset(d) がパニックしない (realTimer.Reset をカバー)
func TestRealClock_NewTimer_Reset(t *testing.T) {
	t.Parallel()
	c := clock.Real()
	timer := c.NewTimer(10 * time.Hour)
	timer.Stop() // 先に Stop
	// Reset を呼んでもパニックしない
	_ = timer.Reset(10 * time.Hour)
	_ = timer.Stop()
}

// T-172: FakeClock の fakeTimer.Stop() が常に true を返す
func TestFakeClock_Timer_Stop(t *testing.T) {
	t.Parallel()
	initial := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	fc := clock.NewFake(initial)
	timer := fc.NewTimer(1 * time.Second)

	if !timer.Stop() {
		t.Error("FakeClock Timer.Stop(): got false, want true")
	}
}

// T-173: FakeClock の fakeTimer.Reset(d) がパニックしない
func TestFakeClock_Timer_Reset(t *testing.T) {
	t.Parallel()
	initial := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	fc := clock.NewFake(initial)
	timer := fc.NewTimer(1 * time.Second)
	// Reset を呼んでもパニックしない
	_ = timer.Reset(2 * time.Second)
}

// Clock interface のコンパイル時 assertion
var _ clock.Clock = clock.Real()

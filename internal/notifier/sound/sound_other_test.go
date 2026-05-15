//go:build !windows

// Package sound のテスト (非 Windows 環境) — Phase 3 Retry 2
// テスト ID: T-107〜T-112
// 受入条件: B1 (Sound Notifier) / E4 (ctx cancel)
package sound_test

import (
	"context"
	"testing"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/notifier/sound"
)

// T-107 / B1: 非 Windows の New が sound.Notifier を返し Name="sound"
func TestNew_NonWindows_Name(t *testing.T) {
	t.Parallel()
	n := sound.New(sound.Config{})
	if got := n.Name(); got != "sound" {
		t.Errorf("Name: got %q, want \"sound\"", got)
	}
}

// T-108 / B4: 非 Windows の noopSound は全 Kind で Wants=true
func TestNew_NonWindows_Wants_AllKinds(t *testing.T) {
	t.Parallel()
	n := sound.New(sound.Config{})
	for kind := range event.ValidKinds {
		if !n.Wants(kind) {
			t.Errorf("Wants(%q): got false, want true (noopSound always true)", kind)
		}
	}
}

// T-109 / B1: 非 Windows の Notify は常に nil を返す (再生せず)
func TestNew_NonWindows_Notify_AlwaysNil(t *testing.T) {
	t.Parallel()
	n := sound.New(sound.Config{WavPath: "/nonexistent.wav"})
	ev := event.Event{Kind: event.KindStop, Title: "test"}
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Errorf("非 Windows Notify: got %v, want nil", err)
	}
}

// T-110 / E4: 非 Windows の Notify は ctx cancel 後も nil を返す (noopSound はブロックしない)
func TestNew_NonWindows_Notify_CancelledCtx_Nil(t *testing.T) {
	t.Parallel()
	n := sound.New(sound.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ev := event.Event{Kind: event.KindStop, Title: "test"}
	if err := n.Notify(ctx, ev); err != nil {
		t.Errorf("非 Windows Notify(cancelled ctx): got %v, want nil", err)
	}
}

// ---- FakeSound ----

// T-111 / B1: FakeSound が Notify 呼び出しを記録する
func TestFakeSound_RecordsCalls(t *testing.T) {
	t.Parallel()
	f := &sound.FakeSound{}
	ev1 := event.Event{Kind: event.KindStop, Title: "ev1"}
	ev2 := event.Event{Kind: event.KindNotification, Title: "ev2"}

	if err := f.Notify(context.Background(), ev1); err != nil {
		t.Fatalf("Notify ev1: %v", err)
	}
	if err := f.Notify(context.Background(), ev2); err != nil {
		t.Fatalf("Notify ev2: %v", err)
	}

	if len(f.Calls) != 2 {
		t.Errorf("Calls: got %d, want 2", len(f.Calls))
	}
	if f.Calls[0].Kind != event.KindStop {
		t.Errorf("Calls[0].Kind: got %q, want %q", f.Calls[0].Kind, event.KindStop)
	}
	if f.Calls[1].Kind != event.KindNotification {
		t.Errorf("Calls[1].Kind: got %q, want %q", f.Calls[1].Kind, event.KindNotification)
	}
}

// T-112 / B1: FakeSound.Name="sound", Wants 常に true
func TestFakeSound_NameAndWants(t *testing.T) {
	t.Parallel()
	f := &sound.FakeSound{}
	if got := f.Name(); got != "sound" {
		t.Errorf("FakeSound.Name: got %q, want \"sound\"", got)
	}
	for kind := range event.ValidKinds {
		if !f.Wants(kind) {
			t.Errorf("FakeSound.Wants(%q): got false, want true", kind)
		}
	}
}

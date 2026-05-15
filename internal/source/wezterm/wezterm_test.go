package wezterm

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
)

// stubFetcher は固定テキストを返す TextFetcher。返却値を入れ替えて状態遷移を再現する。
type stubFetcher struct {
	mu    sync.Mutex
	texts []string
	err   error
	calls int
}

func (s *stubFetcher) FetchText(_ context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	if s.calls >= len(s.texts) {
		return s.texts[len(s.texts)-1], nil
	}
	t := s.texts[s.calls]
	s.calls++
	return t, nil
}

// recordingPublisher は Publish 呼び出しを記録するだけのテスト用 publisher。
type recordingPublisher struct {
	mu     sync.Mutex
	events []event.Event
}

func (r *recordingPublisher) Publish(_ context.Context, e event.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recordingPublisher) snapshot() []event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]event.Event, len(r.events))
	copy(out, r.events)
	return out
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestPollOnce_FiresOnFalseToTrueTransition は false→true 遷移で 1 度だけ発火することを確認する。
func TestPollOnce_FiresOnFalseToTrueTransition(t *testing.T) {
	pub := &recordingPublisher{}
	fetcher := &stubFetcher{texts: []string{
		"some normal text", // matched=false → detected stays false, no publish
		"prompt with Enter to select · ↑/↓ to navigate", // matched=true, false→true → publish
		"prompt with Enter to select · ↑/↓ to navigate", // matched=true, true→true → no publish
		"normal text again",             // matched=false, true→false → no publish
		"matches again Enter to select", // matched=true, false→true → publish
	}}

	src := New(pub, Config{Fetcher: fetcher}, quietLogger())
	ctx := context.Background()
	for i := 0; i < len(fetcher.texts); i++ {
		src.pollOnce(ctx)
	}

	events := pub.snapshot()
	if got, want := len(events), 2; got != want {
		t.Fatalf("publish count = %d, want %d", got, want)
	}
	for i, ev := range events {
		if ev.Kind != event.KindNotification {
			t.Errorf("events[%d].Kind = %v, want %v", i, ev.Kind, event.KindNotification)
		}
		if ev.Source != "wezterm" {
			t.Errorf("events[%d].Source = %q, want %q", i, ev.Source, "wezterm")
		}
		if ev.Title == "" {
			t.Errorf("events[%d].Title is empty", i)
		}
	}
}

// TestPollOnce_NoFireWhenSignatureAbsent は Signature 不在で発火しないことを確認する。
func TestPollOnce_NoFireWhenSignatureAbsent(t *testing.T) {
	pub := &recordingPublisher{}
	fetcher := &stubFetcher{texts: []string{
		"normal claude conversation",
		"more text",
		"still nothing",
	}}

	src := New(pub, Config{Fetcher: fetcher}, quietLogger())
	ctx := context.Background()
	for i := 0; i < len(fetcher.texts); i++ {
		src.pollOnce(ctx)
	}

	if got := len(pub.snapshot()); got != 0 {
		t.Fatalf("publish count = %d, want 0", got)
	}
}

// TestPollOnce_CustomSignature は Config.Signature が反映されることを確認する。
func TestPollOnce_CustomSignature(t *testing.T) {
	pub := &recordingPublisher{}
	fetcher := &stubFetcher{texts: []string{
		"foo MY_CUSTOM_PROMPT bar",
		"normal",
	}}

	src := New(pub, Config{
		Fetcher:   fetcher,
		Signature: "MY_CUSTOM_PROMPT",
	}, quietLogger())
	ctx := context.Background()
	for i := 0; i < len(fetcher.texts); i++ {
		src.pollOnce(ctx)
	}

	events := pub.snapshot()
	if len(events) != 1 {
		t.Fatalf("publish count = %d, want 1", len(events))
	}
}

// TestPollOnce_FetcherErrorIsTolerated は Fetcher エラーで panic / publish しないことを確認。
func TestPollOnce_FetcherErrorIsTolerated(t *testing.T) {
	pub := &recordingPublisher{}
	fetcher := &stubFetcher{err: errors.New("wezterm not found")}

	src := New(pub, Config{Fetcher: fetcher}, quietLogger())
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		src.pollOnce(ctx)
	}

	if got := len(pub.snapshot()); got != 0 {
		t.Fatalf("publish count = %d, want 0 (errors should be silent)", got)
	}
}

// TestRun_PollsOnTickAndStopsOnContextCancel は Run の polling と ctx cancel での停止を確認。
func TestRun_PollsOnTickAndStopsOnContextCancel(t *testing.T) {
	pub := &recordingPublisher{}
	fetcher := &stubFetcher{texts: []string{
		"none",
		"none",
		"Enter to select hit",
	}}

	src := New(pub, Config{
		Fetcher:      fetcher,
		PollInterval: 10 * time.Millisecond,
	}, quietLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = src.Run(ctx)
		close(done)
	}()

	// 3 回 tick して 1 度発火するのを待つ
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(pub.snapshot()) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancel")
	}

	if got := len(pub.snapshot()); got < 1 {
		t.Fatalf("expected at least 1 publish, got %d", got)
	}
}

// TestNew_DefaultsApplied は未指定 Config のフィールドに既定値が入ることを確認。
func TestNew_DefaultsApplied(t *testing.T) {
	pub := &recordingPublisher{}
	src := New(pub, Config{Fetcher: &stubFetcher{texts: []string{""}}}, quietLogger())

	if src.cfg.PollInterval != time.Second {
		t.Errorf("PollInterval = %v, want 1s", src.cfg.PollInterval)
	}
	if src.cfg.Signature != defaultSignature {
		t.Errorf("Signature = %q, want %q", src.cfg.Signature, defaultSignature)
	}
	if src.cfg.Title != defaultTitle {
		t.Errorf("Title = %q, want %q", src.cfg.Title, defaultTitle)
	}
	if src.cfg.Body != defaultBody {
		t.Errorf("Body = %q, want %q", src.cfg.Body, defaultBody)
	}
}

// TestNew_NegativePaneIDClamped は PaneID<0 が 0 にクランプされることを確認。
func TestNew_NegativePaneIDClamped(t *testing.T) {
	pub := &recordingPublisher{}
	src := New(pub, Config{PaneID: -5, Fetcher: &stubFetcher{texts: []string{""}}}, quietLogger())
	if src.cfg.PaneID != 0 {
		t.Errorf("PaneID = %d, want 0", src.cfg.PaneID)
	}
}

//go:build !windows

// Package sound は WAV 再生 Notifier を提供する (D-11)。
// 非 Windows では stub 実装 (FakeSound のみ)。
package sound

import (
	"context"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/notifier"
)

// Config は Sound Notifier の設定。
type Config struct {
	WavPath  string
	KindMask map[event.EventKind]bool
}

type noopSound struct{}

var _ notifier.Notifier = (*noopSound)(nil)

// New は非 Windows では no-op Notifier を返す。
func New(cfg Config) notifier.Notifier {
	return &noopSound{}
}

func (n *noopSound) Name() string                                  { return "sound" }
func (n *noopSound) Wants(_ event.EventKind) bool                  { return true }
func (n *noopSound) Notify(_ context.Context, _ event.Event) error { return nil }

// FakeSound はテスト用の in-memory 実装。
type FakeSound struct {
	Calls []event.Event
}

func (f *FakeSound) Name() string                 { return "sound" }
func (f *FakeSound) Wants(_ event.EventKind) bool { return true }
func (f *FakeSound) Notify(_ context.Context, ev event.Event) error {
	f.Calls = append(f.Calls, ev)
	return nil
}

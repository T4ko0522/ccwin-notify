//go:build windows

// Package sound は WAV 再生 Notifier を提供する (D-11)。
// Windows のみ: WinMM PlaySound API を使用。
package sound

import (
	"context"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/notifier"
)

var (
	winmm     = windows.NewLazyDLL("winmm.dll")
	playSound = winmm.NewProc("PlaySoundW")
)

const (
	sndFilename  = 0x00020000 // SND_FILENAME
	sndAsync     = 0x00000001 // SND_ASYNC
	sndNoDefault = 0x00000002 // SND_NODEFAULT
	sndMemory    = 0x00000004 // SND_MEMORY
)

// Config は Sound Notifier の設定。
type Config struct {
	WavPath  string
	KindMask map[event.EventKind]bool
}

type soundNotifier struct {
	wavPath  string
	kindMask map[event.EventKind]bool
}

var _ notifier.Notifier = (*soundNotifier)(nil)

// New は WAV 再生 Notifier を返す。
func New(cfg Config) notifier.Notifier {
	return &soundNotifier{wavPath: cfg.WavPath, kindMask: cfg.KindMask}
}

func (s *soundNotifier) Name() string { return "sound" }

func (s *soundNotifier) Wants(kind event.EventKind) bool {
	return len(s.kindMask) == 0 || s.kindMask[kind]
}

func (s *soundNotifier) Notify(ctx context.Context, ev event.Event) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if len(s.kindMask) > 0 && !s.kindMask[ev.Kind] {
		return nil
	}

	if s.wavPath == "" {
		// WavPath 未指定: 同梱 default.wav を SND_MEMORY で再生する。
		// embed バイト列は static なので SND_ASYNC でも安全に参照され続ける。
		if len(defaultWAV) == 0 {
			return nil
		}
		playSound.Call(
			uintptr(unsafe.Pointer(&defaultWAV[0])),
			0,
			sndMemory|sndAsync|sndNoDefault,
		)
		return nil
	}

	pathPtr, err := windows.UTF16PtrFromString(s.wavPath)
	if err != nil {
		return err
	}

	// PlaySoundW(pszSound, hmod, fdwSound) — ファイル名 + 非同期再生
	playSound.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		sndFilename|sndAsync|sndNoDefault,
	)

	return nil
}

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

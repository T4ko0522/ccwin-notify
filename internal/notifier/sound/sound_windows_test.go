//go:build windows

// Package sound のテスト (Windows 環境) — Phase 3 Retry 2
// テスト ID: T-113〜T-118
// 受入条件: B1 (Sound Notifier Windows) / B4 (kind_mask) / E4 (ctx cancel)
//
// Windows 環境のみ: soundNotifier は PlaySoundW を呼ぶため、
// 実音声ファイルなしのテストは WavPath="" / KindMask / ctx cancel でカバーする。
// 実 PlaySoundW の呼び出し成功は E2E 相当になるため対象外とする。
package sound_test

import (
	"context"
	"testing"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/notifier/sound"
)

// T-113 / B1: Windows の New が Name="sound" を返す
func TestNew_Windows_Name(t *testing.T) {
	t.Parallel()
	n := sound.New(sound.Config{})
	if got := n.Name(); got != "sound" {
		t.Errorf("Name: got %q, want \"sound\"", got)
	}
}

// T-114 / B4: KindMask が空なら全 Kind で Wants=true
func TestNew_Windows_Wants_EmptyMask(t *testing.T) {
	t.Parallel()
	n := sound.New(sound.Config{})
	for kind := range event.ValidKinds {
		if !n.Wants(kind) {
			t.Errorf("Wants(%q): got false, want true (empty KindMask)", kind)
		}
	}
}

// T-115 / B4: KindMask で許可外 Kind は Wants=false
func TestNew_Windows_Wants_KindMask(t *testing.T) {
	t.Parallel()
	n := sound.New(sound.Config{
		KindMask: map[event.EventKind]bool{
			event.KindStop: true,
		},
	})
	if !n.Wants(event.KindStop) {
		t.Error("Wants(KindStop): got false, want true")
	}
	if n.Wants(event.KindNotification) {
		t.Error("Wants(KindNotification): got true, want false (kind_mask)")
	}
}

// T-116 / B1: WavPath="" のとき Notify は nil を返す (PlaySoundW を呼ばない)
func TestNew_Windows_Notify_EmptyWavPath_Nil(t *testing.T) {
	t.Parallel()
	n := sound.New(sound.Config{WavPath: ""})
	ev := event.Event{Kind: event.KindStop, Title: "test"}
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Errorf("WavPath 空: got %v, want nil", err)
	}
}

// T-117 / E4: キャンセル済み ctx で Notify → ctx.Err() を返す
func TestNew_Windows_Notify_CancelledCtx(t *testing.T) {
	t.Parallel()
	n := sound.New(sound.Config{WavPath: "/nonexistent.wav"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ev := event.Event{Kind: event.KindStop, Title: "test"}
	err := n.Notify(ctx, ev)
	if err == nil {
		t.Error("キャンセル済み ctx: nil が返ったが error が期待される")
	}
	if err != context.Canceled {
		t.Errorf("キャンセル済み ctx: got %v, want context.Canceled", err)
	}
}

// T-118 / B4: KindMask で弾かれた Kind は Notify が nil を返す (PlaySoundW を呼ばない)
func TestNew_Windows_Notify_KindMaskFiltered_Nil(t *testing.T) {
	t.Parallel()
	n := sound.New(sound.Config{
		WavPath: "/nonexistent.wav",
		KindMask: map[event.EventKind]bool{
			event.KindStop: true,
		},
	})
	// KindNotification は KindMask で弾かれる → PlaySoundW を呼ばず nil
	ev := event.Event{Kind: event.KindNotification, Title: "test"}
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Errorf("KindMask filtered: got %v, want nil", err)
	}
}

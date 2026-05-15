// internal/notifier/toast パッケージのユニットテスト (サイクル 5)
// テスト ID: T-049〜T-052
// 受入条件: B1 / E4 / B4 — Toast Fake + ctx timeout + kind_mask
package toast_test

import (
	"context"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/notifier/toast"
)

// T-049: FakeToaster.Push が呼ばれ記録が残る
func TestToastNotifier_FakeToaster_Called(t *testing.T) {
	t.Parallel()
	fakeToaster := toast.NewFakeToaster()
	n := toast.New(fakeToaster)

	ev := event.Event{
		Kind:  event.KindStop,
		Title: "Claude Code: response complete",
		Body:  "task done",
	}

	ctx := context.Background()
	if err := n.Notify(ctx, ev); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	calls := fakeToaster.Calls()
	if len(calls) != 1 {
		t.Fatalf("Push呼出回数: got %d, want 1", len(calls))
	}
	if calls[0].Title != "Claude Code: response complete" {
		t.Errorf("Title: got %q, want \"Claude Code: response complete\"", calls[0].Title)
	}
	if calls[0].Body != "task done" {
		t.Errorf("Body: got %q, want \"task done\"", calls[0].Body)
	}
}

// T-050: ctx.Done() 後に Notify を呼ぶと即 return (context.Canceled)
func TestToastNotifier_CtxCanceled(t *testing.T) {
	t.Parallel()
	fakeToaster := toast.NewFakeToaster()
	n := toast.New(fakeToaster)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 即座にキャンセル

	ev := event.Event{Kind: event.KindStop}
	err := n.Notify(ctx, ev)
	if err == nil {
		t.Fatal("キャンセル済み ctx: error が返るべきだが nil だった")
	}
}

// T-050: ctx タイムアウトで Notify が打ち切れる (E4)
func TestToastNotifier_CtxTimeout(t *testing.T) {
	t.Parallel()
	// ブロッキングなToaster (Notify がブロックされているとき ctx タイムアウトで戻る)
	blockingToaster := toast.NewBlockingFakeToaster()
	n := toast.New(blockingToaster)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	ev := event.Event{Kind: event.KindStop}
	done := make(chan error, 1)
	go func() {
		done <- n.Notify(ctx, ev)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("タイムアウト: error が返るべきだが nil だった")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("タイムアウト: Notify が 2 秒以上ブロックしている")
	}
}

// T-051: kind_mask が設定済みの Notifier は許可外 Kind を無視する (B4)
func TestToastNotifier_KindMask_FilterOut(t *testing.T) {
	t.Parallel()
	fakeToaster := toast.NewFakeToaster()
	// Stop のみ許可
	kindMask := map[event.EventKind]bool{
		event.KindStop: true,
	}
	n := toast.NewWithKindMask(fakeToaster, kindMask)

	// Notification を送っても無視される
	ev := event.Event{Kind: event.KindNotification, Title: "filtered"}
	ctx := context.Background()
	if err := n.Notify(ctx, ev); err != nil {
		t.Fatalf("Notify (kind masked): %v", err)
	}

	calls := fakeToaster.Calls()
	if len(calls) != 0 {
		t.Errorf("kind_mask でフィルタされるべきだが Push が呼ばれた: %v", calls)
	}
}

// T-149: toastNotifier.Name() が "toast" を返す
func TestToastNotifier_Name(t *testing.T) {
	t.Parallel()
	n := toast.New(toast.NewFakeToaster())
	if got := n.Name(); got != "toast" {
		t.Errorf("Name: got %q, want \"toast\"", got)
	}
}

// T-150: toastNotifier.Wants() が KindMask に従って動作する
func TestToastNotifier_Wants(t *testing.T) {
	t.Parallel()

	// KindMask なし = 全 Kind 通す
	n := toast.New(toast.NewFakeToaster())
	for kind := range event.ValidKinds {
		if !n.Wants(kind) {
			t.Errorf("Wants(%q) with empty mask: got false, want true", kind)
		}
	}

	// KindMask あり
	kindMask := map[event.EventKind]bool{event.KindStop: true}
	n2 := toast.NewWithKindMask(toast.NewFakeToaster(), kindMask)
	if !n2.Wants(event.KindStop) {
		t.Error("Wants(KindStop): got false, want true")
	}
	if n2.Wants(event.KindNotification) {
		t.Error("Wants(KindNotification): got true, want false (kind_mask)")
	}
}

// T-052: kind_mask が空の Notifier は全 Kind を通す (B4)
func TestToastNotifier_KindMask_Empty_AllowAll(t *testing.T) {
	t.Parallel()
	fakeToaster := toast.NewFakeToaster()
	n := toast.NewWithKindMask(fakeToaster, nil) // nil = 全有効

	kinds := []event.EventKind{
		event.KindStop,
		event.KindNotification,
		event.KindSubagentStop,
	}

	ctx := context.Background()
	for _, kind := range kinds {
		ev := event.Event{Kind: kind}
		if err := n.Notify(ctx, ev); err != nil {
			t.Fatalf("Notify(%v): %v", kind, err)
		}
	}

	calls := fakeToaster.Calls()
	if len(calls) != len(kinds) {
		t.Errorf("全Kind通過: got %d calls, want %d", len(calls), len(kinds))
	}
}

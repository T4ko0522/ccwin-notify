// internal/notifier パッケージのユニットテスト (サイクル 1)
// テスト ID: T-002, T-012
// 受入条件: A4 / B2 — Notifier interface + FakeNotifier compile-time assertion
package notifier_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/notifier"
)

// T-002 / B2: FakeNotifier が Notifier interface を満たすコンパイル時 assertion
// このコードがコンパイルできる = FakeNotifier は Notifier を満たす
var _ notifier.Notifier = (*notifier.FakeNotifier)(nil)

// T-012: FakeNotifier.Notify が呼出記録を保持する
func TestFakeNotifier_Records(t *testing.T) {
	t.Parallel()
	fn := notifier.NewFakeNotifier("toast")

	ev1 := event.Event{Kind: event.KindStop, Title: "event1"}
	ev2 := event.Event{Kind: event.KindNotification, Title: "event2"}

	ctx := context.Background()
	if err := fn.Notify(ctx, ev1); err != nil {
		t.Fatalf("Notify(ev1): %v", err)
	}
	if err := fn.Notify(ctx, ev2); err != nil {
		t.Fatalf("Notify(ev2): %v", err)
	}

	if len(fn.Records) != 2 {
		t.Fatalf("Records len: got %d, want 2", len(fn.Records))
	}
	if fn.Records[0].Title != "event1" {
		t.Errorf("Records[0].Title: got %q, want \"event1\"", fn.Records[0].Title)
	}
	if fn.Records[1].Title != "event2" {
		t.Errorf("Records[1].Title: got %q, want \"event2\"", fn.Records[1].Title)
	}
}

// T-012: FakeNotifier.Name() が生成時に渡した名前を返す
func TestFakeNotifier_Name(t *testing.T) {
	t.Parallel()
	fn := notifier.NewFakeNotifier("webhook.discord")
	if fn.Name() != "webhook.discord" {
		t.Errorf("Name(): got %q, want \"webhook.discord\"", fn.Name())
	}
}

// T-012: FakeNotifier.Err が設定されていると Notify がそのエラーを返す
func TestFakeNotifier_ReturnsErr(t *testing.T) {
	t.Parallel()
	fn := notifier.NewFakeNotifier("sound")
	expectedErr := errors.New("sound device not found")
	fn.Err = expectedErr

	ctx := context.Background()
	ev := event.Event{Kind: event.KindStop}
	err := fn.Notify(ctx, ev)
	if err != expectedErr {
		t.Errorf("Notify with Err: got %v, want %v", err, expectedErr)
	}
}

// T-143 / B1: FakeNotifier.Wants は常に true
func TestFakeNotifier_Wants(t *testing.T) {
	t.Parallel()
	fn := notifier.NewFakeNotifier("toast")
	for kind := range event.ValidKinds {
		if !fn.Wants(kind) {
			t.Errorf("Wants(%q): got false, want true", kind)
		}
	}
}

// T-144 / B1: FakeNotifier.Len がスレッドセーフに件数を返す
func TestFakeNotifier_Len(t *testing.T) {
	t.Parallel()
	fn := notifier.NewFakeNotifier("toast")
	if fn.Len() != 0 {
		t.Errorf("Len (初期): got %d, want 0", fn.Len())
	}
	ev := event.Event{Kind: event.KindStop}
	_ = fn.Notify(context.Background(), ev)
	if fn.Len() != 1 {
		t.Errorf("Len (1件後): got %d, want 1", fn.Len())
	}
}

// T-145 / B1: FakeNotifier.Get が i 番目の Record を返す
func TestFakeNotifier_Get(t *testing.T) {
	t.Parallel()
	fn := notifier.NewFakeNotifier("toast")
	ev := event.Event{Kind: event.KindStop, Title: "hello"}
	_ = fn.Notify(context.Background(), ev)

	got, ok := fn.Get(0)
	if !ok {
		t.Fatal("Get(0): ok=false, want true")
	}
	if got.Title != "hello" {
		t.Errorf("Get(0).Title: got %q, want \"hello\"", got.Title)
	}

	// 範囲外
	_, ok = fn.Get(999)
	if ok {
		t.Error("Get(999): ok=true, want false")
	}
}

// T-146 / B1: FakeNotifier.Reset が記録をクリアする
func TestFakeNotifier_Reset(t *testing.T) {
	t.Parallel()
	fn := notifier.NewFakeNotifier("toast")
	_ = fn.Notify(context.Background(), event.Event{Kind: event.KindStop})
	fn.Reset()
	if fn.Len() != 0 {
		t.Errorf("Reset 後の Len: got %d, want 0", fn.Len())
	}
}

// T-147 / B1: FakeNotifier.WaitForN は Notify が n 件以上呼ばれたら close される
func TestFakeNotifier_WaitForN(t *testing.T) {
	t.Parallel()
	fn := notifier.NewFakeNotifier("toast")

	// 2 件 Notify されたら close されるチャネルを取得
	done := fn.WaitForN(2)

	// 1 件目 (まだ close されない)
	_ = fn.Notify(context.Background(), event.Event{Kind: event.KindStop, Title: "1"})
	select {
	case <-done:
		t.Error("WaitForN(2): 1件目で close されてはならない")
	default:
	}

	// 2 件目 (close される)
	_ = fn.Notify(context.Background(), event.Event{Kind: event.KindStop, Title: "2"})
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-done:
		// 期待通り
	case <-timer.C:
		t.Error("WaitForN(2): 2件目後に close されなかった")
	}
}

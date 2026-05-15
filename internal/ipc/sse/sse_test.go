// internal/ipc/sse パッケージのユニット / 統合テスト (サイクル B)
// テスト ID: T-067〜T-069 の一部 (SSE Hub 側)
// 受入条件: TUI-2 / M-TEA-CMD — SSE Hub の Publish / Subscribe / Close
package sse_test

import (
	"context"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/ipc/sse"
)

// Hub の Publish が Subscribe チャネルに届く
func TestHub_PublishAndSubscribe(t *testing.T) {
	t.Parallel()
	hub := sse.NewHub()
	defer hub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, _ := hub.Subscribe(ctx)

	msg := sse.SSEMessage{
		Kind:    "event-published",
		Payload: event.Event{Kind: event.KindStop, Title: "test"},
	}
	hub.Publish(msg)

	select {
	case received := <-ch:
		if received.Kind != "event-published" {
			t.Errorf("Kind: got %q, want \"event-published\"", received.Kind)
		}
		ev, ok := received.Payload.(event.Event)
		if !ok {
			t.Fatalf("Payload の型が event.Event でない: %T", received.Payload)
		}
		if ev.Kind != event.KindStop {
			t.Errorf("Event.Kind: got %q, want %q", ev.Kind, event.KindStop)
		}
	case <-ctx.Done():
		t.Fatal("タイムアウト: Publish したメッセージが Subscribe チャネルに届かなかった")
	}
}

// Hub の Close で Subscribe チャネルが close される
func TestHub_Close_ClosesSubscribers(t *testing.T) {
	t.Parallel()
	hub := sse.NewHub()

	ctx := context.Background()
	ch, _ := hub.Subscribe(ctx)

	hub.Close()

	// Close 後はチャネルが close される
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("Close後: チャネルがまだ open (close されていない)")
		}
		// ok == false = close された (期待通り)
	case <-time.After(2 * time.Second):
		t.Fatal("タイムアウト: Close 後にチャネルが close されなかった")
	}
}

// ctx.Done() で購読者が自動解除される
func TestHub_Subscribe_CtxCancel_Unsubscribes(t *testing.T) {
	t.Parallel()
	hub := sse.NewHub()
	defer hub.Close()

	subCtx, cancelSub := context.WithCancel(context.Background())
	ch, _ := hub.Subscribe(subCtx)

	// ctx をキャンセルして購読解除
	cancelSub()
	time.Sleep(50 * time.Millisecond) // 解除が伝播するまで待つ

	// その後に Publish してもチャネルには届かない (or チャネルが close されている)
	hub.Publish(sse.SSEMessage{Kind: "event-published"})

	// チャネルが close されているか、またはタイムアウト
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("ctx cancel後: チャネルにメッセージが届いてはならない")
		}
		// ok == false は close (期待通り)
	case <-time.After(500 * time.Millisecond):
		// タイムアウト: チャネルに何も来なかった = 購読解除 OK
	}
}

// 複数購読者がいるとき Publish が全員に届く (非ブロッキング)
func TestHub_Publish_MultipleSubscribers(t *testing.T) {
	t.Parallel()
	hub := sse.NewHub()
	defer hub.Close()

	const numSubs = 3
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	chans := make([]<-chan sse.SSEMessage, numSubs)
	for i := range chans {
		chans[i], _ = hub.Subscribe(ctx)
	}

	msg := sse.SSEMessage{Kind: "dispatch-result"}
	hub.Publish(msg)

	for i, ch := range chans {
		select {
		case received := <-ch:
			if received.Kind != "dispatch-result" {
				t.Errorf("購読者%d: Kind got %q, want \"dispatch-result\"", i, received.Kind)
			}
		case <-ctx.Done():
			t.Fatalf("タイムアウト: 購読者%dがメッセージを受信しなかった", i)
		}
	}
}

// M-06: subscriber 上限を超えると ok=false で拒否される
func TestHub_Subscribe_LimitReached(t *testing.T) {
	t.Parallel()
	hub := sse.NewHub()
	defer hub.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const limit = 16 // sse.maxSubscribers と一致させること
	// limit 個までは accepted=true
	for i := 0; i < limit; i++ {
		_, ok := hub.Subscribe(ctx)
		if !ok {
			t.Fatalf("%d 件目で拒否された (limit=%d まで accept 想定)", i+1, limit)
		}
	}
	// limit+1 件目は ok=false
	ch, ok := hub.Subscribe(ctx)
	if ok {
		t.Fatal("上限超過なのに accept された")
	}
	// 返り値の channel は close 済みのため即時 receive で zero value + false
	select {
	case _, alive := <-ch:
		if alive {
			t.Error("拒否された subscriber の channel は close されているはず")
		}
	default:
		t.Error("拒否された channel は close されて非ブロックで返るはず")
	}
}

// 遅い購読者がいても他の購読者をブロックしない (非ブロッキング特性)
func TestHub_Publish_SlowSubscriberDoesNotBlock(t *testing.T) {
	t.Parallel()
	hub := sse.NewHub()
	defer hub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 遅い購読者 (チャネルを読まない)
	_, _ = hub.Subscribe(ctx)

	// 速い購読者
	fastCh, _ := hub.Subscribe(ctx)

	// 大量に Publish しても速い購読者がブロックされないことを確認
	// (遅い購読者のバッファが溢れたら drop する)
	for i := 0; i < 100; i++ {
		hub.Publish(sse.SSEMessage{Kind: "event-published"})
	}

	// 速い購読者は少なくとも 1 件受信できる
	select {
	case <-fastCh:
		// 期待通り
	case <-ctx.Done():
		t.Fatal("タイムアウト: 遅い購読者がいても速い購読者が受信できるはず")
	}
}

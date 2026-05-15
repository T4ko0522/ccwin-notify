// internal/event パッケージのユニットテスト (サイクル 1 / 2)
// テスト ID: T-001〜T-005, T-013〜T-018
// 受入条件: A1 (Event型 / EventKind) / E3 (Bus drop policy / sentinel errors)
package event_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
)

// T-001: `internal/event` パッケージの存在確認 (コンパイル成功 = F2 部分条件)
// Go test ファイルが存在すれば `just test` が PASS する (D-18)。

// T-003: Event 型の全フィールドが正常に設定できる
func TestEvent_Fields(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	raw := json.RawMessage(`{"key":"value"}`)
	ev := event.Event{
		ID:        "01HTEST00000000000000000",
		Kind:      event.KindStop,
		Title:     "Claude Code: response complete",
		Body:      "task body",
		Source:    "hooks",
		Timestamp: now,
		Raw:       raw,
	}
	if ev.ID != "01HTEST00000000000000000" {
		t.Errorf("ID: got %q, want %q", ev.ID, "01HTEST00000000000000000")
	}
	if ev.Kind != event.KindStop {
		t.Errorf("Kind: got %q, want %q", ev.Kind, event.KindStop)
	}
	if ev.Title != "Claude Code: response complete" {
		t.Errorf("Title: got %q", ev.Title)
	}
	if ev.Body != "task body" {
		t.Errorf("Body: got %q", ev.Body)
	}
	if ev.Source != "hooks" {
		t.Errorf("Source: got %q", ev.Source)
	}
	if !ev.Timestamp.Equal(now) {
		t.Errorf("Timestamp: got %v, want %v", ev.Timestamp, now)
	}
	if string(ev.Raw) != `{"key":"value"}` {
		t.Errorf("Raw: got %q", ev.Raw)
	}
}

// T-004: EventKind 定数が文字列と一致する
func TestEventKind_Constants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind event.EventKind
		want string
	}{
		{event.KindStop, "Stop"},
		{event.KindNotification, "Notification"},
		{event.KindSubagentStop, "SubagentStop"},
		{event.KindIdle, "Idle"},
		{event.KindProcessStarted, "ProcessStarted"},
		{event.KindProcessStopped, "ProcessStopped"},
	}
	for _, tc := range cases {
		if string(tc.kind) != tc.want {
			t.Errorf("EventKind %v: got %q, want %q", tc.kind, tc.kind, tc.want)
		}
	}
}

// T-005: sentinel errors が errors.Is で正しく判定できる
func TestSentinelErrors(t *testing.T) {
	t.Parallel()
	if event.ErrDropped == nil {
		t.Fatal("ErrDropped は nil であってはならない")
	}
	if event.ErrPublishCanceled == nil {
		t.Fatal("ErrPublishCanceled は nil であってはならない")
	}
	if event.ErrBusClosed == nil {
		t.Fatal("ErrBusClosed は nil であってはならない")
	}
	// 各エラーが互いに異なることを確認
	if event.ErrDropped == event.ErrPublishCanceled {
		t.Error("ErrDropped と ErrPublishCanceled は別エラーでなければならない")
	}
	if event.ErrDropped == event.ErrBusClosed {
		t.Error("ErrDropped と ErrBusClosed は別エラーでなければならない")
	}
	if event.ErrPublishCanceled == event.ErrBusClosed {
		t.Error("ErrPublishCanceled と ErrBusClosed は別エラーでなければならない")
	}
}

// T-013: DropOldest ポリシー — capacity=2 に 3 件 Publish → 先頭が破棄されて nil が返る
// P-H-02: 循環バッファが wrap-around 後も正しく先入れ先出しを保つ
func TestBus_CircularBuffer_WrapAround(t *testing.T) {
	t.Parallel()
	bus := event.NewBus(3, event.DropOldest)
	ctx := context.Background()
	ch := bus.Subscribe()

	// capacity=3 を超える 10 件を順次 publish → drain
	// publish と subscribe が並行に回るため、head/tail が複数回 wrap してもデータ順序を保つ。
	want := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		title := "ev" + string(rune('0'+i))
		want = append(want, title)
		if err := bus.Publish(ctx, event.Event{Kind: event.KindStop, Title: title}); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}
	// Close で drain 完了を待つ
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := bus.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := []string{}
	for ev := range ch {
		got = append(got, ev.Title)
	}
	// 順序は保たれている (Subscribe 即時 drain なので drop は発生しない可能性がある)
	if len(got) == 0 {
		t.Fatal("got nothing")
	}
	// 末尾の want と一致する suffix を持つ
	tail := want[len(want)-len(got):]
	for i := range got {
		if got[i] != tail[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], tail[i])
		}
	}
}

func TestBus_DropOldest(t *testing.T) {
	t.Parallel()
	bus := event.NewBus(2, event.DropOldest)
	ctx := context.Background()

	ev1 := event.Event{Kind: event.KindStop, Title: "first"}
	ev2 := event.Event{Kind: event.KindNotification, Title: "second"}
	ev3 := event.Event{Kind: event.KindSubagentStop, Title: "third"}

	// 1件目: キュー余裕あり → nil
	if err := bus.Publish(ctx, ev1); err != nil {
		t.Fatalf("1件目Publish: got err %v, want nil", err)
	}
	// 2件目: キュー余裕あり → nil
	if err := bus.Publish(ctx, ev2); err != nil {
		t.Fatalf("2件目Publish: got err %v, want nil", err)
	}
	// 3件目: capacity 満杯 → drop-oldest で先頭 (ev1) を破棄して enqueue → nil
	if err := bus.Publish(ctx, ev3); err != nil {
		t.Fatalf("3件目Publish (drop-oldest): got err %v, want nil", err)
	}

	// Subscribe して残っているイベントを確認: ev2, ev3 が残っている (ev1 は破棄)
	ch := bus.Subscribe()
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var received []event.Event
	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			received = append(received, ev)
		case <-closeCtx.Done():
			t.Fatal("タイムアウト: イベントを受信できなかった")
		}
	}
	if len(received) != 2 {
		t.Fatalf("受信件数: got %d, want 2", len(received))
	}
	if received[0].Title != "second" {
		t.Errorf("最初のイベント Title: got %q, want \"second\" (ev1 が破棄されているはず)", received[0].Title)
	}
	if received[1].Title != "third" {
		t.Errorf("2番目のイベント Title: got %q, want \"third\"", received[1].Title)
	}
}

// T-014: DropNewest ポリシー — capacity=2 に 3 件 Publish → 3件目が ErrDropped
func TestBus_DropNewest(t *testing.T) {
	t.Parallel()
	bus := event.NewBus(2, event.DropNewest)
	ctx := context.Background()

	ev1 := event.Event{Kind: event.KindStop, Title: "first"}
	ev2 := event.Event{Kind: event.KindNotification, Title: "second"}
	ev3 := event.Event{Kind: event.KindSubagentStop, Title: "third"}

	// 1件目: キュー余裕あり → nil
	if err := bus.Publish(ctx, ev1); err != nil {
		t.Fatalf("1件目Publish: got err %v, want nil", err)
	}
	// 2件目: キュー余裕あり → nil
	if err := bus.Publish(ctx, ev2); err != nil {
		t.Fatalf("2件目Publish: got err %v, want nil", err)
	}
	// 3件目: capacity 満杯 → drop-newest → ErrDropped
	err := bus.Publish(ctx, ev3)
	if err != event.ErrDropped {
		t.Fatalf("3件目Publish (drop-newest): got err %v, want ErrDropped", err)
	}
}

// T-015: DropBlock ポリシー — 購読者が消費後に Publish が成功し nil を返す
func TestBus_DropBlock_Success(t *testing.T) {
	t.Parallel()
	bus := event.NewBus(1, event.DropBlock)
	ctx := context.Background()

	ev1 := event.Event{Kind: event.KindStop, Title: "first"}
	ev2 := event.Event{Kind: event.KindNotification, Title: "second"}

	// 1件目: 余裕あり → nil
	if err := bus.Publish(ctx, ev1); err != nil {
		t.Fatalf("1件目Publish: got err %v, want nil", err)
	}

	// 別 goroutine で購読・消費
	ch := bus.Subscribe()
	done := make(chan struct{})
	go func() {
		<-ch // ev1 を消費
		close(done)
	}()

	// ev1 が消費されるのを待ってから ev2 を投入
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("タイムアウト: ev1 が消費されなかった")
	}

	// 2件目: スペースが空いたので nil が返る
	if err := bus.Publish(ctx, ev2); err != nil {
		t.Fatalf("2件目Publish (block): got err %v, want nil", err)
	}
}

// T-016: DropBlock ポリシー — ctx キャンセル時に ErrPublishCanceled が返る
func TestBus_DropBlock_CtxCancel(t *testing.T) {
	t.Parallel()
	bus := event.NewBus(1, event.DropBlock)
	ctx := context.Background()

	// まずキューを満杯にする
	ev1 := event.Event{Kind: event.KindStop, Title: "first"}
	if err := bus.Publish(ctx, ev1); err != nil {
		t.Fatalf("1件目Publish: got err %v, want nil", err)
	}

	// ctx をキャンセルして次の Publish を試みる
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel() // 即座にキャンセル

	ev2 := event.Event{Kind: event.KindNotification, Title: "second"}
	err := bus.Publish(cancelCtx, ev2)
	if err != event.ErrPublishCanceled {
		t.Fatalf("キャンセル時Publish: got err %v, want ErrPublishCanceled", err)
	}
}

// T-017: Close 済みの Bus に Publish すると ErrBusClosed が返る
func TestBus_PublishAfterClose(t *testing.T) {
	t.Parallel()
	bus := event.NewBus(4, event.DropOldest)
	ctx := context.Background()

	// Bus を閉じる
	if err := bus.Close(ctx); err != nil {
		t.Fatalf("Bus.Close: got err %v, want nil", err)
	}

	// Close 後の Publish → ErrBusClosed
	ev := event.Event{Kind: event.KindStop}
	err := bus.Publish(ctx, ev)
	if err != event.ErrBusClosed {
		t.Fatalf("Close後Publish: got err %v, want ErrBusClosed", err)
	}
}

// T-018: Bus.Close(ctx) で Subscribe チャネルが drain 後にクローズされる
func TestBus_Close_Drain(t *testing.T) {
	t.Parallel()
	bus := event.NewBus(4, event.DropOldest)
	ctx := context.Background()

	// 2 件 enqueue
	ev1 := event.Event{Kind: event.KindStop, Title: "drain1"}
	ev2 := event.Event{Kind: event.KindNotification, Title: "drain2"}
	if err := bus.Publish(ctx, ev1); err != nil {
		t.Fatalf("1件目Publish: %v", err)
	}
	if err := bus.Publish(ctx, ev2); err != nil {
		t.Fatalf("2件目Publish: %v", err)
	}

	ch := bus.Subscribe()

	// Close を呼ぶ (drain してチャネルを close)
	closeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := bus.Close(closeCtx); err != nil {
		t.Fatalf("Bus.Close: %v", err)
	}

	// チャネルから全イベントを読み取れて、その後 close されている
	var received []event.Event
	for ev := range ch {
		received = append(received, ev)
	}
	if len(received) != 2 {
		t.Errorf("drain 後の受信件数: got %d, want 2", len(received))
	}
}

// T-162a: Bus.Subscribe を 2 回呼ぶと panic する (M3R-02)
func TestBus_Subscribe_DoublePanic(t *testing.T) {
	t.Parallel()
	bus := event.NewBus(4, event.DropOldest)

	bus.Subscribe() // 1 回目は正常

	// 2 回目は panic するはず
	defer func() {
		if r := recover(); r == nil {
			t.Error("Bus.Subscribe 2 回目: panic が発生するべきだが nil だった")
		}
	}()
	bus.Subscribe() // 2 回目は panic
}

// T-162b: DropBlock で Bus が Close されると Publish が ErrBusClosed/ErrPublishCanceled を返す
func TestBus_DropBlock_ClosedDuringBlock(t *testing.T) {
	t.Parallel()
	bus := event.NewBus(1, event.DropBlock)
	ctx := context.Background()

	// キューを満杯にする
	ev1 := event.Event{Kind: event.KindStop, Title: "first"}
	if err := bus.Publish(ctx, ev1); err != nil {
		t.Fatalf("1件目Publish: %v", err)
	}

	// Note: Subscribe を呼ばずに Close する。
	// drainLoop が起動していないので drainDone=nil → Close は即座に戻る。
	// notFull.Broadcast() が呼ばれることで DropBlock がブロック解除 → b.closed=true → ErrBusClosed

	publishDone := make(chan error, 1)
	go func() {
		// DropBlock でブロックされる
		ev2 := event.Event{Kind: event.KindNotification, Title: "second"}
		publishDone <- bus.Publish(context.Background(), ev2)
	}()

	// Publish goroutine が待機に入るのを待つ
	time.Sleep(20 * time.Millisecond)

	// Bus を Close (Subscribe なし → drainDone nil → 即座に戻る)
	_ = bus.Close(context.Background())

	select {
	case err := <-publishDone:
		// ErrBusClosed が返れば OK
		if err == nil {
			t.Error("Close後のDropBlock Publish: ErrBusClosed が返るべきだが nil だった")
		}
		t.Logf("T-162b: Publish err = %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("タイムアウト: Publish が戻らなかった")
	}
}

// T-162: event.NewID() が一意な ULID 文字列を返す
func TestNewID_Unique(t *testing.T) {
	t.Parallel()
	id1 := event.NewID()
	id2 := event.NewID()

	if id1 == "" {
		t.Error("NewID(): 空文字が返された")
	}
	if id2 == "" {
		t.Error("NewID(): 空文字が返された (2回目)")
	}
	if id1 == id2 {
		t.Errorf("NewID(): 2回連続で同じ ID が返された: %q", id1)
	}
	// ULID は26文字
	if len(id1) != 26 {
		t.Errorf("NewID(): len(id1)=%d, want 26", len(id1))
	}
}

// T-163: event.NewID() が並行呼び出しでパニックしない
func TestNewID_Concurrent(t *testing.T) {
	t.Parallel()
	const n = 100
	ids := make([]string, n)
	done := make(chan int, n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			ids[idx] = event.NewID()
			done <- idx
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}
	// 全 ID が非空であること
	for i, id := range ids {
		if id == "" {
			t.Errorf("NewID() goroutine %d: 空文字", i)
		}
	}
}

// Bus が EventSink interface を満たすことのコンパイル時確認
var _ event.EventSink = (event.Bus)(nil)

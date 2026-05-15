// internal/daemon パッケージの Dispatcher テスト (サイクル 8)
// テスト ID: T-054, T-056〜T-065, T-076, T-077
// 受入条件: B3 / E1 / C4 / H2 / M-WG-RACE / M-TEA-CMD — ライフサイクル + goroutine 管理
package daemon_test

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/daemon"
	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/ipc/sse"
	"github.com/t4ko0522/ccwin-notify/internal/notifier"
)

// panicNotifier: Notify を呼ぶと必ず panic する Notifier (B3 / E1 テスト用)
type panicNotifier struct{ name string }

func (p *panicNotifier) Name() string                 { return p.name }
func (p *panicNotifier) Wants(_ event.EventKind) bool { return true }
func (p *panicNotifier) Notify(_ context.Context, _ event.Event) error {
	panic("intentional panic for testing")
}

// slowNotifier: Notify がブロックする Notifier (graceful shutdown テスト用)
type slowNotifier struct {
	name     string
	started  chan struct{}
	release  chan struct{}
	mu       sync.Mutex
	notified int
}

func newSlowNotifier(name string) *slowNotifier {
	return &slowNotifier{
		name:    name,
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (s *slowNotifier) Name() string                 { return s.name }
func (s *slowNotifier) Wants(_ event.EventKind) bool { return true }
func (s *slowNotifier) Notify(ctx context.Context, _ event.Event) error {
	s.started <- struct{}{} // 開始通知
	select {
	case <-s.release: // 解放されるまで待つ
		s.mu.Lock()
		s.notified++
		s.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// countNotifier: Notify 呼出回数を記録する Notifier (WaitForN チャネル対応)
type countNotifier struct {
	name    string
	count   atomic.Int32
	mu      sync.Mutex
	target  int32
	reached chan struct{}
	once    sync.Once
}

func (c *countNotifier) Name() string                 { return c.name }
func (c *countNotifier) Wants(_ event.EventKind) bool { return true }
func (c *countNotifier) Notify(_ context.Context, _ event.Event) error {
	n := c.count.Add(1)
	c.mu.Lock()
	tgt := c.target
	ch := c.reached
	c.mu.Unlock()
	if ch != nil && n >= tgt {
		c.once.Do(func() { close(ch) })
	}
	return nil
}

// WaitForN は Notify が n 件以上呼ばれたら close されるチャネルを返す。
// スレッドセーフ。
func (c *countNotifier) WaitForN(n int32) <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan struct{})
	c.target = n
	c.reached = ch
	c.once = sync.Once{}
	// 既に n 件達している場合は即 close
	if c.count.Load() >= n {
		c.once.Do(func() { close(ch) })
	}
	return ch
}

// テスト用の最小設定
var testDispatcherCfg = daemon.DispatcherConfig{
	MaxConcurrentPerNotifier: 4,
	NotifierTimeout:          3 * time.Second,
}

// テスト用 Bus を作成するヘルパー
func newTestBus() event.Bus {
	return event.NewBus(256, event.DropOldest)
}

// テスト用 SSE Hub を作成するヘルパー
func newTestSSEHub() *sse.Hub {
	return sse.NewHub()
}

// テスト用 Logger (無音)
var testLogger = slog.Default()

// T-054 / B3 / E1: panic する Notifier を混ぜても他の Notifier が呼ばれ WG.Done が呼ばれる
func TestDispatcher_PanicIsolation(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()

	cnt := &countNotifier{name: "normal"}
	panicN := &panicNotifier{name: "panicker"}

	acceptCtx, cancelAccept := context.WithCancel(context.Background())
	defer cancelAccept()

	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{panicN, cnt}, hub, testDispatcherCfg, testLogger)
	go d.Run()

	ev := event.Event{Kind: event.KindStop, Title: "test"}
	if err := bus.Publish(context.Background(), ev); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// normal Notifier が少なくとも 1 件呼ばれるのをチャネルで待つ
	waitCh := cnt.WaitForN(1)
	select {
	case <-waitCh:
		// 期待通り
	case <-time.After(3 * time.Second):
		t.Error("panic Notifier があっても normal Notifier が呼ばれるべきだが呼ばれなかった")
	}

	// Dispatcher を正常に Close できる = WG が正しく Done されている
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cancelAccept()

	// Bus を close して Run() を抜けさせる
	if err := bus.Close(context.Background()); err != nil {
		t.Fatalf("Bus.Close: %v", err)
	}

	if err := d.Close(shutdownCtx); err != nil {
		t.Errorf("Dispatcher.Close: %v (panic から recover できていない可能性)", err)
	}
}

// T-059 / M-WG-RACE: Close 後の Submit が ErrDispatcherClosing を返す
func TestDispatcher_SubmitAfterClose(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()

	cnt := &countNotifier{name: "cnt"}
	acceptCtx, cancelAccept := context.WithCancel(context.Background())
	defer cancelAccept()

	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{cnt}, hub, testDispatcherCfg, testLogger)
	go d.Run()

	// Bus を close して Run() を抜けさせる
	if err := bus.Close(context.Background()); err != nil {
		t.Fatalf("Bus.Close: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := d.Close(shutdownCtx); err != nil {
		t.Fatalf("Dispatcher.Close: %v", err)
	}

	// Close 後の Submit → ErrDispatcherClosing
	ev := event.Event{Kind: event.KindStop}
	err := d.Submit(ev)
	if err != daemon.ErrDispatcherClosing {
		t.Errorf("Close後Submit: got %v, want ErrDispatcherClosing", err)
	}
}

// T-058 / M-WG-RACE: Submit 直後に Close を並行呼出して -race で data race なし
// このテストは -race フラグ付きで実行することで race detector が検出するはず
func TestDispatcher_SubmitAndClose_Race(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()

	cnt := &countNotifier{name: "cnt"}
	acceptCtx, cancelAccept := context.WithCancel(context.Background())

	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{cnt}, hub, testDispatcherCfg, testLogger)
	go d.Run()

	ev := event.Event{Kind: event.KindStop, Title: "race test"}

	// Submit と Close を並行実行して race detector でチェック
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		// 複数回 Submit を試みる (Gosched で他 goroutine に実行機会を与える)
		for i := 0; i < 10; i++ {
			_ = d.Submit(ev)
			runtime.Gosched()
		}
	}()

	go func() {
		defer wg.Done()
		// Submit と Close が並行になるよう少し待つ
		for i := 0; i < 5; i++ {
			runtime.Gosched()
		}
		cancelAccept()
		_ = bus.Close(context.Background())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.Close(shutdownCtx)
	}()

	wg.Wait()
	// panic や race がなければ成功
}

// T-062 / E3: submitInternal の sem 取得が acceptCtx を監視し cancelAccept で即 unwind
func TestDispatcher_SubmitInternal_AcceptCtxCancel(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()

	// MaxConcurrentPerNotifier=1 かつ sem を即座に占有するスロー Notifier
	slow := newSlowNotifier("slow")

	acceptCtx, cancelAccept := context.WithCancel(context.Background())

	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{slow}, hub,
		daemon.DispatcherConfig{
			MaxConcurrentPerNotifier: 1,
			NotifierTimeout:          5 * time.Second,
		}, testLogger)
	go d.Run()

	// sem を占有させる
	ev1 := event.Event{Kind: event.KindStop, Title: "occupy-sem"}
	if err := bus.Publish(context.Background(), ev1); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// slow が started するまで待つ
	select {
	case <-slow.started:
	case <-time.After(2 * time.Second):
		t.Fatal("slow Notifier が started しなかった")
	}

	// 今度は acceptCtx をキャンセルしてから 2 件目を Submit
	cancelAccept()
	runtime.Gosched() // acceptCtx cancel が伝播するよう goroutine に実行機会を与える

	ev2 := event.Event{Kind: event.KindStop, Title: "should-fail"}
	err := d.Submit(ev2)
	// acceptCtx cancel → ErrDispatcherClosing か context.Canceled のいずれか
	if err == nil {
		t.Error("acceptCtxキャンセル後の Submit: error が返るべきだが nil だった")
	}

	// slow を解放してクリーンアップ
	close(slow.release)
	if err := bus.Close(context.Background()); err != nil {
		t.Logf("Bus.Close: %v", err)
	}
}

// T-063 / C4: Dispatcher.Close(shutdownCtx) が in-flight 完走を待ち、timeout 後は dispatchCtx をキャンセル
func TestDispatcher_Close_WaitsForInFlight(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()

	slow := newSlowNotifier("slow")
	cnt := &countNotifier{name: "cnt"}

	acceptCtx, cancelAccept := context.WithCancel(context.Background())

	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{slow, cnt}, hub, testDispatcherCfg, testLogger)
	go d.Run()

	ev := event.Event{Kind: event.KindStop, Title: "inflight"}
	if err := bus.Publish(context.Background(), ev); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// slow が started するまで待つ
	select {
	case <-slow.started:
	case <-time.After(2 * time.Second):
		t.Fatal("slow Notifier が started しなかった")
	}

	cancelAccept()
	if err := bus.Close(context.Background()); err != nil {
		t.Logf("Bus.Close: %v", err)
	}

	// 別 goroutine で slow を解放 (少し遅延させる)
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(slow.release)
	}()

	// shutdownCtx は十分な余裕を持って in-flight の完走を待つ
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := d.Close(shutdownCtx); err != nil {
		t.Errorf("Close: got %v, want nil (in-flight 完走を待つはず)", err)
	}
}

// T-057: shutdownCtx タイムアウトで in-flight 未完走 → ErrDispatcherClosing が返る
func TestDispatcher_Close_Timeout(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()

	// 解放しない slow Notifier
	slow := newSlowNotifier("slow-never-released")
	acceptCtx, cancelAccept := context.WithCancel(context.Background())

	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{slow}, hub, testDispatcherCfg, testLogger)
	go d.Run()

	ev := event.Event{Kind: event.KindStop, Title: "inflight"}
	if err := bus.Publish(context.Background(), ev); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// started まで待つ
	select {
	case <-slow.started:
	case <-time.After(2 * time.Second):
		t.Fatal("slow started しなかった")
	}

	cancelAccept()
	if err := bus.Close(context.Background()); err != nil {
		t.Logf("Bus.Close: %v", err)
	}

	// 非常に短い timeout で Close → タイムアウトエラー
	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := d.Close(shortCtx)
	if err == nil {
		t.Error("タイムアウト: error が返るべきだが nil だった")
	}
	// タイムアウト後は slow が dispatchCtx cancel で終了するのを待つ
	select {
	case <-slow.started:
		// 次の started は来ない
	case <-time.After(1 * time.Second):
		// タイムアウト後にcloseしたのでOK
	}
}

// T-064: SubmitAndCollect の out chan が必ず close されて caller の for range が永久待ちにならない
func TestDispatcher_SubmitAndCollect_ChanAlwaysClosed(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()

	cnt := &countNotifier{name: "cnt"}
	acceptCtx, cancelAccept := context.WithCancel(context.Background())
	defer cancelAccept()

	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{cnt}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.Close(shutdownCtx)
	}()

	ev := event.Event{Kind: event.KindStop, Title: "collect-test"}
	out, err := d.SubmitAndCollect(ev)
	if err != nil {
		t.Fatalf("SubmitAndCollect: %v", err)
	}
	if out == nil {
		t.Fatal("SubmitAndCollect: out は nil であってはならない")
	}

	// for range で全件読む。永久待ちにならないことを timeout で確認
	done := make(chan struct{})
	go func() {
		for range out {
			// 全件読む
		}
		close(done)
	}()

	select {
	case <-done:
		// 期待通り: chan が close された
	case <-time.After(5 * time.Second):
		t.Fatal("SubmitAndCollect: out chan が close されず永久待ちになった")
	}
}

// maskedNotifier: 特定 kind のみ Wants=true を返す Notifier (filterFor テスト用)
type maskedNotifier struct {
	name     string
	allowed  event.EventKind
	notified atomic.Int32
}

func (m *maskedNotifier) Name() string                 { return m.name }
func (m *maskedNotifier) Wants(k event.EventKind) bool { return k == m.allowed }
func (m *maskedNotifier) Notify(_ context.Context, _ event.Event) error {
	m.notified.Add(1)
	return nil
}

// T-080 / B4: filterFor — Wants=false の Notifier は submit で呼ばれず、collect=false で wg.Add しない
func TestDispatcher_FilterFor_SkipsWantsFalse(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()

	// KindNotification を許可する Notifier (KindStop イベントには Wants=false)
	masked := &maskedNotifier{name: "masked", allowed: event.KindNotification}
	cnt := &countNotifier{name: "normal"}

	acceptCtx, cancelAccept := context.WithCancel(context.Background())
	defer cancelAccept()

	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{masked, cnt}, hub, testDispatcherCfg, testLogger)
	go d.Run()

	// KindStop イベントを送信 → masked は Wants=false なので呼ばれない
	ev := event.Event{Kind: event.KindStop, Title: "filter-test"}
	if err := bus.Publish(context.Background(), ev); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// cnt が呼ばれるのをチャネルで待つ
	waitCh := cnt.WaitForN(1)
	select {
	case <-waitCh:
		// 期待通り
	case <-time.After(3 * time.Second):
		t.Error("normal Notifier (Wants=true) が呼ばれなかった")
	}
	if masked.notified.Load() != 0 {
		t.Errorf("masked Notifier (Wants=false) が呼ばれてはならないが %d 回呼ばれた", masked.notified.Load())
	}

	_ = bus.Close(context.Background())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := d.Close(shutdownCtx); err != nil {
		t.Errorf("Dispatcher.Close: %v", err)
	}
}

// T-081 / B4: filterFor — SubmitAndCollect で Wants=false の Notifier が ErrKindFiltered で結果リストに入る
func TestDispatcher_FilterFor_CollectIncludesFiltered(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()

	// KindNotification を許可する Notifier (KindStop には Wants=false)
	masked := &maskedNotifier{name: "masked", allowed: event.KindNotification}
	cnt := &countNotifier{name: "normal"}

	acceptCtx, cancelAccept := context.WithCancel(context.Background())
	defer cancelAccept()

	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{masked, cnt}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.Close(shutdownCtx)
	}()

	// KindStop を SubmitAndCollect
	ev := event.Event{Kind: event.KindStop, Title: "collect-filter-test"}
	out, err := d.SubmitAndCollect(ev)
	if err != nil {
		t.Fatalf("SubmitAndCollect: %v", err)
	}

	results := make(map[string]sse.DispatchResult)
	for r := range out {
		results[r.Notifier] = r
	}

	// 2 件 (masked + normal) が results に含まれるはず
	if len(results) != 2 {
		t.Errorf("results 件数: got %d, want 2 (masked + normal)", len(results))
	}

	// masked は kind_filtered で OK=false
	maskedResult, ok := results["masked"]
	if !ok {
		t.Error("masked Notifier の結果が results に含まれていない")
	} else {
		if maskedResult.OK {
			t.Error("masked Notifier の result.OK は false であるべき")
		}
		if maskedResult.Err != daemon.ErrKindFiltered {
			t.Errorf("masked Notifier の result.Err: got %v, want ErrKindFiltered", maskedResult.Err)
		}
	}

	// normal は OK=true
	normalResult, ok := results["normal"]
	if !ok {
		t.Error("normal Notifier の結果が results に含まれていない")
	} else if !normalResult.OK {
		t.Errorf("normal Notifier の result.OK は true であるべき: err=%v", normalResult.Err)
	}

	// out chan が close されていることを確認 (for range が完了した時点で確認済み)
}

// T-082 / B4: filterFor — 全 Notifier が Wants=false の場合も out chan が close される
func TestDispatcher_FilterFor_AllFiltered_ChanCloses(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()

	// KindNotification のみ許可 (KindStop には全員 Wants=false)
	masked1 := &maskedNotifier{name: "m1", allowed: event.KindNotification}
	masked2 := &maskedNotifier{name: "m2", allowed: event.KindNotification}

	acceptCtx, cancelAccept := context.WithCancel(context.Background())
	defer cancelAccept()

	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{masked1, masked2}, hub, testDispatcherCfg, testLogger)
	go d.Run()
	defer func() {
		_ = bus.Close(context.Background())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.Close(shutdownCtx)
	}()

	ev := event.Event{Kind: event.KindStop, Title: "all-filtered"}
	out, err := d.SubmitAndCollect(ev)
	if err != nil {
		t.Fatalf("SubmitAndCollect: %v", err)
	}

	done := make(chan struct{})
	go func() {
		for range out {
		}
		close(done)
	}()

	select {
	case <-done:
		// 期待通り: 全員 filtered でも out chan は close される
	case <-time.After(3 * time.Second):
		t.Fatal("全 Notifier が filtered でも out chan が close されなかった")
	}
}

// T-076 / B3: panic した worker が SSE Hub に dispatch-result(OK=false) を送出する
func TestDispatcher_PanicWorker_SSEPublish(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()

	panicN := &panicNotifier{name: "panicker"}
	acceptCtx, cancelAccept := context.WithCancel(context.Background())
	defer cancelAccept()

	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{panicN}, hub, testDispatcherCfg, testLogger)
	go d.Run()

	// SSE Hub を購読
	subCtx, cancelSub := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelSub()
	sseC, _ := hub.Subscribe(subCtx)

	ev := event.Event{Kind: event.KindStop, Title: "panic-test"}
	if err := bus.Publish(context.Background(), ev); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// SSE Hub から dispatch-result を受信する ("event-published" が先に来る場合はスキップ)
	for {
		select {
		case msg := <-sseC:
			if msg.Kind != "dispatch-result" {
				continue
			}
			result, ok := msg.Payload.(sse.DispatchResult)
			if !ok {
				t.Fatalf("payload の型が DispatchResult でない: %T", msg.Payload)
			}
			if result.OK {
				t.Error("panic した Notifier の result.OK は false であるべき")
			}
			if result.Notifier != "panicker" {
				t.Errorf("result.Notifier: got %q, want \"panicker\"", result.Notifier)
			}
			return
		case <-subCtx.Done():
			t.Fatal("タイムアウト: SSE Hub から dispatch-result が受信されなかった")
			return
		}
	}
}

// T-083 / B3R-02: cancelAccept 後に Publish されたイベントが確実に dispatch される (shutdown drain)
// 手順: cancelAccept() → bus.Publish() → bus.Close() → count==1 をチャネルで待つ → d.Close()
//
// NOTE: bus.Close() は drainLoop 完了 (subCh への転送完了) を待つが、Run() が subCh から
// 読み取り dispatch する前に d.Close() が完了する可能性があるため、WaitForN で dispatch
// 完了を確認してから d.Close() を呼ぶことで決定的に検証する。
func TestDispatcher_ShutdownDrain(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	hub := newTestSSEHub()
	defer hub.Close()

	cnt := &countNotifier{name: "cnt"}
	acceptCtx, cancelAccept := context.WithCancel(context.Background())

	d := daemon.NewDispatcher(acceptCtx, bus, []notifier.Notifier{cnt}, hub, testDispatcherCfg, testLogger)
	go d.Run()

	// acceptCtx を先にキャンセル (shutdown 開始を模倣)
	cancelAccept()

	// Bus にイベントを投入 (acceptCtx キャンセル後でも Bus はまだ open)
	ev := event.Event{Kind: event.KindStop, Title: "drain-test"}
	if err := bus.Publish(context.Background(), ev); err != nil {
		t.Fatalf("Publish (post-cancelAccept): %v", err)
	}

	// Bus を close して Run() に EOF を通知 (drainLoop が ev を subCh に転送してから終了)
	if err := bus.Close(context.Background()); err != nil {
		t.Fatalf("Bus.Close: %v", err)
	}

	// Run() が ev を dispatch したことを WaitForN(1) で確認してから Close を呼ぶ
	// (bus.Close() だけでは Run() の dispatch 完了は保証されない)
	waitCh := cnt.WaitForN(1)
	select {
	case <-waitCh:
		// B3R-02: cancelAccept 後のイベントが drain されて dispatch された
	case <-time.After(5 * time.Second):
		t.Errorf("shutdown drain: cancelAccept 後のイベントが dispatch されなかった (count=%d)", cnt.count.Load())
	}

	// Dispatcher を close
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := d.Close(shutdownCtx); err != nil {
		t.Fatalf("Dispatcher.Close: %v", err)
	}

	// drain されたのでカウントは 1
	if cnt.count.Load() != 1 {
		t.Errorf("shutdown drain: countNotifier.count = %d, want 1", cnt.count.Load())
	}
}

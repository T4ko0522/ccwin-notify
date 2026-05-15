// Package daemon は ccwin-notify デーモンの本体を提供する。
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/ipc/sse"
	"github.com/t4ko0522/ccwin-notify/internal/notifier"
)

// ErrDispatcherClosing は Close 済みの Dispatcher に Submit した場合に返る。
var ErrDispatcherClosing = errors.New("dispatcher: closing")

// ErrKindFiltered は kind_mask によって Notifier がフィルタされた場合に DispatchResult.Err に設定される。
var ErrKindFiltered = errors.New("kind_filtered")

// DispatcherConfig は NewDispatcher に渡す設定。
type DispatcherConfig struct {
	MaxConcurrentPerNotifier int
	NotifierTimeout          time.Duration
}

// Dispatcher は fan-out + WaitGroup + bounded worker pool を実装する。
type Dispatcher struct {
	// acceptCtx: 外部からの新規 Submit ゲート (cancelAccept で遮断する)。
	// Run() 経由の Bus drain には使わない。
	acceptCtx      context.Context
	dispatchCtx    context.Context
	cancelDispatch context.CancelFunc

	bus       event.Bus
	notifiers []notifier.Notifier
	sem       map[string]chan struct{}

	sseHub  *sse.Hub
	logger  *slog.Logger
	timeout time.Duration

	closingMu sync.Mutex
	closing   bool
	wg        sync.WaitGroup

	// runDone は Run() goroutine が終了したら close される。
	// Close() はこれを待ってから closing=true をセットすることで、
	// Bus からの drain イベントが closing チェックで弾かれないことを保証する (B3R-02)。
	runDone chan struct{}
}

// NewDispatcher は Dispatcher を生成する。
// acceptCtx: 新規入力ゲート (shutdownCtx では無くルートから分岐した専用 ctx)
// dispatchCtx は内部で context.Background() から生成する (C4: graceful shutdown 中に in-flight を完走させる)
func NewDispatcher(
	acceptCtx context.Context,
	bus event.Bus,
	ns []notifier.Notifier,
	sseHub *sse.Hub,
	cfg DispatcherConfig,
	logger *slog.Logger,
) *Dispatcher {
	dispatchCtx, cancelDispatch := context.WithCancel(context.Background())

	maxConcurrent := cfg.MaxConcurrentPerNotifier
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	timeout := cfg.NotifierTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	sem := make(map[string]chan struct{}, len(ns))
	for _, n := range ns {
		sem[n.Name()] = make(chan struct{}, maxConcurrent)
	}

	return &Dispatcher{
		acceptCtx:      acceptCtx,
		dispatchCtx:    dispatchCtx,
		cancelDispatch: cancelDispatch,
		bus:            bus,
		notifiers:      ns,
		sem:            sem,
		sseHub:         sseHub,
		logger:         logger,
		timeout:        timeout,
		runDone:        make(chan struct{}),
	}
}

// Run は Bus からの受信ループ。Bus.Close で Subscribe channel が close されたら抜ける。
// rootCtx は見ない (B2R-01 の本質)。
func (d *Dispatcher) Run() {
	defer close(d.runDone)
	ch := d.bus.Subscribe()
	for ev := range ch {
		// SSE Hub に "event-published" を通知 (TUI ストリーム用 / C-03 修正)
		d.sseHub.Publish(sse.SSEMessage{
			Kind:    "event-published",
			Payload: ev,
		})
		// fromBus=true: acceptCtx キャンセル後でも drain できるようゲートをスキップ (B3R-02 修正)
		if _, err := d.submit(ev, false, true, nil); err != nil {
			d.logger.Warn("Dispatcher.Run: submit failed", "err", err, "event_id", ev.ID)
		}
	}
}

// Submit は通常経路の非同期発火。
func (d *Dispatcher) Submit(ev event.Event) error {
	_, err := d.submit(ev, false, false, nil)
	return err
}

// SubmitAndCollect は POST /v1/test 専用。out chan は必ず close される。
// target_notifier に応じて Notifier をフィルタしたい場合は SubmitAndCollectTargeted を使う。
func (d *Dispatcher) SubmitAndCollect(ev event.Event) (<-chan sse.DispatchResult, error) {
	return d.submit(ev, true, false, nil)
}

// SubmitAndCollectTargeted は POST /v1/test 用 (D-40 / H-04)。
// target が "" or "all" → 全 Notifier に fan-out (SubmitAndCollect と同じ)。
// 特定名 → 一致する Notifier のみ dispatch する。一致しない場合は空 out を即 close する
// (handler 側が事前に 409 で弾く前提)。
func (d *Dispatcher) SubmitAndCollectTargeted(ev event.Event, target string) (<-chan sse.DispatchResult, error) {
	if target == "" || target == "all" {
		return d.submit(ev, true, false, nil)
	}
	var picked []notifier.Notifier
	for _, n := range d.notifiers {
		if n.Name() == target {
			picked = append(picked, n)
			break
		}
	}
	if len(picked) == 0 {
		out := make(chan sse.DispatchResult)
		close(out)
		return out, nil
	}
	return d.submit(ev, true, false, picked)
}

// submit は fan-out ロジックの共通実装。
// collect=true のとき DispatchResult channel を返す (必ず close される)。
// collect=false のとき nil channel と error のみ返す。
// fromBus=true のとき acceptCtx キャンセルおよび closing フラグを無視して drain を優先する (B3R-02 / C4)。
// Close() が runDone を待ってから closing=true をセットするため、fromBus=true のパスが closing=true を
// 見ることはない (不変条件)。
//
// filterFor (B4 / plan §5.3):
// 各 Notifier の Wants(kind) を事前チェックし、false なら:
//   - collect=true のとき: 即座に ok=false, err=ErrKindFiltered を out に送る (結果リストに含める)
//   - collect=false のとき: 単純スキップ (wg.Add しない)
//
// deadlock 回避 (MUST dispatcher-submit-blocks-under-closingmu):
// closingMu 配下では closing チェックと wg.Add のみ行い、ロック解放後に
// goroutine 内で sem を取得する。sem 取得待ちで Close() の closingMu 取得をブロックしない。
func (d *Dispatcher) submit(ev event.Event, collect bool, fromBus bool, notifiersOverride []notifier.Notifier) (<-chan sse.DispatchResult, error) {
	// 対象 Notifier (target_notifier 指定時は呼び出し側で絞ったものを受け取る)
	notifiers := d.notifiers
	if notifiersOverride != nil {
		notifiers = notifiersOverride
	}
	// filterFor: Notifier を wanted / filtered に分類する
	wanted := make([]notifier.Notifier, 0, len(notifiers))
	filtered := make([]notifier.Notifier, 0, len(notifiers))
	for _, n := range notifiers {
		if n.Wants(ev.Kind) {
			wanted = append(wanted, n)
		} else {
			filtered = append(filtered, n)
		}
	}

	var (
		out      chan sse.DispatchResult
		callWG   *sync.WaitGroup
		resultFn func(sse.DispatchResult)
	)

	if collect {
		// out buffer = 全 Notifier 数 (wanted + filtered)
		out = make(chan sse.DispatchResult, len(notifiers)+1)
		callWG = &sync.WaitGroup{}
		resultFn = func(r sse.DispatchResult) {
			out <- r
			callWG.Done()
		}
	}

	d.closingMu.Lock()

	// fromBus=false (外部直接呼び出し) の場合のみ closing / acceptCtx をチェックする。
	// fromBus=true (Run() 経由の Bus drain) では Close() が runDone を待ってから closing=true を
	// セットするため、このパスが closing=true を見ることはない (B3R-02 不変条件)。
	if !fromBus {
		if d.closing {
			d.closingMu.Unlock()
			if collect {
				close(out)
			}
			return out, ErrDispatcherClosing
		}
		if d.acceptCtx.Err() != nil {
			d.closingMu.Unlock()
			if collect {
				close(out)
			}
			return out, fmt.Errorf("%w: %w", ErrDispatcherClosing, d.acceptCtx.Err())
		}
	}

	// wg.Add は closingMu 配下で行い、Close().wg.Wait との race を防ぐ (MUST dispatcher-wg-add-wait-race)
	for range wanted {
		d.wg.Add(1)
	}
	if collect {
		// wanted (goroutine 経由) + filtered (即時送信) の合計を callWG に登録
		callWG.Add(len(wanted) + len(filtered))
	}

	d.closingMu.Unlock()

	// filtered Notifier: 即座に kind_filtered 結果を out に送る (goroutine 不要)
	if collect {
		for _, n := range filtered {
			resultFn(sse.DispatchResult{
				EventID:  ev.ID,
				Notifier: n.Name(),
				OK:       false,
				Err:      ErrKindFiltered,
			})
		}
	}

	// wanted Notifier: sem 取得は goroutine 内で行う (closingMu を保持しない)
	for _, n := range wanted {
		n := n
		go func() {
			semCh := d.sem[n.Name()]
			select {
			case semCh <- struct{}{}:
				// sem 取得成功 → dispatch
				d.dispatchOne(ev, n, semCh, resultFn)
			case <-d.dispatchCtx.Done():
				// dispatchCtx キャンセル (Close タイムアウト時のみ) → sem なしで wg.Done + resultFn
				d.wg.Done()
				if resultFn != nil {
					resultFn(sse.DispatchResult{
						EventID:  ev.ID,
						Notifier: n.Name(),
						OK:       false,
						Err:      d.dispatchCtx.Err(),
					})
				}
			}
		}()
	}

	if collect {
		go func() {
			callWG.Wait()
			close(out)
		}()
	}

	return out, nil
}

// dispatchOne は単一 Notifier への発火を担当するゴルーチン本体。
func (d *Dispatcher) dispatchOne(ev event.Event, n notifier.Notifier, semCh chan struct{}, resultFn func(sse.DispatchResult)) {
	defer d.wg.Done()
	defer func() { <-semCh }()

	ctx, cancel := context.WithTimeout(d.dispatchCtx, d.timeout)
	defer cancel()

	var (
		ok  bool
		err error
	)

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("notifier %s panicked: %v", n.Name(), r)
				d.logger.Error("Dispatcher: notifier panic recovered", "notifier", n.Name(), "panic", r, "event_id", ev.ID)
			}
		}()
		err = n.Notify(ctx, ev)
		if err == nil {
			ok = true
		}
	}()

	result := sse.DispatchResult{
		EventID:  ev.ID,
		Notifier: n.Name(),
		OK:       ok,
		Err:      err,
	}

	// SSE Hub に publish
	d.sseHub.Publish(sse.SSEMessage{
		Kind:    "dispatch-result",
		Payload: result,
	})

	if resultFn != nil {
		resultFn(result)
	}
}

// Close は Run() goroutine の終了を待ってから closing flag を立て wg.Wait を ctx 監視つきで待つ。
// Run() 終了後に closing=true をセットすることで、Bus drain 中のイベントが
// closing チェックで弾かれないことを保証する (B3R-02)。
// ctx タイムアウト後は dispatchCtx をキャンセルして in-flight を強制終了する。
func (d *Dispatcher) Close(ctx context.Context) error {
	// 1) Run() goroutine が Bus を全て drain して終了するまで待つ
	select {
	case <-d.runDone:
	case <-ctx.Done():
		d.cancelDispatch()
		return ctx.Err()
	}

	// 2) Run() 終了後に closing=true をセット (外部からの新規 Submit を遮断)
	d.closingMu.Lock()
	d.closing = true
	d.closingMu.Unlock()

	// 3) in-flight goroutine の完走を待つ
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		d.cancelDispatch()
		return ctx.Err()
	}
}

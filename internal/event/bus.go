package event

import (
	"context"
	"sync"
)

// bus は Bus インターフェースの実装。
// 循環バッファ (固定長配列 + head/tail/count) で GC 圧を低減する (P-H-02)。
type bus struct {
	mu       sync.Mutex
	notEmpty *sync.Cond
	notFull  *sync.Cond

	// buf は固定長 capacity の循環バッファ。slice の reslice は行わない。
	buf      []Event
	head     int // 次に取り出す位置
	tail     int // 次に書き込む位置
	count    int // 現在の要素数 (0..capacity)
	capacity int
	policy   DropPolicy

	closed bool
	subCh  chan Event

	// drainDone は Subscribe が呼ばれたときに設定され、drainLoop 完了時に close される。
	// Subscribe が呼ばれない場合は nil のまま。
	drainDone chan struct{}
}

// NewBus は capacity 付きの Bus を返す。
func NewBus(capacity int, policy DropPolicy) Bus {
	if capacity < 1 {
		capacity = 1
	}
	b := &bus{
		buf:      make([]Event, capacity),
		capacity: capacity,
		policy:   policy,
		subCh:    make(chan Event, capacity),
	}
	b.notEmpty = sync.NewCond(&b.mu)
	b.notFull = sync.NewCond(&b.mu)
	return b
}

// pushLocked は b.mu 保持下で循環バッファに e を追加する。
// count < capacity 前提 (呼び出し側が drop 判定を済ませていること)。
func (b *bus) pushLocked(e Event) {
	b.buf[b.tail] = e
	b.tail = (b.tail + 1) % b.capacity
	b.count++
}

// pushDropOldestLocked は b.mu 保持下で循環バッファに e を追加する。
// 満杯のときは head を進めて最古要素を捨てる (DropOldest)。
func (b *bus) pushDropOldestLocked(e Event) {
	if b.count == b.capacity {
		// 最古要素を上書きしつつ head を進める
		b.buf[b.tail] = e
		b.tail = (b.tail + 1) % b.capacity
		b.head = (b.head + 1) % b.capacity
		return
	}
	b.pushLocked(e)
}

// popLocked は b.mu 保持下で最古要素を取り出す。count > 0 前提。
// 取り出したスロットは zero 値でクリアし、Event 内のポインタを GC 可能にする。
func (b *bus) popLocked() Event {
	e := b.buf[b.head]
	b.buf[b.head] = Event{} // GC 可能化
	b.head = (b.head + 1) % b.capacity
	b.count--
	return e
}

// Publish は Event をキューに追加する。
func (b *bus) Publish(ctx context.Context, e Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return ErrBusClosed
	}

	switch b.policy {
	case DropOldest:
		b.pushDropOldestLocked(e)
		b.notEmpty.Signal()
		return nil

	case DropNewest:
		if b.count >= b.capacity {
			return ErrDropped
		}
		b.pushLocked(e)
		b.notEmpty.Signal()
		return nil

	case DropBlock:
		for b.count >= b.capacity && !b.closed {
			// ctx のキャンセルを監視しながら待機
			b.mu.Unlock()
			select {
			case <-ctx.Done():
				b.mu.Lock()
				return ErrPublishCanceled
			default:
				b.mu.Lock()
				if b.count < b.capacity || b.closed {
					break
				}
				// まだフル: goroutine で ctx.Done 監視しながら cond.Wait
				ctxDone := make(chan struct{})
				go func() {
					select {
					case <-ctx.Done():
						b.notFull.Signal()
					case <-ctxDone:
					}
				}()
				b.notFull.Wait()
				close(ctxDone)
				if ctx.Err() != nil {
					return ErrPublishCanceled
				}
			}
		}
		if b.closed {
			return ErrBusClosed
		}
		if ctx.Err() != nil {
			return ErrPublishCanceled
		}
		b.pushLocked(e)
		b.notEmpty.Signal()
		return nil
	}

	return nil
}

// Subscribe は Event を受け取る channel を返す。single-consumer 前提。
// 呼び出し側は channel が close されるまで読み続けること。
// 2 回目以降の呼び出しは panic する (M3R-02: double close / goroutine leak 対策)。
func (b *bus) Subscribe() <-chan Event {
	b.mu.Lock()
	if b.drainDone != nil {
		b.mu.Unlock()
		panic("event.Bus.Subscribe: already subscribed (single-consumer only)")
	}
	drainDone := make(chan struct{})
	b.drainDone = drainDone
	b.mu.Unlock()

	go func() {
		b.drainLoop()
		close(drainDone)
	}()

	return b.subCh
}

// drainLoop は buf から subCh に Event を転送するバックグラウンドループ。
func (b *bus) drainLoop() {
	for {
		b.mu.Lock()
		for b.count == 0 && !b.closed {
			b.notEmpty.Wait()
		}
		if b.count == 0 {
			// closed かつ buf 空 = 終了
			b.mu.Unlock()
			close(b.subCh)
			return
		}
		ev := b.popLocked()
		b.notFull.Signal()
		b.mu.Unlock()

		b.subCh <- ev
	}
}

// Close は新規 Publish を遮断し、既存 Event を drain してから channel を close する。
func (b *bus) Close(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.notEmpty.Broadcast()
	b.notFull.Broadcast()
	drainDone := b.drainDone
	b.mu.Unlock()

	if drainDone == nil {
		// Subscribe が呼ばれていない場合は drain 待ち不要
		return nil
	}

	select {
	case <-drainDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

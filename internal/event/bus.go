package event

import (
	"context"
	"sync"
)

// bus は Bus インターフェースの実装。
// bounded queue + drop policy + ctx 対応 + single-consumer Subscribe。
type bus struct {
	mu       sync.Mutex
	notEmpty *sync.Cond
	notFull  *sync.Cond

	buf      []Event
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
		capacity: capacity,
		policy:   policy,
		subCh:    make(chan Event, capacity),
	}
	b.notEmpty = sync.NewCond(&b.mu)
	b.notFull = sync.NewCond(&b.mu)
	return b
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
		if len(b.buf) >= b.capacity {
			b.buf = b.buf[1:]
		}
		b.buf = append(b.buf, e)
		b.notEmpty.Signal()
		return nil

	case DropNewest:
		if len(b.buf) >= b.capacity {
			return ErrDropped
		}
		b.buf = append(b.buf, e)
		b.notEmpty.Signal()
		return nil

	case DropBlock:
		for len(b.buf) >= b.capacity && !b.closed {
			// ctx のキャンセルを監視しながら待機
			b.mu.Unlock()
			select {
			case <-ctx.Done():
				b.mu.Lock()
				return ErrPublishCanceled
			default:
				b.mu.Lock()
				if len(b.buf) < b.capacity || b.closed {
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
		b.buf = append(b.buf, e)
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
		for len(b.buf) == 0 && !b.closed {
			b.notEmpty.Wait()
		}
		if len(b.buf) == 0 {
			// closed かつ buf 空 = 終了
			b.mu.Unlock()
			close(b.subCh)
			return
		}
		ev := b.buf[0]
		b.buf = b.buf[1:]
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

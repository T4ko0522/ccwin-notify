package notifier

import (
	"context"
	"sync"

	"github.com/t4ko0522/ccwin-notify/internal/event"
)

// コンパイル時に Notifier interface を満たすことを保証する。
var _ Notifier = (*FakeNotifier)(nil)

// FakeNotifier はテスト用の in-memory 実装。
// Records は Notify が呼ばれるたびに追記される公開フィールド。
type FakeNotifier struct {
	mu      sync.Mutex
	name    string
	Records []event.Event
	Err     error // non-nil なら Notify がこのエラーを返す
	notifyCh chan struct{} // WaitForN 用 (n 件通知後に close)
	waitN    int
	seen     int
}

// NewFakeNotifier は name を持つ FakeNotifier を生成する。
func NewFakeNotifier(name string) *FakeNotifier {
	return &FakeNotifier{name: name}
}

// WaitForN は Notify が n 件以上呼ばれるまで待つチャネルを返す。
// ctx.Done() が先に来た場合はチャネルがcloseされずにフローが終わる (タイムアウト検出は呼び出し側で行う)。
// 呼び出し前に n を設定し、戻りのチャネルをブロッキングに使う。
// スレッドセーフ。
func (f *FakeNotifier) WaitForN(n int) <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan struct{})
	f.waitN = n
	f.notifyCh = ch
	// 既に n 件以上記録されていたら即時 close
	if f.seen >= n {
		close(ch)
		f.notifyCh = nil
	}
	return ch
}

func (f *FakeNotifier) Name() string { return f.name }

func (f *FakeNotifier) Wants(_ event.EventKind) bool { return true }

func (f *FakeNotifier) Notify(ctx context.Context, e event.Event) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if f.Err != nil {
		return f.Err
	}
	f.mu.Lock()
	f.Records = append(f.Records, e)
	f.seen++
	ch := f.notifyCh
	if ch != nil && f.seen >= f.waitN {
		close(ch)
		f.notifyCh = nil
	}
	f.mu.Unlock()
	return nil
}

// Len はスレッドセーフに現在の Records 件数を返す。
func (f *FakeNotifier) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Records)
}

// Get はスレッドセーフに i 番目の Record を返す。i が範囲外の場合は zero value を返す。
func (f *FakeNotifier) Get(i int) (event.Event, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 || i >= len(f.Records) {
		return event.Event{}, false
	}
	return f.Records[i], true
}

// Reset は記録をクリアする。
func (f *FakeNotifier) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Records = nil
	f.seen = 0
	f.notifyCh = nil
}

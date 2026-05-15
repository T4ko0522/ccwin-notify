// Package toast は Windows Toast 通知の Notifier を提供する。
// 実装: toast_real_windows.go が jackmordaunt/go-toast を呼ぶ実体、
// toast_fake_default.go が Fake (環境変数 CCWIN_NOTIFY_USE_FAKE_TOASTER=1 で切替)。
package toast

import (
	"context"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/notifier"
)

// Toaster は Toast 通知を送信するインターフェース (DI 用)。
// Push は ctx で中断可能にする (C-05 対応)。
// toastNotifier.Notify は goroutine を起動せず直接 Push を呼ぶため、
// Push 内の panic は Dispatcher の defer recover で捕捉される。
type Toaster interface {
	Push(ctx context.Context, title, body string) error
}

// PushCall は FakeToaster が記録する呼出情報。
type PushCall struct {
	Title string
	Body  string
}

// FakeToaster はテスト用の in-memory Toaster 実装。
type FakeToaster struct {
	calls []PushCall
}

// NewFakeToaster は FakeToaster を生成する。
func NewFakeToaster() *FakeToaster {
	return &FakeToaster{}
}

// Push は呼出を記録する。
func (f *FakeToaster) Push(_ context.Context, title, body string) error {
	f.calls = append(f.calls, PushCall{Title: title, Body: body})
	return nil
}

// Calls は記録された Push 呼出を返す。
func (f *FakeToaster) Calls() []PushCall {
	return f.calls
}

// BlockingFakeToaster は Push がブロックする Toaster (ctx timeout テスト用)。
type BlockingFakeToaster struct {
	release chan struct{}
}

// NewBlockingFakeToaster は BlockingFakeToaster を生成する。
func NewBlockingFakeToaster() *BlockingFakeToaster {
	return &BlockingFakeToaster{release: make(chan struct{})}
}

// Push はブロックして返らない (ctx タイムアウトで解放される)。
func (b *BlockingFakeToaster) Push(ctx context.Context, title, body string) error {
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// toastNotifier は Notifier インターフェースの実装。
type toastNotifier struct {
	toaster  Toaster
	kindMask map[event.EventKind]bool
}

// コンパイル時 Notifier assertion
var _ notifier.Notifier = (*toastNotifier)(nil)

// New は Toaster を DI した Notifier を返す。
func New(toaster Toaster) notifier.Notifier {
	return &toastNotifier{toaster: toaster, kindMask: nil}
}

// NewWithKindMask は kind_mask 付きの Notifier を返す。
func NewWithKindMask(toaster Toaster, kindMask map[event.EventKind]bool) notifier.Notifier {
	return &toastNotifier{toaster: toaster, kindMask: kindMask}
}

func (n *toastNotifier) Name() string { return "toast" }

func (n *toastNotifier) Wants(kind event.EventKind) bool {
	return len(n.kindMask) == 0 || n.kindMask[kind]
}

func (n *toastNotifier) Notify(ctx context.Context, ev event.Event) error {
	// ctx キャンセル確認
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// kind_mask チェック (空 = 全有効)
	if len(n.kindMask) > 0 && !n.kindMask[ev.Kind] {
		return nil
	}

	// goroutine を使わず直接呼ぶ (C-05: panic が Dispatcher の recover で捕捉されるよう)。
	// Toaster 実装は ctx に従って中断する責務を持つ。
	return n.toaster.Push(ctx, ev.Title, ev.Body)
}

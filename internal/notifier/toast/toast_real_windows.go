//go:build windows && !faketoast

package toast

import (
	"context"
	"fmt"

	goToast "git.sr.ht/~jackmordaunt/go-toast"
)

// realToaster は git.sr.ht/~jackmordaunt/go-toast を使う実 Windows Toast 実装。
// //go:build windows && !faketoast タグが付いており、CI / テスト時は使われない。
type realToaster struct{}

// NewRealToaster は Windows 実装の Toaster を返す。
func NewRealToaster() Toaster {
	return &realToaster{}
}

// NewDefaultToaster は build tag に応じたデフォルト Toaster を返す。
// windows && !faketoast タグ: 実 Windows Toast 実装。
func NewDefaultToaster() Toaster {
	return NewRealToaster()
}

// Push は Windows トースト通知を送信する (ctx でキャンセル可能)。
func (r *realToaster) Push(ctx context.Context, title, body string) error {
	done := make(chan error, 1)
	go func() {
		n := goToast.Notification{
			AppID: "ccwin-notify",
			Title: title,
			Body:  body,
		}
		done <- n.Push()
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("toast: push failed: %w", err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

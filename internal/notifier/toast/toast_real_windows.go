//go:build windows && !faketoast

package toast

import (
	"context"
	"fmt"

	goToast "git.sr.ht/~jackmordaunt/go-toast"
)

// realToaster は git.sr.ht/~jackmordaunt/go-toast を使う実 Windows Toast 実装。
// //go:build windows && !faketoast タグが付いており、CI / テスト時は使われない。
//
// silent=true のとき Toast XML に <audio silent="true"/> を埋め込み、
// Windows 標準の Toast 着信音をミュートする (= sound notifier の WAV と二重に
// 鳴らないようにする目的で orchestrator から立てられる)。
type realToaster struct {
	silent bool
}

// NewRealToaster は Windows 実装の Toaster を返す。
// silent=true のとき Toast の標準通知音をミュートする。
func NewRealToaster(silent bool) Toaster {
	return &realToaster{silent: silent}
}

// NewDefaultToaster は build tag に応じたデフォルト Toaster を返す。
// windows && !faketoast タグ: 実 Windows Toast 実装。
func NewDefaultToaster(silent bool) Toaster {
	return NewRealToaster(silent)
}

// Push は Windows トースト通知を送信する (ctx でキャンセル可能)。
// urgent=true の場合 Duration=Long にしてポップアップ表示を長時間化する。
func (r *realToaster) Push(ctx context.Context, title, body string, urgent bool) error {
	done := make(chan error, 1)
	go func() {
		n := goToast.Notification{
			AppID: "ccwin-notify",
			Title: title,
			Body:  body,
		}
		if r.silent {
			n.Audio = goToast.Silent
		}
		if urgent {
			n.Duration = goToast.Long
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

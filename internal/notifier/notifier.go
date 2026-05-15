package notifier

import (
	"context"

	"github.com/t4ko0522/ccwin-notify/internal/event"
)

// Notifier は単一の通知チャネルを表す。
// A4 を満たすため Event のみを引数に取る。
type Notifier interface {
	Name() string                                    // "toast" / "sound" / "webhook.discord" / ...
	Wants(kind event.EventKind) bool                 // B4: kind_mask フィルタ。空 mask なら常に true
	Notify(ctx context.Context, e event.Event) error // E4: ctx で中断可能
}

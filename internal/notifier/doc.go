// Package notifier は通知バックエンドの共通インターフェースと、テスト用の偽実装を提供する。
//
// # Notifier
//
// [Notifier] インターフェースは 4 つのメソッドを持つ:
//   - Name() string     — ログや SSE のディスパッチ結果に使われる一意な識別名を返す。
//   - Wants(kind event.EventKind) bool — 指定 Kind を処理対象とするか返す。
//     Dispatcher は false の Notifier をフィルタし、kind_mask による絞り込みに使う。
//   - Notify(ctx, event.Event) error — イベントを受け取り通知を実行する。
//     ctx が期限切れになった場合は速やかに処理を打ち切り error を返すこと。
//   - Close() error — リソース解放。デーモン終了時に呼ばれる。
//
// 具体実装は以下のサブパッケージに存在する:
//   - notifier/toast   — Windows Toast 通知 (Windows 専用、実装済み。
//     環境変数 CCWIN_NOTIFY_USE_FAKE_TOASTER=1 で Fake に切替可能)
//   - notifier/sound   — WinMM PlaySound による WAV 再生 (Windows 専用、実装済み)
//   - notifier/webhook — Discord / Slack Webhook 送信 (HTTPS 必須・retry 対応、実装済み)
//
// # FakeNotifier
//
// [FakeNotifier] はテスト専用の Notifier 実装。
// Notify を呼ぶと引数の Event が [FakeNotifier.Records] に追記される。
// [FakeNotifier.Err] に非 nil を設定すると Notify が常にその error を返す。
// [FakeNotifier.Reset] で Records をクリアして再利用できる。
package notifier

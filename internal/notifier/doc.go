// Package notifier は通知バックエンドの共通インターフェースと、テスト用の偽実装を提供する。
//
// # Notifier
//
// [Notifier] インターフェースは 2 つのメソッドを持つ:
//   - Name() string     — ログや SSE のディスパッチ結果に使われる一意な識別名を返す。
//   - Notify(ctx, event.Event) error — イベントを受け取り通知を実行する。
//     ctx が期限切れになった場合は速やかに処理を打ち切り error を返すこと。
//
// 具体実装は以下のサブパッケージに存在する:
//   - notifier/toast   — Windows Toast 通知 (Windows 専用)
//   - notifier/sound   — WinMM PlaySound による WAV 再生 (Windows 専用) (v0.2.0 以降で実装予定)
//   - notifier/webhook — Discord / Slack Webhook 送信 (v0.2.0 以降で実装予定)
//
// # FakeNotifier
//
// [FakeNotifier] はテスト専用の Notifier 実装。
// Notify を呼ぶと引数の Event が [FakeNotifier.Records] に追記される。
// [FakeNotifier.Err] に非 nil を設定すると Notify が常にその error を返す。
// [FakeNotifier.Reset] で Records をクリアして再利用できる。
package notifier

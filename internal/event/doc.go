// Package event は ccwin-notify が内部で扱うイベント型・バス・ドロップポリシーを定義する。
//
// # 概要
//
// EventSource (Hooks / プロセス監視) は正規化した [Event] を [Bus] に Publish し、
// Dispatcher が [Bus.Subscribe] で受け取って各 [Notifier] に配送する。
//
// # Event
//
// [Event] は全 EventSource が出力する共通形式。
// [EventKind] は Stop / Notification / SubagentStop (Hooks 由来) と
// Idle / ProcessStarted / ProcessStopped (プロセス監視由来) の 6 種。
//
// ID は [NewID] で生成される ULID (crypto/rand ベース、ソート可能な相関キー)。
// v0.1.0 での採番経路:
//   - [source/hooks.HandleEvents] (POST /v1/events): 受信 JSON から新規 Event を構築し
//     [NewID] で ULID を付与する。send.NormalizeHook が設定した ID は受信側で再生成される。
//   - [daemon.HandleTest] (POST /v1/test): テスト発火時に [NewID] で ULID を付与する
//   - send.NormalizeHook (send サブコマンド): [NewID] で ULID を設定してから IPC へ送信する。
//     ただし HandleEvents が受信時に新しい ULID を上書き付与するため、
//     Bus に投入される Event の ID は HandleEvents が生成したものになる。
//
// Bus 上の全 Event は必ず ULID を持つため G2 (event_id 相関) は全ソースで充足する。
//
// # Bus
//
// [Bus] は bounded queue + drop policy を持つイベントバス。
// [NewBus] で生成し、容量超過時の挙動を [DropPolicy] で選択できる:
//   - [DropOldest]: 最古の未配送イベントを破棄して新着を受け付ける
//   - [DropNewest]: 新着を破棄して [ErrDropped] を返す
//   - [DropBlock]:  空きが出るまでブロックする。ctx キャンセルで [ErrPublishCanceled]、
//     Bus Close で [ErrBusClosed] を返す
//
// # EventSink
//
// [EventSink] は Source から Bus への書き口のみを切り出した最小インターフェース。
// Source は drop policy / capacity を知らずに Publish できる (D-20)。
package event

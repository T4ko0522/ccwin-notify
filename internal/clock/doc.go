// Package clock は時刻取得とタイマー生成を抽象化するインターフェースを提供する。
//
// # 目的
//
// 本番コードが time パッケージを直接呼び出すとテストの決定性が失われる (F6)。
// [Clock] / [Timer] インターフェースを DI することで、テストでは [FakeClock] に
// 差し替えてタイムアウトや経過時間を即座に制御できる。
//
// # Clock
//
// [Clock] は現在時刻の取得・チャネルタイマー生成・[Timer] 生成を担う:
//   - Now() は現在の [time.Time] を返す。
//   - After(d) は d 後に現在時刻を送信する読み取り専用チャネルを返す (time.After と同義)。
//   - NewTimer(d) は d 後に発火する [Timer] を返す。Stop/Reset が必要な場合に使う。
//
// [Real] で本番用の Clock を取得する。
//
// # FakeClock
//
// [FakeClock] はテスト専用の Clock 実装:
//   - 初期時刻を [NewFake] で設定する。
//   - [FakeClock.Advance] で時計を任意の分だけ進める。
//   - Advance すると対応する Timer / After チャネルが同期的に発火するため、
//     goroutine を待つ sleep が不要になる。
package clock

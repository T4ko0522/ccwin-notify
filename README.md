# ccwin-notify

Windows 上で動作する Claude Code セッション通知デーモン。
Claude Code の Hooks イベント (Stop / Notification / SubagentStop) を受け取り、
Windows Toast 通知・サウンド再生・Discord / Slack Webhook への送信を行う。

---

## 現在の状態 (v0.1.0)

v0.1.0 はコアパッケージ・CLI サブコマンドの実装が完了した段階。
Windows 実機での End-to-End 動作検証は未実施。

**実装済みの機能 (コードレベル):**

- `just build` でバイナリが生成される
- `just check` (fmt → vet → test) が全パッケージ PASS
- イベントバス (`internal/event.Bus`) — 有界キュー + drop-oldest / drop-newest / block ポリシー
- Hooks IPC ハンドラ (`source/hooks.HandleEvents` / `HandleStream`) — ULID 付き Event を Bus に投入
- Dispatcher — fan-out goroutine、per-Notifier セマフォ、panic リカバリー
- HTTP Server (`internal/ipc/server`) — ReadHeaderTimeout 5s / WriteTimeout 0 (SSE 対応) で構成
- Bearer 認証 (`internal/ipc/middleware`) — SHA-256 固定長 constant-time 比較
- Sound Notifier (`internal/notifier/sound`) — WinMM PlaySoundW による WAV 非同期再生
- Webhook Notifier (`internal/notifier/webhook`) — Discord / Slack、指数バックオフ + Retry-After 対応
- Toast Notifier — `go-toast` を使った実 Toaster 実装済み (`toast_real_windows.go`)。CI 時は `CCWIN_NOTIFY_USE_FAKE_TOASTER=1` で FakeToaster に切り替え可能
- SSE Hub — TUI 向けライブログストリーム
- TUI Model — bubbletea による Elm アーキテクチャ実装
- 設定ファイル読み込み / バリデーション (`%APPDATA%/ccwin-notify/config.toml`)
- `apiclient` インターフェース — send CLI / TUI が共有するデーモン API クライアント
- `internal/auth` — secret.token 生成・Windows ACL 設定 (LoadOrCreate / EnsureDirACL、fail-closed 設計)
- CLI サブコマンド (`daemon` / `send` / `tui` / `config` / `version`) — 全て結線済み

**未実装・未検証の機能 (継続サイクルで対応予定):**

- プロセス監視 (`internal/source/process/` — 空ディレクトリ)
- Toast AUMID 整備 (スタートメニューへのショートカット登録 — Phase 5 以降)
- Windows 実機での End-to-End 動作検証 (Toast 発火・Sound 再生・Webhook 送信を含む)
- winget / scoop によるパッケージ配布

---

## 対応環境

- **OS**: Windows 10 / 11 (amd64)
- Windows 以外の OS では起動時に即座にエラーで終了する (`GOOS != "windows"` チェック)。ビルドは任意の OS で可能。

---

## 前提ツール

| ツール | バージョン | 役割 |
|--------|------------|------|
| [mise](https://mise.jdx.dev/) | 任意 (最新安定版推奨) | Go / just のバージョン管理 |
| Go | 1.26.3 (`mise` が自動導入) | ビルド |
| just | 1.51.0 (`mise` が自動導入) | タスクランナー |

---

## インストール (開発者向け)

```powershell
# 1. リポジトリをクローン
git clone https://github.com/t4ko0522/ccwin-notify
cd ccwin-notify

# 2. Go と just を導入
mise install

# 3. ビルド
just build
# => bin/ccwin-notify.exe が生成される
```

---

## クイックスタート

> **注意**: v0.1.0 ではコードが実装済みだが Windows 実機での End-to-End 動作検証は未実施。
> Toast AUMID が設定されていない環境では通知が「PowerShell」名義で表示される場合がある。

### 起動フロー

```powershell
# Step 1: デーモンを別ターミナルで起動
.\bin\ccwin-notify.exe daemon

# Step 2: 別ターミナルで TUI を起動してライブログを確認
.\bin\ccwin-notify.exe tui
```

### Claude Code Hooks の設定

`~/.claude/settings.json` に以下を追加する:

```jsonc
{
  "hooks": {
    "Stop": [
      {
        "type": "command",
        "command": "powershell -NoProfile -File C:\\path\\to\\hook.ps1 Stop",
        "async": true
      }
    ],
    "Notification": [
      {
        "type": "command",
        "command": "powershell -NoProfile -File C:\\path\\to\\hook.ps1 Notification",
        "async": true
      }
    ],
    "SubagentStop": [
      {
        "type": "command",
        "command": "powershell -NoProfile -File C:\\path\\to\\hook.ps1 SubagentStop",
        "async": true
      }
    ]
  }
}
```

`hook.ps1`:

```powershell
param([string]$Kind)
# stdin の JSON を ccwin-notify send に渡す
$Input | & "C:\path\to\ccwin-notify.exe" send --kind $Kind --stdin
```

> `--stdin` フラグを使うことで、PowerShell の `echo` 経由で発生するバックスラッシュ破壊問題 (Issue #44482) を回避している。
> JSON のパースは Go CLI 側で行われる。

---

## CLI サブコマンド

| サブコマンド | 説明 |
|-------------|------|
| `daemon` | デーモンを起動する。多重起動は Windows Mutex で防止 |
| `send --kind <Kind> --stdin` | stdin の JSON を Hook イベントとしてデーモンに送信 |
| `tui` | ライブログ TUI を起動する |
| `config show` | 現在の設定を表示する |
| `config path` | 設定ファイルのパスを表示する |

### Hook イベント種別

| Kind | 発火タイミング |
|------|---------------|
| `Stop` | Claude Code がレスポンスを完了したとき |
| `Notification` | Claude Code が通知を送出したとき |
| `SubagentStop` | サブエージェントが完了したとき |

---

## 設定ファイル

設定ファイルのデフォルトパス: `%APPDATA%\ccwin-notify\config.toml`

ファイルが存在しない場合はデフォルト値で動作する。

### 設定例

```toml
log_level = "info"        # "debug" | "info" | "warn" | "error"
log_format = "text"       # "text" | "json"
shutdown_timeout = "5s"   # graceful shutdown 上限 (C4)

[queue]
capacity = 256
policy = "drop-oldest"    # "drop-oldest" | "drop-newest" | "block"

[dispatcher]
max_concurrent_per_notifier = 4  # per-Notifier の同時実行数上限
notifier_timeout = "3s"          # per-attempt タイムアウト (E4)

[ipc]
bind_address = "127.0.0.1"   # ループバックアドレス必須 (セキュリティ要件 A6)

[sources.hooks]
enabled = true

[sources.process]
enabled = false    # internal/source/process は未実装 (v0.2.0 以降)
interval = "2s"
process_name = "claude.exe"
idle_threshold = "60s"

[notifiers.toast]
enabled = true

[notifiers.sound]
enabled = false
wav_path = ""

[notifiers.webhook.discord]
enabled = false
url = ""           # https:// で始まる URL が必須
timeout = "3s"
max_retries = 3

[notifiers.webhook.slack]
enabled = false
url = ""           # https:// で始まる URL が必須
timeout = "3s"
max_retries = 3
```

### バリデーション規則

- `ipc.bind_address`: `127.0.0.1` 完全一致のみ受理 (A6 / I2)。`::1` や `127.0.0.0/8` 内の他アドレスは拒否される。
- `notifiers.webhook.*.url` は URL が空でなければ常に `https://` 必須 (enabled に関わらず検証)。`enabled = true` のときは URL が空文字なら起動失敗。エラーメッセージには URL 平文を含めない (H-03 / I4)。
- `queue.capacity` は 1 以上
- `queue.policy` は `drop-oldest` / `drop-newest` / `block` のいずれか
- `dispatcher.max_concurrent_per_notifier` は 1 以上 256 以下
- `dispatcher.notifier_timeout` は 100ms 以上 5 分以下

---

## 動作確認コマンド

```powershell
# 全パッケージのテストと静的解析
just check

# テストのみ
just test

# race detector 付きテスト
just test-race

# カバレッジレポート生成 (coverage.html)
just cover

# ビルドのみ
just build
```

---

## パッケージ構成

```
ccwin-notify/
├── cmd/ccwin-notify/        # エントリポイント (サブコマンドルーティング)
└── internal/
    ├── event/               # Event 型・EventKind・Bus・DropPolicy
    ├── secret/              # SecretString (slog LogValuer + MarshalJSON によるマスク)
    ├── clock/               # Clock 抽象 (Real / FakeClock)
    ├── config/              # TOML 設定読み込み・バリデーション
    ├── platform/            # OS プラットフォーム固有機能
    ├── notifier/            # Notifier インターフェース・FakeNotifier
    │   ├── toast/           # Windows Toast Notifier (go-toast 使用 / CI 時は CCWIN_NOTIFY_USE_FAKE_TOASTER=1 で FakeToaster)
    │   ├── sound/           # WinMM PlaySoundW による WAV 非同期再生
    │   └── webhook/         # Discord / Slack Webhook 送信 (指数バックオフ + Retry-After 対応)
    ├── daemon/              # Dispatcher (fan-out / WaitGroup / panic recovery)
    ├── send/                # Hook stdin JSON → Event 正規化 (NormalizeHook)
    ├── tui/                 # bubbletea TUI Model (Elm アーキテクチャ)
    ├── apiclient/           # デーモン API クライアント (SSE 再接続含む)
    ├── auth/                # secret.token 生成・Windows ACL 設定 (LoadOrCreate / EnsureDirACL)
    ├── logging/             # slog text/json ハンドラ構築
    ├── ipc/
    │   ├── client/          # portfile 読み込み・Bearer HTTP クライアント
    │   ├── middleware/       # Bearer 認証 (SHA-256 constant-time 比較)
    │   ├── server/          # HTTP サーバー集約・RegisterRoute
    │   └── sse/             # SSE Hub (Publish / Subscribe / Close)
    └── source/
        ├── hooks/           # Hooks IPC ハンドラ (HTTP server は非所有)
        └── process/         # プロセス監視ポーリング (未実装 - v0.2.0 以降)

### アーキテクチャ概要

パッケージ間の主要な依存方向を示す。詳細は `docs/plans/2026-05-14-ccwin-notify-bootstrap/2_plan.md` を参照。

```
[Claude Code Hooks]
        |
        v (HTTP POST / stdin JSON)
  send / source/hooks        <-- ipc/middleware (Bearer 認証)
        |
        v
  event.Bus (bounded queue)
        |
        v
  daemon.Dispatcher (fan-out goroutine + panic recovery)
     |        |        |
     v        v        v
  notifier/ notifier/ notifier/
  toast    sound    webhook

横断:
  config  --> 全パッケージ (設定注入)
  secret  --> config / logging (SecretString マスク)
  clock   --> daemon / source/process (FakeClock でテスト決定性確保)
  ipc/sse --> apiclient / tui (SSE ライブログストリーム)
  platform --> OS チェック (main での起動拒否)
```

---

## セキュリティ設計

詳細は [docs/SECURITY.md](docs/SECURITY.md) を参照。

- IPC は `127.0.0.1` にバインドし、外部ネットワークには公開しない (A6)
- Bearer トークンは SHA-256 ハッシュ化して 32 byte 固定長で `subtle.ConstantTimeCompare` 比較 (D-39)
  タイミング攻撃によるトークン長の推定を防ぐ
- Webhook URL はログ・JSON API レスポンスどちらにも出力しない (`SecretString` が slog LogValuer + MarshalJSON で自動マスク)
- トークンファイル ACL: `internal/auth.LoadOrCreate` / `EnsureDirACL` で実装済み (Windows DACL + fail-closed 設計)

---

## トラブルシューティング

以下の各項目はコードが実装済みであることを前提とした案内。Windows 実機での End-to-End 動作検証は未実施。

### portfile が stale のまま残っている (デーモンが起動できない)

デーモンが異常終了すると `%APPDATA%\ccwin-notify\daemon.port` が残る場合がある。

```powershell
# portfile を確認 (PID が存在するか)
Get-Content "$env:APPDATA\ccwin-notify\daemon.port"

# PID に対応するプロセスが存在しなければ portfile を削除
Remove-Item "$env:APPDATA\ccwin-notify\daemon.port"

# デーモンを再起動
.\bin\ccwin-notify.exe daemon
```

### Bearer トークン不一致で 401 が返る

`send` CLI が読む `secret.token` とデーモンが保持するトークンが異なる場合に発生する。

- デーモンを再起動するとトークンが再生成されるため、その後に `send` が自動的に最新のトークンを読み込む。
- `%APPDATA%\ccwin-notify\secret.token` が存在しない場合は、デーモン起動時に `internal/auth.LoadOrCreate` が自動生成する。

### Toast が表示されない

以下のことを確認する (コードが実装済みであることを前提とした手順):

- `CCWIN_NOTIFY_USE_FAKE_TOASTER=1` 環境変数が設定されている場合は FakeToaster が使われ実 Toast は発火しない。この環境変数を削除してデーモンを再起動する。
- AUMID (アプリ識別子) が未設定の環境では通知が「PowerShell」名義で表示される場合がある。
  スタートメニューへのショートカット登録が必要 (Phase 5 以降で対応予定)。
- Sound 再生で WAV ファイルが見つからない場合は、`notifiers.sound.wav_path` に有効なパスを設定すること。

### Webhook 送信が失敗する

- `notifiers.webhook.discord.url` または `notifiers.webhook.slack.url` が `https://` で始まっていることを確認する。
- ファイアウォールまたはプロキシが outbound HTTPS を遮断していないか確認する。
- `log_level = "debug"` に設定してデーモンを再起動し、ログで詳細なエラーを確認する。

### "subcommand required" が表示される

引数なしで `ccwin-notify` を実行すると必ずこのメッセージで終了する。`daemon` / `send` / `tui` / `config` / `version` のいずれかを指定すること。

### go test が失敗する

```powershell
# Go のバージョンを確認
go version
# => go1.26.3 windows/amd64 が期待値

# mise で正しいバージョンを使う
mise install
mise exec -- go test ./...
```

---

## 制限事項

- Windows 10 / 11 (amd64) 専用。Linux / macOS では動作しない
- GUI 設定画面・システムトレイ常駐は非スコープ
- 自動アップデート機構なし
- パッケージ配布 (winget / scoop) は未対応

### 既知の実装側 TODO (継続サイクルで対応予定)

以下は設計で決定済みだが v0.1.0 では未対応の項目:

- **bind_address 検証強化**: `config.Validate()` は現状 `IsLoopback()` 判定 (IPv6 `::1` や `127.0.0.2` も通過)。設計仕様 §6.2 は `127.0.0.1` 単一 IP 固定のため、実装を IP 完全一致チェックに修正する必要がある
- **Toast AUMID 整備**: スタートメニューへのショートカット登録。Phase 5 以降で対応予定
- **プロセス監視** (`internal/source/process`): 空ディレクトリ。v0.2.0 以降で実装予定

---

## 開発者向け情報

### TDD フロー

本プロジェクトは TDD (探索 → Red → Green → Refactor) で開発している。
各サイクルの設計判断は `docs/plans/2026-05-14-ccwin-notify-bootstrap/2_plan.md` を参照。

### race detector

全テストは `-race` フラグ付きで PASS することを確認している:

```powershell
just test-race
```

### コントリビュート

1. `just check` を通してから PR を作成する
2. 新しいパッケージには godoc コメント (`Package xxx は ...` で始まる) を必ず書く。
   既存 `.go` ファイル冒頭にパッケージコメントがある場合は `doc.go` を別途作る必要はない
3. テストは `_test.go` に書き、依存は `FakeXxx` 型で注入する
4. TDD サイクル中に書いた「Red 段階」などのコメントは Green 通過後に削除する。リリースブランチには残さない

---

## ライセンス

LICENSE 未確定 — リポジトリ owner と要相談

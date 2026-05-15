# ccwin-notify

Windows で Claude Code のセッションイベントを通知するデーモン。

Claude Code が応答を完了したり通知を出したりしたタイミングを、Windows のトースト通知・サウンド再生・Discord / Slack の Webhook で受け取れる。長時間の作業中に Claude Code の状態を見逃さなくなる。

> 現在ベータ版 (v0.1.0)。

---

## 何ができるか

Claude Code の Hooks (`Stop` / `Notification` / `SubagentStop`) を受け取り、設定に応じて以下を実行する。

- **Windows トースト通知** — 応答完了などをデスクトップ右下に表示
- **サウンド再生** — お好みの WAV ファイルを再生
- **Discord / Slack Webhook** — チャットに通知を投稿 (リトライ・指数バックオフ対応)
- **ライブログ TUI** — 通知の流れをターミナルで確認

各通知先は個別に有効化・無効化できる。

---

## 対応環境

- Windows 10 / 11 (64bit)

Windows 以外では起動しない。

---

## インストール

### Scoop でインストール

```powershell
# 1. tap (bucket) を追加
scoop bucket add t4ko0522 https://github.com/t4ko0522/tap

# 2. インストール
scoop install ccwin-notify

# 3. 初期設定 (対話型ウィザードで音声 / Webhook URL などを設定)
ccwin-notify init
```

`ccwin-notify init` が `%APPDATA%\ccwin-notify\config.toml` を作成する。既存ファイルがある場合は上書き確認が出る (`--force` でスキップ可)。

更新は `scoop update ccwin-notify` で行う。

---

## 使い方

### 1. デーモンを起動する

```powershell
ccwin-notify daemon
```

このターミナルは起動したままにする。閉じるとデーモンも止まる。

### 2. (任意) ライブログを確認する

別のターミナルで TUI を起動すると、通知の流れがリアルタイムで見える。

```powershell
ccwin-notify tui
```

### 3. Claude Code 側に Hooks を設定する

`~/.claude/settings.json` に以下を追加する。`C:\path\to\hook.ps1` と `C:\path\to\ccwin-notify.exe` は実際のパスに置き換える (Scoop インストールなら `ccwin-notify.exe` は `~\scoop\apps\ccwin-notify\current\ccwin-notify.exe`)。

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

`hook.ps1` の中身:

```powershell
param([string]$Kind)
$Input | & "C:\path\to\ccwin-notify.exe" send --kind $Kind --stdin
```

これで Claude Code が応答を完了したタイミング (`Stop`) などに通知が飛ぶようになる。

### Hook イベント種別

| Kind | 発火タイミング |
|------|---------------|
| `Stop` | Claude Code がレスポンスを完了したとき |
| `Notification` | Claude Code が通知を出したとき |
| `SubagentStop` | サブエージェントが完了したとき |

---

## 設定ファイル

設定ファイルのパス: `%APPDATA%\ccwin-notify\config.toml`

`ccwin-notify init` で作成できる。ファイルが存在しない場合はデフォルト値で動作する。

### 設定例

```toml
log_level = "info"        # "debug" | "info" | "warn" | "error"

[notifiers.toast]
enabled = true

[notifiers.sound]
enabled = false
wav_path = ""             # 再生したい WAV ファイルのパス

[notifiers.webhook.discord]
enabled = false
url = ""                  # https://discord.com/api/webhooks/... を指定

[notifiers.webhook.slack]
enabled = false
url = ""                  # https://hooks.slack.com/services/... を指定
```

### 主な設定項目

- **`notifiers.toast.enabled`** — Windows トースト通知の ON/OFF
- **`notifiers.sound.enabled` / `wav_path`** — サウンド再生の ON/OFF と WAV ファイルのパス
- **`notifiers.webhook.discord.url` / `notifiers.webhook.slack.url`** — Webhook URL (必ず `https://` で始まる URL)

Webhook URL はログや表示には出力されないようマスクされる。

---

## CLI サブコマンド

| サブコマンド | 説明 |
|-------------|------|
| `ccwin-notify daemon` | デーモンを起動する |
| `ccwin-notify init` | 対話型ウィザードで `config.toml` を生成する (`--force` で既存上書き確認をスキップ) |
| `ccwin-notify tui` | ライブログ TUI を起動する |
| `ccwin-notify send --kind <Kind> --stdin` | stdin の JSON を Hook イベントとしてデーモンに送信 (Hook 用) |
| `ccwin-notify config show` | 現在の設定を表示する |
| `ccwin-notify config path` | 設定ファイルのパスを表示する |
| `ccwin-notify version` | バージョンを表示する |

---

## トラブルシューティング

### トースト通知が表示されない

- 設定で `notifiers.toast.enabled = true` になっているか確認する
- Windows の通知設定 (システム → 通知) で通知が許可されているか確認する
- 通知が「PowerShell」名義で表示される場合がある (今後のリリースで改善予定)

### Webhook 通知が届かない

- `notifiers.webhook.*.url` が `https://` で始まる正しい URL になっているか確認する
- ファイアウォール / プロキシが outbound HTTPS を遮断していないか確認する
- 設定ファイルの `log_level` を `"debug"` にしてデーモンを再起動し、ログでエラー内容を確認する

### デーモンが起動できない / "portfile が残っている" 系のエラー

デーモンが異常終了した場合に発生する。次の手順で復旧する。

```powershell
# portfile を削除
Remove-Item "$env:APPDATA\ccwin-notify\daemon.port"

# デーモンを再起動
ccwin-notify daemon
```

### `subcommand required` と表示されて終了する

`ccwin-notify` を引数なしで実行するとこのメッセージが出る。`daemon` / `init` / `tui` / `config` / `version` のいずれかを必ず指定する。

---

## ライセンス

https://github.com/T4ko0522/ccwin-notify/blob/main/LICENSE

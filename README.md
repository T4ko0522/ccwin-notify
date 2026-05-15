# ccwin-notify

Windows で Claude Code のセッションイベントを通知するデーモン。

Claude Code が応答を完了したり通知を出したりしたタイミングを、Windows のトースト通知・サウンド再生・Discord / Slack の Webhook で受け取れる。長時間の作業中に Claude Code の状態を見逃さなくなる。

> 現在ベータ版 (v1.1.0)

---

## 何ができるか

Claude Code の Hooks (`Stop` / `Notification` / `SubagentStop`) を受け取り、設定に応じて以下を実行する。

- **Windows トースト通知** — 応答完了などをデスクトップ右下に表示
- **サウンド再生** — 同梱の通知音を再生 (任意の WAV に差し替え可)
- **Discord / Slack Webhook** — チャットに通知を投稿 (リトライ・指数バックオフ対応)
- **ライブログ TUI** — 通知の流れをターミナルで確認

各通知先は個別に有効化・無効化できる。

---

## 対応環境

- **OS**: Windows 10 / 11 (64bit) ※ Windows 以外では起動しない
- **ターミナル**: 任意 (Windows Terminal / WezTerm / PowerShell / cmd など)
  - ただし **`sources.wezterm` Source は WezTerm 必須**。AskUserQuestion / ExitPlanMode の即時検知 (Claude Code の組み込み UI パネル) には `wezterm.exe` をインストールし、Claude Code を WezTerm 上で起動する必要がある。
- **Claude Code**: v2.1.x で動作確認 (Hooks API 経由 + sessionlog 監視)

---

## インストール

### Scoop でインストール

```powershell
# 1. tap (bucket) を追加
scoop bucket add t4ko0522 https://github.com/t4ko0522/tap

# 2. インストール
scoop install ccwin

# 3. (任意) Webhook など追加設定をする場合のみ ccwin init を実行
ccwin init --discord-webhook "https://discord.com/api/webhooks/..."
```

config.toml は初回 `ccwin daemon` 起動時に既定値で自動生成されるため、`ccwin init` は必須ではない。

`ccwin init` は defaultConfig を CLI フラグ (Webhook URL や Toast/Sound の ON/OFF など) で上書きして `%USERPROFILE%\.config\ccwin-notify\config.toml` (または `$XDG_CONFIG_HOME\ccwin-notify\config.toml`) に書き出す。既存ファイルは `--force` を付けないと上書きされない。詳細なフラグは [`CLI サブコマンド`](#cli-サブコマンド) を参照。

更新は `scoop update ccwin` で行う。

---

## 使い方

### 1. デーモンを起動する

```powershell
ccwin daemon
```

このターミナルは起動したままにする。閉じるとデーモンも止まる。

### 2. (任意) ライブログを確認する

別のターミナルで TUI を起動すると、通知の流れがリアルタイムで見える。

```powershell
ccwin tui
```

### 3. Claude Code 側に Hooks を設定する

`~/.claude/settings.json` に以下を追加する。`C:\path\to\hook.ps1` と `C:\path\to\ccwin.exe` は実際のパスに置き換える (Scoop インストールなら `ccwin.exe` は `~\scoop\apps\ccwin\current\ccwin.exe`)。

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
$Input | & "C:\path\to\ccwin.exe" send --kind $Kind --stdin
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

設定ファイルのパス解決:

1. `--config <path>` で明示指定したパス
2. `$XDG_CONFIG_HOME\ccwin-notify\config.toml` (環境変数が設定されている場合)
3. `%USERPROFILE%\.config\ccwin-notify\config.toml` (フォールバック)

`ccwin daemon` 起動時にファイルが無ければ defaultConfig を書き出す。`ccwin init` でも生成できる (CLI フラグで上書き可)。手動で TOML を直接編集する場合も同様に動作する。なお `secret.token` と `daemon.port` は引き続き `%APPDATA%\ccwin-notify\` 配下に保存される。

### 設定例

```toml
log_level = "info"        # "debug" | "info" | "warn" | "error"

[notifiers.toast]
enabled = true

[notifiers.sound]
enabled = true
wav_path = ""             # 空文字なら同梱の default.wav を使用、任意の WAV パスを指定すると差し替え

[notifiers.webhook.discord]
enabled = false
url = ""                  # https://discord.com/api/webhooks/... を指定

[notifiers.webhook.slack]
enabled = false
url = ""                  # https://hooks.slack.com/services/... を指定
```

### 主な設定項目

- **`notifiers.toast.enabled`** — Windows トースト通知の ON/OFF
- **`notifiers.sound.enabled` / `wav_path`** — サウンド再生の ON/OFF と WAV ファイルのパス (空なら同梱 WAV)
- **`notifiers.webhook.discord.url` / `notifiers.webhook.slack.url`** — Webhook URL (必ず `https://` で始まる URL)

Webhook URL はログや表示には出力されないようマスクされる。

### 同梱の通知音について

`internal/notifier/sound/assets/default.wav` がバイナリに `//go:embed` で取り込まれており、`wav_path` が空のときはこれを再生する。WAV を差し替えたい場合は同じパスにファイルを置いた上で再ビルドする。

---

## CLI サブコマンド

| サブコマンド | 説明 |
|-------------|------|
| `ccwin daemon` | デーモンを起動する (config.toml 不在時は defaultConfig を自動書き出し) |
| `ccwin init [flags]` | CLI フラグから `config.toml` を生成する (詳細は下記) |
| `ccwin tui` | ライブログ TUI を起動する |
| `ccwin send --kind <Kind> --stdin` | stdin の JSON を Hook イベントとしてデーモンに送信 (Hook 用) |
| `ccwin config show` | 現在の設定を表示する |
| `ccwin config path` | 設定ファイルのパスを表示する |
| `ccwin version` | バージョンを表示する |

### `ccwin init` フラグ

| フラグ | 説明 |
|-------|------|
| `--force` | 既存の `config.toml` を確認なしで上書きする |
| `--path <path>` | 出力先パスを明示指定 |
| `--log-level <level>` | `debug` / `info` / `warn` / `error` |
| `--toast <true\|false>` | Toast 通知の ON/OFF |
| `--sound <true\|false>` | Sound 通知の ON/OFF |
| `--discord-webhook <url>` | Discord Webhook URL を設定し、`discord.enabled = true` にする |
| `--slack-webhook <url>` | Slack Webhook URL を設定し、`slack.enabled = true` にする |

フラグを渡さない項目は defaultConfig の値が使われる。細かい設定 (`kind_mask` や `dispatcher.notifier_timeout` など) を変えたい場合は TOML を直接編集する。

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
ccwin daemon
```

### `subcommand required` と表示されて終了する

`ccwin` を引数なしで実行するとこのメッセージが出る。`daemon` / `init` / `tui` / `config` / `version` のいずれかを必ず指定する。

---

## ライセンス

https://github.com/T4ko0522/ccwin-notify/blob/main/LICENSE

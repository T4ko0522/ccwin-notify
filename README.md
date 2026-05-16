# ccwin-notify

Windows版 Claude Codeのhooksは**非常に不安定**で通知を送信するshell scriptを書いても通知を逃すことが多い。  
そこで今回の **ccwin-notify** を使用してください。
ccwin-notify は Claude Code の Hooks を受け取り、Windows トースト・通知音などで「完了したよ」を即座に知らせる。

---

## こんな人向け

- **Windows 10 / 11** で Claude Code を使っている
- 長いタスクを投げて別作業をしている間に、応答完了を **トースト / 音 / Discord / Slack** で受け取りたい
-  
> `AskUserQuestion` や `ExitPlanMode` のような Claude Code の組み込み UI パネル検知システムはWezTermのユーザーのみ利用可能です。

---

## できること

| 通知先 | 説明 |
|--------|------|
| **Windows トースト** | デスクトップ右下に通知を表示 |
| **サウンド再生** | 同梱の通知音を再生 (任意の WAV に差し替え可) |
| **Discord Webhook** | チャットに投稿 (リトライ・指数バックオフ対応) |
| **Slack Webhook** | チャットに投稿 (リトライ・指数バックオフ対応) |
| **ライブログ TUI** | 通知の流れをターミナルで確認 |

各通知先は config で個別に ON/OFF

---

## クイックスタート (3 ステップ)

### Step 1. インストール

[Scoop](https://scoop.sh/) を使う。

```powershell
# 1. tap (bucket) を追加
scoop bucket add t4ko0522 https://github.com/t4ko0522/tap

# 2. インストール
scoop install ccwin
```

更新は `scoop update ccwin`。

### Step 2. デーモンを起動

#### Claude Code 起動時にデーモンを自動起動する (任意)

毎回手動で `ccwin daemon` を立ち上げるのが面倒な場合は、Claude Code の `SessionStart` Hook でバックグラウンド起動させられる。`~/.claude/settings.json` は以下のようになる。

```jsonc
{
  "hooks": {
    "SessionStart": [
      {
        "type": "command",
        "command": "powershell -NoProfile -File C:\\path\\to\\ccwin-autostart.ps1",
        "async": true
      }
    ],
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

`ccwin-autostart.ps1` の中身:

```powershell
# ccwin daemon が起動していなければバックグラウンドで立ち上げる。
$portfile = Join-Path $env:APPDATA "ccwin-notify\daemon.port"
if (Test-Path $portfile) {
    # portfile が残っている = 既に起動中とみなしてスキップ。
    # 異常終了で残骸が残った場合はトラブルシューティングを参照。
    exit 0
}

# Scoop インストールの既定パスを優先、無ければ PATH の ccwin を使う。
$exe = "$env:USERPROFILE\scoop\apps\ccwin\current\ccwin.exe"
if (-not (Test-Path $exe)) { $exe = "ccwin" }

Start-Process -FilePath $exe -ArgumentList "daemon" -WindowStyle Hidden
```

## Hook イベント種別

| Kind | 発火タイミング |
|------|---------------|
| `Stop` | Claude Code がレスポンスを完了したとき |
| `Notification` | Claude Code が通知を出したとき |
| `SubagentStop` | サブエージェントが完了したとき |

---

## 対応環境

- **OS**: Windows 10 / 11 (64bit)
- **Claude Code**: v2.1.x で動作確認 (Hooks API + sessionlog 監視)

### WezTerm 限定機能

`sources.wezterm` Source は **WezTerm 必須**。`AskUserQuestion` / `ExitPlanMode` のような Claude Code 組み込み UI パネルの即時検知をしたい場合は、`wezterm.exe` をインストールし、Claude Code を WezTerm 上で起動する必要がある。

WezTerm を使わない場合は Hooks (`Stop` 等) ベースの通知のみ動作する。

---

## 設定ファイル

### 設定ファイルのパス解決

1. `--config <path>` で明示指定したパス
2. `$XDG_CONFIG_HOME\ccwin-notify\config.toml` (環境変数が設定されている場合)
3. `%USERPROFILE%\.config\ccwin-notify\config.toml` (フォールバック)

`ccwin daemon` 起動時にファイルが無ければ defaultConfig を書き出す。`ccwin init` でも生成できる (CLI フラグで上書き可)。手動で TOML を直接編集する場合も同様に動作する。

> `secret.token` と `daemon.port` は `%APPDATA%\ccwin-notify\` 配下に保存される (Webhook URL とは別管理)。

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

| キー | 説明 |
|------|------|
| `notifiers.toast.enabled` | Windows トースト通知の ON/OFF |
| `notifiers.sound.enabled` | サウンド再生の ON/OFF |
| `notifiers.sound.wav_path` | WAV ファイルのパス (空なら同梱 WAV) |
| `notifiers.webhook.discord.url` | Discord Webhook URL (`https://` 必須) |
| `notifiers.webhook.slack.url` | Slack Webhook URL (`https://` 必須) |

Webhook URL はログや表示で自動的にマスクされる。

### 同梱の通知音

`internal/notifier/sound/assets/default.wav` がバイナリに `//go:embed` で取り込まれており、`wav_path` が空のときはこれを再生する。

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

---

## ライセンス

https://github.com/T4ko0522/ccwin-notify/blob/main/LICENSE

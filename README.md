# ccwin-notify

**Claude Code / Codex (Windows) の "応答完了" を見逃さないための通知デーモン。**

長時間タスクを Claude Code や Codex に任せていると、応答が返ってきたことに気付かず時間を無駄にしがち。ccwin-notify は Claude Code の Hooks と Codex の rollout JSONL を監視し、Windows トースト・通知音・Discord / Slack Webhook で「完了したよ」を即座に知らせる。

---

## こんな人向け

- **Windows 10 / 11** で Claude Code または Codex CLI を使っている
- 長いタスクを投げて別作業をしている間に、応答完了を **トースト / 音 / Discord / Slack** で受け取りたい
- (任意) **WezTerm** ユーザーで、`AskUserQuestion` や `ExitPlanMode` のような Claude Code の組み込み UI パネルにも即時反応してほしい

> **WezTerm でない人も使えます。** WezTerm が必要なのは「組み込み UI パネルの即時検知」だけで、`Stop` / `Notification` / `SubagentStop` の通知は任意のターミナル (Windows Terminal / PowerShell / cmd など) で動く。

---

## できること

Claude Code の Hooks (`Stop` / `Notification` / `SubagentStop`)、Claude sessionlog 監視、Codex rollout JSONL 監視を組み合わせて、以下を実行する。

| 通知先 | 説明 |
|--------|------|
| **Windows トースト** | デスクトップ右下に通知を表示 |
| **サウンド再生** | 同梱の通知音を再生 (任意の WAV に差し替え可) |
| **Discord Webhook** | チャットに投稿 (リトライ・指数バックオフ対応) |
| **Slack Webhook** | チャットに投稿 (リトライ・指数バックオフ対応) |
| **ライブログ TUI** | 通知の流れをターミナルで確認 |

各通知先は config で個別に ON/OFF できる。

---

## 仕組み (ざっくり)

```
Claude Code ──[Hooks]──────> ccwin send ──> ccwin daemon ──> Toast / Sound / Webhook
              (Stop など)                       ▲
                                                │
Claude sessionlog ──────────────────────────────┤
Codex rollout JSONL ────────────────────────────┘
```

- `ccwin daemon` を常駐させておく
- Claude Code 側から Hook 経由で `ccwin send` を叩く
- Codex は `sources.codexlog.enabled = true` で `~/.codex/sessions/**/*.jsonl` を監視する
- daemon が設定に従って各通知先に配信する

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

> **`ccwin init` は必須ではない。** config.toml は次の `ccwin daemon` 起動時に既定値で自動生成される。Webhook を最初から有効化したい場合だけ `ccwin init --discord-webhook "..."` のように使う (詳細は [CLI サブコマンド](#cli-サブコマンド) を参照)。

### Step 2. デーモンを起動

```powershell
ccwin daemon
```

このターミナルは開いたままにする。閉じるとデーモンも止まる。

### Step 3. Claude Code に Hooks を設定

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

これで Claude Code が応答完了したタイミング (`Stop`) などにトースト・音・Webhook が飛ぶ。

### Step 3b. Codex に対応する

Codex CLI は公開設定で Claude Code 相当のイベント Hook を持たないため、ccwin-notify 側で `~/.codex/sessions/**/*.jsonl` の `final_answer` 追記を監視する。`config.toml` に以下を追加して `ccwin daemon` を再起動する。

```toml
[sources.codexlog]
enabled = true
sessions_dir = ""          # 空文字なら %USERPROFILE%\.codex\sessions
body_max_len = 200
poll_interval = "1s"
```

これで Codex が最終応答を書き出したタイミングで `Codex finished` の `Stop` イベントが発火する。途中経過の commentary は通知しない。

### (任意) ライブログを見る

別ターミナルで TUI を起動すると、通知の流れがリアルタイムで見える。

```powershell
ccwin tui
```

---

## Claude Code 起動時にデーモンを自動起動する (任意)

毎回手動で `ccwin daemon` を立ち上げるのが面倒な場合は、Claude Code の `SessionStart` Hook でバックグラウンド起動させられる。Step 3 の Hook 設定に `SessionStart` を足して、`~/.claude/settings.json` をまとめると以下のようになる。

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

### ポイント

- **`async: true`** — Claude Code 側のセッション開始をブロックしない
- **`-WindowStyle Hidden`** — コンソールウィンドウを出さずに常駐
- **portfile による二重起動防止** — 既にデーモンが動いていれば何もしない
- **PATH 解決のフォールバック** — Scoop 以外でインストールした場合も `ccwin` が PATH にあれば動く

> daemon が異常終了して portfile が残った場合は自動起動がスキップされる。その場合は [トラブルシューティング](#デーモンが起動できない--portfile-が残っている-系のエラー) の手順で portfile を削除する。

---

## Hook イベント種別

| Kind | 発火タイミング |
|------|---------------|
| `Stop` | Claude Code がレスポンスを完了したとき |
| `Notification` | Claude Code が通知を出したとき |
| `SubagentStop` | サブエージェントが完了したとき |

---

## 対応環境

- **OS**: Windows 10 / 11 (64bit) — **Windows 専用**。他 OS では起動しない
- **ターミナル**: 任意 (Windows Terminal / WezTerm / PowerShell / cmd など)
- **Claude Code**: v2.1.x で動作確認 (Hooks API + sessionlog 監視)
- **Codex CLI**: 0.134.0 で動作確認 (`~/.codex/sessions/**/*.jsonl` 監視)

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

[sources.codexlog]
enabled = false
sessions_dir = ""         # 空文字なら %USERPROFILE%\.codex\sessions
body_max_len = 200
poll_interval = "1s"

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
| `sources.codexlog.enabled` | Codex rollout JSONL 監視の ON/OFF |
| `sources.codexlog.sessions_dir` | Codex sessions ディレクトリ (空なら `%USERPROFILE%\.codex\sessions`) |
| `notifiers.webhook.discord.url` | Discord Webhook URL (`https://` 必須) |
| `notifiers.webhook.slack.url` | Slack Webhook URL (`https://` 必須) |

Webhook URL はログや表示で自動的にマスクされる。

### 同梱の通知音

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

フラグを渡さない項目は defaultConfig の値が使われる。`kind_mask` や `dispatcher.notifier_timeout` など細かい設定は TOML を直接編集する。

---

## トラブルシューティング

### トースト通知が表示されない

- 設定で `notifiers.toast.enabled = true` になっているか確認
- Windows の通知設定 (システム → 通知) で通知が許可されているか確認
- 通知が「PowerShell」名義で表示される場合がある (今後のリリースで改善予定)

### Webhook 通知が届かない

- `notifiers.webhook.*.url` が `https://` で始まる正しい URL になっているか確認
- ファイアウォール / プロキシが outbound HTTPS を遮断していないか確認
- `log_level` を `"debug"` にしてデーモンを再起動し、ログでエラー内容を確認

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

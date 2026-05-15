## 認証モデル

ccwin-notify の IPC は **Bearer トークン + SHA-256 ハッシュ** で保護されている。

### トークン生成と保存

- デーモン初回起動時にランダムな Bearer トークンを生成し `%APPDATA%\ccwin-notify\secret.token` に保存する
- トークンはファイルには平文で保存される (CLI / TUI が読み込んでリクエストに付与するため)
- デーモン内部ではトークンを SHA-256 ハッシュ化した 32 byte のみを保持する

### リクエスト検証

- 全 API エンドポイント (`/v1/events`, `/v1/events/stream` 等) は `Authorization: Bearer <token>` ヘッダーを必須とする
- ヘッダーが欠落・複数存在・プレフィクス不一致の場合は即座に HTTP 401 を返す
- 受信した Bearer トークンを SHA-256 ハッシュ化 (32 byte 固定長) してから `subtle.ConstantTimeCompare` で比較する
  - SHA-256 化により双方が 32 byte 固定長になりタイミング攻撃によるトークン長の推定を防ぐ
  - `/v1/healthz` のみ認証不要 (ヘルスチェック用)

---

## アクセス制御 (ACL)

### ファイルパスと権限

| パス | 内容 | 目標権限 |
|------|------|----------|
| `%APPDATA%\ccwin-notify\` | アプリデータディレクトリ (secret.token / daemon.port を保持) | 現ユーザーのみフルコントロール |
| `%APPDATA%\ccwin-notify\secret.token` | Bearer トークン (平文) | 現ユーザーのみ読み取り可能 |
| `%APPDATA%\ccwin-notify\daemon.port` | portfile (JSON) | 現ユーザーのみ読み取り可能 |
| `$XDG_CONFIG_HOME\ccwin-notify\` または `%USERPROFILE%\.config\ccwin-notify\` | 設定ファイル用ディレクトリ。`ccwin daemon` 起動時に `auth.EnsureDirACL` で DACL を強制適用 (`%APPDATA%\ccwin-notify\` と同じ規則) | 現ユーザーのみフルコントロール |
| 上記ディレクトリ配下の `config.toml` | 設定ファイル (Webhook URL 等を含む)。`ccwin init` / `ccwin daemon` 自動生成いずれも 0600 で書き出す | 親ディレクトリ DACL に依拠して他ユーザーから保護 |

ACL は Windows DACL (Dynamic Access Control List) で設定する。
現ユーザーの SID を動的に取得し、他ユーザーからの読み取りを禁止する (D-24 / D-25)。

`internal/auth.LoadOrCreate` / `EnsureDirACL` で v0.1.0 に実装済み (`auth_windows.go`)。

**fail-closed 設計**: トークン生成直後に ACL 適用が失敗した場合、トークンファイルを削除してエラーを返すため、
デーモンは ACL なしで起動しない。既存ファイル読み込み時も DACL を再検証する。

> **v0.1.0 の制限**: Windows 実機での DACL assert テストは未実施。実環境での動作確認は継続サイクルで実施予定。

### ネットワーク bind

- IPC サーバーは `127.0.0.1` 単一 IP にのみバインドする (A6 / I2)
- 外部 NIC へのバインドはコード上禁止されており、設定変更もできない
- `config.Validate()` は `net.IPv4(127,0,0,1)` との完全一致判定。`::1` や
  `127.0.0.0/8` の他アドレスは起動時に拒否される (M-03)。

---

## シークレットマスキング

`internal/secret.SecretString` 型は以下の 2 層でシークレット漏洩を防ぐ:

1. **slog.LogValuer 実装**: slog 経由のログ出力で自動的に `***` に置換される。
2. **MarshalJSON 実装**: `json.Marshal` 経由の JSON 出力 (`/v1/status` レスポンス等) でも `"***"` にマスクされる。これにより `/v1/status` の `auth.token_value` フィールドが平文でクライアントに漏洩することを防ぐ。

対象:
- `config.toml` の Webhook URL (`notifiers.webhook.*.url`)
- Bearer トークン (ログ・JSON API レスポンスともにマスク対象)

`SecretString.Reveal()` を呼ぶことで平文を取得できるが、呼び出し箇所はアプリ内部の必要最小限に限定する。

---

## 外部依存のリスク

ccwin-notify が依存する主要な外部パッケージと信頼境界:

| パッケージ | 用途 | 信頼境界 |
|----------|------|----------|
| `github.com/BurntSushi/toml` | TOML 設定ファイル解析 | 設定ファイルからの入力をパースする。不正 TOML は行番号付きエラーで拒否 |
| `github.com/shirou/gopsutil/v4` | プロセス監視ポーリング | OS のプロセス情報を読み取り専用で参照。書き込みなし |
| `git.sr.ht/~jackmordaunt/go-toast` | Windows Toast 通知発火 (`toast_real_windows.go` で実装済み。CI/テスト時は `CCWIN_NOTIFY_USE_FAKE_TOASTER=1` で FakeToaster に切り替え可能) | Title / Body 文字列のみを渡す。外部 URL にはアクセスしない |
| `github.com/charmbracelet/bubbletea` | TUI フレームワーク | ターミナル I/O のみ。ネットワークアクセスなし |
| `golang.org/x/sys/windows` | Windows API 直接呼び出し | ACL 設定・Mutex・Sound 再生に使用 |

Webhook 送信では外部 HTTPS エンドポイントに Event データを POST する。
送信される内容は Event の `Kind`, `Title`, `Body` フィールドのみであり、
トークンや設定ファイルのパスは含まれない。

---

## 既知の制限と受容済みリスク

| 項目 | 状態 | 対応予定 |
|------|------|----------|
| ACL 設定 (`secret.token` / `daemon.port` / `config.toml` 親ディレクトリ) | `auth_windows.go` で実装済み (実機 DACL assert テストは未実施)。`%APPDATA%\ccwin-notify\` と `~/.config\ccwin-notify\` (もしくは `$XDG_CONFIG_HOME\ccwin-notify\`) の両方に DACL を強制 | 継続サイクルで検証 |
| bind_address 単一 IP 強制 | `net.IPv4(127,0,0,1)` 完全一致で実装済み (M-03)。`::1` / `127.0.0.0/8` の他は拒否 | 解消済 |
| Toast 実発火 | v0.1.0 実装済み (`toast_real_windows.go` / `go-toast` 依存追加済み)。CI 時は `CCWIN_NOTIFY_USE_FAKE_TOASTER=1` で FakeToaster を使用 | Windows 実機での動作確認は継続サイクルで実施 |
| IPC は localhost 限定のため、リモートからの操作は設計上不可能 | 意図的な制限 | 変更予定なし |

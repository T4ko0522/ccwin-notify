// internal/config パッケージのユニットテスト (サイクル 8 / D2 / D3 部分)
// テスト ID: T-053 (D2), T-074, T-075, T-076 (Webhook URL disabled Red), T-077 (line number assert)
// 受入条件: D2 / D3 / A5 / A6 / I1 / B6 — 設定ファイル無しでデフォルト値起動
package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/t4ko0522/ccwin-notify/internal/config"
	"github.com/t4ko0522/ccwin-notify/internal/event"
)

// T-074: config.Load("") がデフォルト値を返し Validate() を通過する (D2)
func TestConfig_Load_Default_NoFile(t *testing.T) {
	t.Parallel()
	// 存在しないパスを渡す = デフォルト値のみ
	cfg, err := config.Load("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("Load(nonexistent): %v", err)
	}
	if cfg == nil {
		t.Fatal("Load: cfg は nil であってはならない")
	}

	// Validate はデフォルト設定でパスするはず
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate (default): %v", err)
	}
}

// T-053 / D2: デフォルト設定値の確認 (Toast enabled / Hooks enabled)
func TestConfig_Load_Default_Values(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// デフォルト: Toast enabled
	if !cfg.Notifiers.Toast.Enabled {
		t.Error("デフォルト設定: Toast は enabled であるべき")
	}
	// デフォルト: Hooks enabled
	if !cfg.Sources.Hooks.Enabled {
		t.Error("デフォルト設定: Hooks は enabled であるべき")
	}
	// デフォルト: Queue capacity 256
	if cfg.Queue.Capacity != 256 {
		t.Errorf("デフォルト Queue Capacity: got %d, want 256", cfg.Queue.Capacity)
	}
	// デフォルト: DropOldest policy
	if cfg.Queue.Policy != event.DropOldest {
		t.Errorf("デフォルト Queue Policy: got %q, want %q", cfg.Queue.Policy, event.DropOldest)
	}
	// デフォルト: bind address 127.0.0.1
	if cfg.IPC.BindAddress != "127.0.0.1" {
		t.Errorf("デフォルト BindAddress: got %q, want \"127.0.0.1\"", cfg.IPC.BindAddress)
	}
}

// T-075 / A5: Sources 両方 disabled の Config → Validate は ErrSourcesAllDisabled を返す
func TestConfig_Validate_BothSourcesDisabled_Warning(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 両方 disable に設定
	cfg.Sources.Hooks.Enabled = false
	cfg.Sources.Process.Enabled = false

	// A5 MUST: 両方 disabled のとき Validate は ErrSourcesAllDisabled を返す
	err = cfg.Validate()
	if err == nil {
		t.Error("A5: Sources 両方 disabled のとき Validate は ErrSourcesAllDisabled を返すべきだが nil だった")
		return
	}
	// errors.Is で sentinel を確認
	if !errors.Is(err, config.ErrSourcesAllDisabled) {
		t.Errorf("A5: errors.Is(err, ErrSourcesAllDisabled) = false; got: %v", err)
	}
}

// bind_address が 127.0.0.1 以外の場合 → Validate がエラーを返す (A6)
func TestConfig_Validate_NonLoopbackBind(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.IPC.BindAddress = "0.0.0.0" // 非ループバック

	if err := cfg.Validate(); err == nil {
		t.Error("bind_address=0.0.0.0: Validate はエラーを返すべきだが nil だった")
	}
}

// TOML ファイルから設定を読み込むテスト
func TestConfig_Load_FromTOML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")

	tomlContent := `
log_level = "debug"
log_format = "json"
shutdown_timeout = "10s"

[queue]
capacity = 100
policy = "drop-newest"

[ipc]
bind_address = "127.0.0.1"

[sources.hooks]
enabled = true

[sources.process]
enabled = false

[notifiers.toast]
enabled = true

[notifiers.sound]
enabled = false

[notifiers.webhook.discord]
enabled = false

[notifiers.webhook.slack]
enabled = false
`
	if err := os.WriteFile(configPath, []byte(tomlContent), 0600); err != nil {
		t.Fatalf("TOML ファイル書き込み: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load(toml): %v", err)
	}

	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel: got %q, want \"debug\"", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat: got %q, want \"json\"", cfg.LogFormat)
	}
	if cfg.Queue.Capacity != 100 {
		t.Errorf("Queue.Capacity: got %d, want 100", cfg.Queue.Capacity)
	}
	if cfg.Queue.Policy != event.DropNewest {
		t.Errorf("Queue.Policy: got %q, want %q", cfg.Queue.Policy, event.DropNewest)
	}
}

// T-077 / D3: 不正な TOML で行番号付きエラーが返る
// 3_contract.md: "戻り値の error は行番号付き (BurntSushi/toml の ParseError)"
func TestConfig_Load_InvalidTOML_LineNumber(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "bad_config.toml")

	// 3行目に構文エラーを意図的に挿入
	badTOML := `log_level = "debug"
log_format = "json"
invalid line without = sign
[queue]
capacity = 100
`
	if err := os.WriteFile(configPath, []byte(badTOML), 0600); err != nil {
		t.Fatalf("bad TOML ファイル書き込み: %v", err)
	}

	_, err := config.Load(configPath)
	if err == nil {
		t.Fatal("不正TOML: エラーが返るべきだが nil だった")
	}

	// D3: BurntSushi/toml ParseError 型として取り出せること
	var parseErr toml.ParseError
	if errors.As(err, &parseErr) {
		// ParseError が取り出せた場合: 行番号が 0 でないことを assert
		// ParseError.Line は 1-indexed
		t.Logf("ParseError.Position.Line=%d", parseErr.Position.Line)
		if parseErr.Position.Line == 0 {
			t.Error("D3: ParseError.Position.Line が 0 (行番号なし) — BurntSushi/toml は行番号を含むはず")
		}
	} else {
		// ParseError として取り出せない場合: エラーメッセージに行番号情報が含まれること
		errMsg := err.Error()
		t.Logf("D3 エラー (ParseError 非取得): %v", errMsg)
		if !strings.Contains(errMsg, "line") && !strings.Contains(errMsg, "Line") &&
			!strings.Contains(errMsg, "3") {
			t.Errorf("D3: エラーメッセージに行番号情報 (\"line\" or \"3\") が含まれていない: %q", errMsg)
		}
	}
}

// T-076 / I1 / B6: Webhook URL が disabled でも不正URL (http://) なら Validate がエラーを返す (Red)
// 3_contract.md バリデーション規則: "webhook.*.url (値が空でない場合、常時) https:// で始まる、url.Parse 成功"
// 現実装は enabled=true の場合のみ検証するため、このテストは Red (失敗) になる。
// at-implementer が disabled でも URL 検証するよう修正してから green になる。
func TestConfig_Validate_Webhook_DisabledButInvalidURL_Rejected(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// disabled=false でも URL が http:// なら拒否されるべき
	cfg.Notifiers.Webhook.Discord.Enabled = false
	cfg.Notifiers.Webhook.Discord.URL = "http://discord.com/webhook/invalid"

	err = cfg.Validate()
	if err == nil {
		t.Error("I1/B6: disabled でも http:// URL の場合 Validate はエラーを返すべきだが nil だった (Red)")
	}
}

// T-076b / I1 / B6: Slack Webhook URL が disabled でも不正URL なら拒否 (Red)
func TestConfig_Validate_SlackWebhook_DisabledButInvalidURL_Rejected(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Notifiers.Webhook.Slack.Enabled = false
	cfg.Notifiers.Webhook.Slack.URL = "http://hooks.slack.com/services/invalid"

	err = cfg.Validate()
	if err == nil {
		t.Error("I1/B6 Slack: disabled でも http:// URL の場合 Validate はエラーを返すべきだが nil だった (Red)")
	}
}

// T-076c / I1: enabled=true かつ URL が空文字の場合はエラー (webhook_url_required)
func TestConfig_Validate_Webhook_EnabledEmptyURL_Rejected(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Notifiers.Webhook.Discord.Enabled = true
	cfg.Notifiers.Webhook.Discord.URL = "" // 空文字

	err = cfg.Validate()
	if err == nil {
		t.Error("I1: enabled=true かつ URL 空文字の場合 Validate はエラーを返すべきだが nil だった")
	}
}

// enabled=true かつ正当な https:// URL は通過する
func TestConfig_Validate_Webhook_EnabledValidURL_Passes(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Notifiers.Webhook.Discord.Enabled = true
	cfg.Notifiers.Webhook.Discord.URL = "https://discord.com/api/webhooks/valid-endpoint"

	err = cfg.Validate()
	if err != nil {
		t.Errorf("I1: enabled=true かつ正当 https:// URL は Validate を通過すべきだが: %v", err)
	}
}

// T-164: toast.kind_mask に不正な EventKind を含む場合 Validate がエラーを返す
func TestConfig_Validate_KindMask_InvalidKind(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 不正な kind_mask
	cfg.Notifiers.Toast.KindMask = map[event.EventKind]bool{
		"UnknownKindXyz": true,
	}

	err = cfg.Validate()
	if err == nil {
		t.Error("KindMask に不正 Kind: Validate はエラーを返すべきだが nil だった")
	}
	if !strings.Contains(err.Error(), "UnknownKindXyz") {
		t.Errorf("エラーに不正 Kind 名が含まれていない: %v", err)
	}
}

// T-165: sound.kind_mask に不正な EventKind を含む場合 Validate がエラーを返す
func TestConfig_Validate_SoundKindMask_InvalidKind(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Notifiers.Sound.KindMask = map[event.EventKind]bool{
		"BadKind": true,
	}

	err = cfg.Validate()
	if err == nil {
		t.Error("Sound KindMask に不正 Kind: Validate はエラーを返すべきだが nil だった")
	}
}

// T-166: Validate でキュー capacity < 1 がエラーになる
func TestConfig_Validate_QueueCapacity_TooSmall(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Queue.Capacity = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Queue.Capacity=0: Validate はエラーを返すべきだが nil だった")
	}
}

// T-167: Validate で不正な log_level がエラーになる
func TestConfig_Validate_InvalidLogLevel(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.LogLevel = "verbose" // 不正
	if err := cfg.Validate(); err == nil {
		t.Error("LogLevel=verbose: Validate はエラーを返すべきだが nil だった")
	}
}

// T-168: Validate で不正な queue policy がエラーになる
func TestConfig_Validate_InvalidQueuePolicy(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Queue.Policy = "invalid-policy"
	if err := cfg.Validate(); err == nil {
		t.Error("Queue.Policy=invalid: Validate はエラーを返すべきだが nil だった")
	}
}

// T-169: Validate で log_format が不正な場合エラーになる
func TestConfig_Validate_InvalidLogFormat(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.LogFormat = "yaml" // 不正
	if err := cfg.Validate(); err == nil {
		t.Error("LogFormat=yaml: Validate はエラーを返すべきだが nil だった")
	}
}

// T-170: Validate で shutdown_timeout <= 0 がエラーになる
func TestConfig_Validate_ShutdownTimeout_Zero(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.ShutdownTimeout = 0
	if err := cfg.Validate(); err == nil {
		t.Error("ShutdownTimeout=0: Validate はエラーを返すべきだが nil だった")
	}
}

// T-171: Validate で dispatcher.max_concurrent_per_notifier が範囲外 (>256) のときエラー
func TestConfig_Validate_Dispatcher_MaxConcurrent_OutOfRange(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Dispatcher.MaxConcurrentPerNotifier = 300 // 256 超
	if err := cfg.Validate(); err == nil {
		t.Error("MaxConcurrentPerNotifier=300: Validate はエラーを返すべきだが nil だった")
	}
}

// T-172: Validate で webhook.discord.timeout が短すぎる場合エラー
func TestConfig_Validate_Webhook_Timeout_TooShort(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Discord を有効化して正しい URL を設定
	cfg.Notifiers.Webhook.Discord.Enabled = true
	cfg.Notifiers.Webhook.Discord.URL = "https://discord.com/api/webhooks/test"
	cfg.Notifiers.Webhook.Discord.Timeout = 10 * time.Millisecond // 100ms 未満
	if err := cfg.Validate(); err == nil {
		t.Error("Timeout=10ms: Validate はエラーを返すべきだが nil だった")
	}
}

// T-173: Validate で webhook.discord.max_retries が範囲外 (>10) のときエラー
func TestConfig_Validate_Webhook_MaxRetries_OutOfRange(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Notifiers.Webhook.Discord.Enabled = true
	cfg.Notifiers.Webhook.Discord.URL = "https://discord.com/api/webhooks/test"
	cfg.Notifiers.Webhook.Discord.MaxRetries = 15 // 10 超
	if err := cfg.Validate(); err == nil {
		t.Error("MaxRetries=15: Validate はエラーを返すべきだが nil だった")
	}
}

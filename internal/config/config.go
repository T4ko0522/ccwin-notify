// Package config は ccwin-notify の設定読み込みと検証を提供する。
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/secret"
)

// Default は呼び出し側が defaultConfig() に相当する初期値を取得するためのエクスポート関数。
func Default() *Config { return defaultConfig() }

// Config は ccwin-notify の設定全体を表す。
type Config struct {
	LogLevel        string           `toml:"log_level"`
	LogFormat       string           `toml:"log_format"`
	ShutdownTimeout time.Duration    `toml:"shutdown_timeout"`
	Queue           QueueConfig      `toml:"queue"`
	Dispatcher      DispatcherConfig `toml:"dispatcher"`
	IPC             IPCConfig        `toml:"ipc"`
	Sources         SourcesConfig    `toml:"sources"`
	Notifiers       NotifiersConfig  `toml:"notifiers"`
}

// DispatcherConfig は Dispatcher の設定。
type DispatcherConfig struct {
	MaxConcurrentPerNotifier int           `toml:"max_concurrent_per_notifier"`
	NotifierTimeout          time.Duration `toml:"notifier_timeout"`
}

// QueueConfig は EventBus の設定。
type QueueConfig struct {
	Capacity int              `toml:"capacity"`
	Policy   event.DropPolicy `toml:"policy"`
}

// IPCConfig は IPC 接続の設定。
type IPCConfig struct {
	BindAddress string `toml:"bind_address"`
}

// SourcesConfig は EventSource の設定。
type SourcesConfig struct {
	Hooks      HooksConfig      `toml:"hooks"`
	Process    ProcessConfig    `toml:"process"`
	Sessionlog SessionlogConfig `toml:"sessionlog"`
	Codexlog   CodexlogConfig   `toml:"codexlog"`
	Wezterm    WezTermConfig    `toml:"wezterm"`
}

// HooksConfig は Hooks ソースの設定。
type HooksConfig struct {
	Enabled bool `toml:"enabled"`
}

// ProcessConfig はプロセス監視ソースの設定。
type ProcessConfig struct {
	Enabled       bool          `toml:"enabled"`
	Interval      time.Duration `toml:"interval"`
	ProcessName   string        `toml:"process_name"`
	IdleThreshold time.Duration `toml:"idle_threshold"`
}

// SessionlogConfig は Claude Code セッションログ (jsonl) 監視ソースの設定。
// Claude Code Hooks に依存せず、~/.claude/projects/*/*.jsonl の追記から
// assistant の応答完了 (stop_reason=end_turn / stop_sequence) を検知して
// Stop イベントを生成する経路 (D-01〜D-09 / [docs/plans/2026-05-15-sessionlog-source/2_plan.md])。
type SessionlogConfig struct {
	Enabled     bool   `toml:"enabled"`
	ProjectsDir string `toml:"projects_dir"`
	BodyMaxLen  int    `toml:"body_max_len"`
}

// CodexlogConfig は Codex CLI rollout JSONL 監視ソースの設定。
// Codex CLI が書き出す ~/.codex/sessions/**/*.jsonl の final_answer 行から
// Stop イベントを生成する。Codex 0.134.0 時点で Claude Code Hooks 相当の
// 公開 event hook がないため、永続化ログを監視する。
type CodexlogConfig struct {
	Enabled      bool          `toml:"enabled"`
	SessionsDir  string        `toml:"sessions_dir"`
	BodyMaxLen   int           `toml:"body_max_len"`
	PollInterval time.Duration `toml:"poll_interval"`
}

// WezTermConfig は WezTerm ターミナル監視ソースの設定。
// `wezterm cli get-text` で pane 内描画テキストを定期取得し、Claude Code の
// 入力待ち UI シグネチャ (既定 "Enter to select") を検出して
// Notification イベントを発火する。AskUserQuestion / ExitPlanMode は
// hook も jsonl も即時通知できないため、現状で唯一の即時検知手段。
type WezTermConfig struct {
	Enabled      bool          `toml:"enabled"`
	PaneID       int           `toml:"pane_id"`
	PollInterval time.Duration `toml:"poll_interval"`
	Signature    string        `toml:"signature"`
}

// NotifiersConfig は Notifier 群の設定。
type NotifiersConfig struct {
	Toast   ToastConfig   `toml:"toast"`
	Sound   SoundConfig   `toml:"sound"`
	Webhook WebhookConfig `toml:"webhook"`
}

// ToastConfig は Toast Notifier の設定。
type ToastConfig struct {
	Enabled  bool                     `toml:"enabled"`
	KindMask map[event.EventKind]bool `toml:"kind_mask"`
}

// SoundConfig は Sound Notifier の設定。
type SoundConfig struct {
	Enabled  bool                     `toml:"enabled"`
	WavPath  string                   `toml:"wav_path"`
	KindMask map[event.EventKind]bool `toml:"kind_mask"`
}

// WebhookConfig は Webhook Notifier の設定。
type WebhookConfig struct {
	Discord WebhookEndpoint `toml:"discord"`
	Slack   WebhookEndpoint `toml:"slack"`
}

// WebhookEndpoint は個別 Webhook エンドポイントの設定。
type WebhookEndpoint struct {
	Enabled    bool                     `toml:"enabled"`
	URL        secret.SecretString      `toml:"url"`
	Timeout    time.Duration            `toml:"timeout"`
	MaxRetries int                      `toml:"max_retries"`
	KindMask   map[event.EventKind]bool `toml:"kind_mask"`
}

// defaultConfig はデフォルト設定を返す。
func defaultConfig() *Config {
	return &Config{
		LogLevel:        "info",
		LogFormat:       "text",
		ShutdownTimeout: 5 * time.Second, // plan §3.5 / §5.2 デフォルト 5s
		Queue: QueueConfig{
			Capacity: 256,
			Policy:   event.DropOldest,
		},
		Dispatcher: DispatcherConfig{
			MaxConcurrentPerNotifier: 4,
			NotifierTimeout:          3 * time.Second, // plan §3.5 デフォルト 3s (E4)
		},
		IPC: IPCConfig{
			BindAddress: "127.0.0.1",
		},
		Sources: SourcesConfig{
			Hooks: HooksConfig{
				Enabled: true,
			},
			Process: ProcessConfig{
				// Process Source は未実装 (internal/source/process パッケージ未追加)。
				// default は false とし /v1/status が嘘の "running" を返さないようにする (impl-opus MUST)。
				Enabled:       false,
				Interval:      2 * time.Second,
				ProcessName:   "claude.exe",
				IdleThreshold: 60 * time.Second,
			},
			Sessionlog: SessionlogConfig{
				// 既存ユーザーの挙動を壊さないため既定 disabled (A7)。
				// 利用時は config.toml で enabled=true を明示する。
				Enabled:    false,
				BodyMaxLen: 200,
			},
			Codexlog: CodexlogConfig{
				// Codex CLI のローカル履歴はユーザーごとの環境差があるため既定 disabled。
				// 利用時は config.toml で enabled=true を明示する。
				Enabled:      false,
				BodyMaxLen:   200,
				PollInterval: time.Second,
			},
			Wezterm: WezTermConfig{
				// WezTerm 専用機能のため既定 disabled。
				// 利用時は config.toml で enabled=true を明示する。
				Enabled:      false,
				PaneID:       0,
				PollInterval: time.Second,
			},
		},
		Notifiers: NotifiersConfig{
			Toast: ToastConfig{
				Enabled: true,
			},
			Sound: SoundConfig{
				// 同梱の default.wav (internal/notifier/sound/assets/default.wav) を
				// 使用するため、WavPath 未指定でもデフォルトで有効化する。
				Enabled: true,
			},
			Webhook: WebhookConfig{
				Discord: WebhookEndpoint{
					Enabled:    false,
					Timeout:    3 * time.Second, // plan §3.5 Webhook.Timeout デフォルト 3s
					MaxRetries: 3,
				},
				Slack: WebhookEndpoint{
					Enabled:    false,
					Timeout:    3 * time.Second,
					MaxRetries: 3,
				},
			},
		},
	}
}

// DefaultPath は config.toml の既定パスを返す。
//   - $XDG_CONFIG_HOME が設定されていれば $XDG_CONFIG_HOME/ccwin-notify/config.toml
//   - そうでなければ <UserHomeDir>/.config/ccwin-notify/config.toml (Windows では %USERPROFILE%\.config\ccwin-notify\config.toml)
//
// ホームディレクトリが解決できない場合は空文字を返す (呼び出し側で扱う)。
func DefaultPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ccwin-notify", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "ccwin-notify", "config.toml")
}

// Load は設定ファイルを読み込む。
// 1) 指定パス (--config) 2) [DefaultPath] 3) デフォルトのみ の順で解決。
// ファイルが存在しない場合はデフォルト値のみを返す (エラーなし)。
// 不正 TOML の場合は行番号付きエラーを返す。
func Load(path string) (*Config, error) {
	cfg := defaultConfig()

	resolvedPath := path
	if resolvedPath == "" {
		resolvedPath = DefaultPath()
	}

	if resolvedPath == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", resolvedPath, err)
	}

	if _, err := toml.Decode(string(data), cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", resolvedPath, err)
	}

	return cfg, nil
}

// ErrInvalidBindAddress は bind_address が 127.0.0.1 以外の場合に返る。
var ErrInvalidBindAddress = errors.New("config: bind_address must be 127.0.0.1")

// ErrSourcesAllDisabled は全 Source が disabled の場合に返る (A5)。
var ErrSourcesAllDisabled = errors.New("config: all sources are disabled (no events will be generated)")

// Validate はバインドアドレス / URL HTTPS / 列挙値 / 必須項目をチェック (plan §6.2 全規則)。
func (c *Config) Validate() error {
	// log_level 列挙値
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: log_level must be debug/info/warn/error, got %q", c.LogLevel)
	}

	// log_format 列挙値
	switch c.LogFormat {
	case "json", "text":
	default:
		return fmt.Errorf("config: log_format must be json/text, got %q", c.LogFormat)
	}

	// shutdown_timeout > 0
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("config: shutdown_timeout must be > 0, got %v", c.ShutdownTimeout)
	}

	// bind_address は 127.0.0.1 完全一致 (A6 / I2 / M-03)。
	// IsLoopback() は ::1 / 127.0.0.0/8 を許してしまうため net.IPv4(127,0,0,1) と比較する。
	ip := net.ParseIP(c.IPC.BindAddress)
	if ip == nil || !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		return fmt.Errorf("%w: got %q (must be exactly \"127.0.0.1\")", ErrInvalidBindAddress, c.IPC.BindAddress)
	}

	// キュー容量は 1 以上
	if c.Queue.Capacity < 1 {
		return fmt.Errorf("config: queue.capacity must be >= 1, got %d", c.Queue.Capacity)
	}

	// DropPolicy 検証
	switch c.Queue.Policy {
	case event.DropOldest, event.DropNewest, event.DropBlock:
	default:
		return fmt.Errorf("config: queue.policy unknown value %q", c.Queue.Policy)
	}

	// dispatcher.max_concurrent_per_notifier: 1..256
	if c.Dispatcher.MaxConcurrentPerNotifier < 1 || c.Dispatcher.MaxConcurrentPerNotifier > 256 {
		return fmt.Errorf("config: dispatcher.max_concurrent_per_notifier must be 1..256, got %d", c.Dispatcher.MaxConcurrentPerNotifier)
	}

	// dispatcher.notifier_timeout: 100ms..5m
	if c.Dispatcher.NotifierTimeout < 100*time.Millisecond || c.Dispatcher.NotifierTimeout > 5*time.Minute {
		return fmt.Errorf("config: dispatcher.notifier_timeout must be 100ms..5m, got %v", c.Dispatcher.NotifierTimeout)
	}

	// sources.process.interval >= 100ms
	if c.Sources.Process.Interval > 0 && c.Sources.Process.Interval < 100*time.Millisecond {
		return fmt.Errorf("config: sources.process.interval must be >= 100ms, got %v", c.Sources.Process.Interval)
	}

	// sources.sessionlog.body_max_len: 0 (= default) または 1..4096
	if c.Sources.Sessionlog.BodyMaxLen < 0 || c.Sources.Sessionlog.BodyMaxLen > 4096 {
		return fmt.Errorf("config: sources.sessionlog.body_max_len must be 0..4096, got %d", c.Sources.Sessionlog.BodyMaxLen)
	}

	// sources.sessionlog.projects_dir: 指定済なら絶対パス必須
	if dir := c.Sources.Sessionlog.ProjectsDir; dir != "" && !filepath.IsAbs(dir) {
		return fmt.Errorf("config: sources.sessionlog.projects_dir must be an absolute path, got %q", dir)
	}

	if c.Sources.Codexlog.BodyMaxLen < 0 || c.Sources.Codexlog.BodyMaxLen > 4096 {
		return fmt.Errorf("config: sources.codexlog.body_max_len must be 0..4096, got %d", c.Sources.Codexlog.BodyMaxLen)
	}
	if dir := c.Sources.Codexlog.SessionsDir; dir != "" && !filepath.IsAbs(dir) {
		return fmt.Errorf("config: sources.codexlog.sessions_dir must be an absolute path, got %q", dir)
	}
	if c.Sources.Codexlog.PollInterval > 0 && c.Sources.Codexlog.PollInterval < 100*time.Millisecond {
		return fmt.Errorf("config: sources.codexlog.poll_interval must be >= 100ms, got %v", c.Sources.Codexlog.PollInterval)
	}

	// sources.wezterm.pane_id: 非負
	if c.Sources.Wezterm.PaneID < 0 {
		return fmt.Errorf("config: sources.wezterm.pane_id must be >= 0, got %d", c.Sources.Wezterm.PaneID)
	}

	// sources.wezterm.poll_interval: 0 (= default) or >= 100ms
	if c.Sources.Wezterm.PollInterval > 0 && c.Sources.Wezterm.PollInterval < 100*time.Millisecond {
		return fmt.Errorf("config: sources.wezterm.poll_interval must be >= 100ms, got %v", c.Sources.Wezterm.PollInterval)
	}

	// A5: 全 Source が disabled の場合はエラー (イベントが生成されない)
	if !c.Sources.Hooks.Enabled && !c.Sources.Process.Enabled &&
		!c.Sources.Sessionlog.Enabled && !c.Sources.Codexlog.Enabled && !c.Sources.Wezterm.Enabled {
		return ErrSourcesAllDisabled
	}

	// kind_mask のキー検証
	if err := validateKindMask(c.Notifiers.Toast.KindMask, "toast"); err != nil {
		return err
	}
	if err := validateKindMask(c.Notifiers.Sound.KindMask, "sound"); err != nil {
		return err
	}
	if err := validateKindMask(c.Notifiers.Webhook.Discord.KindMask, "webhook.discord"); err != nil {
		return err
	}
	if err := validateKindMask(c.Notifiers.Webhook.Slack.KindMask, "webhook.slack"); err != nil {
		return err
	}

	// Webhook URL は HTTPS 必須 — URL != "" の場合は常に検証 (enabled に関わらず)
	if err := validateWebhookEndpoint(c.Notifiers.Webhook.Discord, "discord"); err != nil {
		return err
	}
	if err := validateWebhookEndpoint(c.Notifiers.Webhook.Slack, "slack"); err != nil {
		return err
	}

	return nil
}

func validateWebhookEndpoint(ep WebhookEndpoint, name string) error {
	raw := ep.URL.Reveal()

	// URL が空でなければ url.Parse + scheme/host/private-IP 検証 (M-07)
	// URL 自体は平文露出させない (H-03 / I4)。エラーメッセージには name のみ含める。
	if raw != "" {
		u, perr := url.Parse(raw)
		if perr != nil {
			return fmt.Errorf("config: notifiers.webhook.%s.url is not a valid URL (value redacted)", name)
		}
		if u.Scheme != "https" {
			return fmt.Errorf("config: notifiers.webhook.%s.url must use scheme https (value redacted)", name)
		}
		if u.Host == "" {
			return fmt.Errorf("config: notifiers.webhook.%s.url is missing host (value redacted)", name)
		}
		// Host が IP リテラルの場合 private / loopback / link-local / multicast / unspecified を拒否
		hostname := u.Hostname()
		if ip := net.ParseIP(hostname); ip != nil {
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
				ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
				return fmt.Errorf("config: notifiers.webhook.%s.url host is a non-routable IP (value redacted)", name)
			}
		}
	}

	// enabled=true のとき URL は必須
	if ep.Enabled && raw == "" {
		return fmt.Errorf("config: notifiers.webhook.%s.url is required when enabled=true", name)
	}

	// timeout >= 100ms
	if ep.Timeout > 0 && ep.Timeout < 100*time.Millisecond {
		return fmt.Errorf("config: notifiers.webhook.%s.timeout must be >= 100ms, got %v", name, ep.Timeout)
	}

	// max_retries: 0..10
	if ep.MaxRetries < 0 || ep.MaxRetries > 10 {
		return fmt.Errorf("config: notifiers.webhook.%s.max_retries must be 0..10, got %d", name, ep.MaxRetries)
	}

	return nil
}

func validateKindMask(mask map[event.EventKind]bool, notifierName string) error {
	for k := range mask {
		if !event.ValidKinds[k] {
			return fmt.Errorf("config: notifiers.%s.kind_mask contains unknown EventKind %q", notifierName, k)
		}
	}
	return nil
}

// Package config は ccwin-notify の設定読み込みと検証を提供する。
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/secret"
)

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
	Hooks   HooksConfig   `toml:"hooks"`
	Process ProcessConfig `toml:"process"`
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
		},
		Notifiers: NotifiersConfig{
			Toast: ToastConfig{
				Enabled: true,
			},
			Sound: SoundConfig{
				Enabled: false,
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

// Load は設定ファイルを読み込む。
// 1) 指定パス (--config) 2) %APPDATA%/ccwin-notify/config.toml 3) デフォルトのみ の順で解決。
// ファイルが存在しない場合はデフォルト値のみを返す (エラーなし)。
// 不正 TOML の場合は行番号付きエラーを返す。
func Load(path string) (*Config, error) {
	cfg := defaultConfig()

	resolvedPath := path
	if resolvedPath == "" {
		appdata := os.Getenv("APPDATA")
		if appdata != "" {
			resolvedPath = filepath.Join(appdata, "ccwin-notify", "config.toml")
		}
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

// ErrSourcesAllDisabled は Hooks と Process の両 Source が disabled の場合に返る (A5)。
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

	// bind_address は 127.0.0.1 固定 (A6)
	ip := net.ParseIP(c.IPC.BindAddress)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%w: got %q", ErrInvalidBindAddress, c.IPC.BindAddress)
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

	// A5: 両 Source が disabled の場合はエラー (イベントが生成されない)
	if !c.Sources.Hooks.Enabled && !c.Sources.Process.Enabled {
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

	// URL が空でなければ HTTPS 検証 (enabled に関わらず)
	// URL 自体は平文露出させない (H-03 / I4)。エラーメッセージには name のみ含める。
	if raw != "" && !strings.HasPrefix(raw, "https://") {
		return fmt.Errorf("config: notifiers.webhook.%s.url must start with https:// (value redacted)", name)
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

package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"

	"github.com/t4ko0522/ccwin-notify/internal/config"
	"github.com/t4ko0522/ccwin-notify/internal/secret"
)

type initIO struct {
	out    io.Writer
	errOut io.Writer
}

type initFlags struct {
	force          bool
	path           string
	logLevel       string
	toast          string
	sound          string
	discordWebhook string
	slackWebhook   string
}

func runInit(args []string) {
	exit := runInitWith(args, initIO{
		out:    os.Stdout,
		errOut: os.Stderr,
	})
	if exit != 0 {
		os.Exit(exit)
	}
}

// runInitWith は init サブコマンドの本体を実行し、exit code を返す。
// 出力先を差し替え可能にしてテストから呼び出せるようにする。
//
// 仕様:
//   - 既存の config.toml があり --force が指定されていない場合は exit 1。
//   - そうでなければ defaultConfig をフラグで上書きし、Validate を通してから
//     0600 で書き出す。対話入力は一切行わない (CLI 専用)。
func runInitWith(args []string, ui initIO) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(ui.errOut)

	var flags initFlags
	fs.BoolVar(&flags.force, "force", false, "既存の config.toml がある場合に確認なしで上書きする")
	fs.StringVar(&flags.path, "path", "", "出力先パス (省略時は $XDG_CONFIG_HOME/ccwin-notify/config.toml、未設定なら ~/.config/ccwin-notify/config.toml)")
	fs.StringVar(&flags.logLevel, "log-level", "", "log_level を上書き (debug|info|warn|error)")
	fs.StringVar(&flags.toast, "toast", "", "Toast 通知の有効/無効 (true|false)")
	fs.StringVar(&flags.sound, "sound", "", "Sound 通知の有効/無効 (true|false)")
	fs.StringVar(&flags.discordWebhook, "discord-webhook", "", "Discord Webhook URL (https://...)。設定すると Discord は自動で有効化")
	fs.StringVar(&flags.slackWebhook, "slack-webhook", "", "Slack Webhook URL (https://...)。設定すると Slack は自動で有効化")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	outPath, err := resolveInitPath(flags.path)
	if err != nil {
		fmt.Fprintf(ui.errOut, "init: %v\n", err)
		return 1
	}

	if _, err := os.Stat(outPath); err == nil && !flags.force {
		fmt.Fprintf(ui.errOut, "init: %s は既に存在します。上書きするには --force を指定してください\n", outPath)
		return 1
	}

	cfg := config.Default()

	if err := applyInitFlags(cfg, flags); err != nil {
		fmt.Fprintf(ui.errOut, "init: %v\n", err)
		return 1
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(ui.errOut, "init: 設定値が不正です: %v\n", err)
		return 1
	}

	if err := config.Write(outPath, cfg); err != nil {
		fmt.Fprintf(ui.errOut, "init: 書き出しに失敗しました: %v\n", err)
		return 1
	}

	fmt.Fprintf(ui.out, "設定を保存しました: %s\n", outPath)
	return 0
}

// resolveInitPath は出力先を決定する。
//   - flag で指定があればそれを使う
//   - なければ [config.DefaultPath] ($XDG_CONFIG_HOME → ~/.config フォールバック)
//   - ホームディレクトリも環境変数も解決できなければエラー
func resolveInitPath(flagPath string) (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	p := config.DefaultPath()
	if p == "" {
		return "", fmt.Errorf("ホームディレクトリが解決できません (XDG_CONFIG_HOME も USERPROFILE も未設定) — --path で出力先を指定してください")
	}
	return p, nil
}

// applyInitFlags はフラグ値を cfg に反映する。空文字フラグは defaultConfig の値を維持。
func applyInitFlags(cfg *config.Config, f initFlags) error {
	if f.logLevel != "" {
		cfg.LogLevel = f.logLevel
	}
	if f.toast != "" {
		v, err := strconv.ParseBool(f.toast)
		if err != nil {
			return fmt.Errorf("--toast: %v は true|false で指定してください", f.toast)
		}
		cfg.Notifiers.Toast.Enabled = v
	}
	if f.sound != "" {
		v, err := strconv.ParseBool(f.sound)
		if err != nil {
			return fmt.Errorf("--sound: %v は true|false で指定してください", f.sound)
		}
		cfg.Notifiers.Sound.Enabled = v
	}
	if f.discordWebhook != "" {
		if err := validateWebhookURL(f.discordWebhook); err != nil {
			return fmt.Errorf("--discord-webhook: %v", err)
		}
		cfg.Notifiers.Webhook.Discord.Enabled = true
		cfg.Notifiers.Webhook.Discord.URL = secret.SecretString(f.discordWebhook)
	}
	if f.slackWebhook != "" {
		if err := validateWebhookURL(f.slackWebhook); err != nil {
			return fmt.Errorf("--slack-webhook: %v", err)
		}
		cfg.Notifiers.Webhook.Slack.Enabled = true
		cfg.Notifiers.Webhook.Slack.URL = secret.SecretString(f.slackWebhook)
	}
	return nil
}

// validateWebhookURL は config.validateWebhookEndpoint と同じ規則を CLI 入力に適用する。
// init フラグの Validate と最終 Config.Validate がズレるとユーザーの入力が無駄になるため
// 規則を一致させる (loopback / private / link-local / multicast / unspecified を拒否)。
func validateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("URL の形式が不正です: %v", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("URL は https:// で始まる必要があります")
	}
	if u.Host == "" {
		return fmt.Errorf("URL にホスト名が含まれていません")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("URL のホストが loopback / private / link-local / multicast / unspecified IP です")
		}
	}
	return nil
}

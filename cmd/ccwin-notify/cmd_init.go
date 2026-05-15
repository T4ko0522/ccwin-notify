package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/t4ko0522/ccwin-notify/internal/config"
	"github.com/t4ko0522/ccwin-notify/internal/secret"
)

type initIO struct {
	in     *bufio.Reader
	out    io.Writer
	errOut io.Writer
}

type initFlags struct {
	force bool
	path  string
}

func runInit(args []string) {
	exit := runInitWith(args, initIO{
		in:     bufio.NewReader(os.Stdin),
		out:    os.Stdout,
		errOut: os.Stderr,
	})
	if exit != 0 {
		os.Exit(exit)
	}
}

// runInitWith は init サブコマンドの本体を実行し、exit code を返す。
// stdin / stdout / stderr を差し替え可能にしてテストから呼び出せるようにする。
func runInitWith(args []string, ui initIO) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(ui.errOut)

	var flags initFlags
	fs.BoolVar(&flags.force, "force", false, "既存の config.toml がある場合に確認なしで上書きする")
	fs.StringVar(&flags.path, "path", "", "出力先パス (省略時は %APPDATA%\\ccwin-notify\\config.toml)")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	outPath, err := resolveInitPath(flags.path)
	if err != nil {
		fmt.Fprintf(ui.errOut, "init: %v\n", err)
		return 1
	}

	// 既存ファイルがあれば値を引き継ぐ。失敗してもデフォルトから始める。
	base, _ := config.Load(outPath)
	if base == nil {
		base, _ = config.Load("")
	}

	if _, err := os.Stat(outPath); err == nil && !flags.force {
		fmt.Fprintf(ui.out, "%s が既に存在します。上書きしますか? [y/N]: ", outPath)
		ans, rerr := readLine(ui.in)
		if rerr != nil {
			if errors.Is(rerr, errInitAborted) {
				fmt.Fprintln(ui.out, "中止しました。")
				return 0
			}
			fmt.Fprintf(ui.errOut, "init: stdin: %v\n", rerr)
			return 1
		}
		if !isYes(ans) {
			fmt.Fprintln(ui.out, "中止しました。")
			return 0
		}
	}

	cfg, err := runInitWizard(ui, base)
	if err != nil {
		if errors.Is(err, errInitAborted) {
			fmt.Fprintln(ui.out, "中止しました。")
			return 0
		}
		fmt.Fprintf(ui.errOut, "init: %v\n", err)
		return 1
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(ui.errOut, "init: 入力された設定が不正です: %v\n", err)
		return 1
	}

	if err := writeConfigTOML(outPath, cfg); err != nil {
		fmt.Fprintf(ui.errOut, "init: 書き出しに失敗しました: %v\n", err)
		return 1
	}

	fmt.Fprintf(ui.out, "設定を保存しました: %s\n", outPath)
	return 0
}

var errInitAborted = errors.New("init aborted")

// resolveInitPath は出力先を決定する。
//   - flag で指定があればそれを使う
//   - なければ %APPDATA%\ccwin-notify\config.toml
//   - APPDATA が無ければエラー (Windows 想定)
func resolveInitPath(flagPath string) (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		return "", fmt.Errorf("APPDATA が設定されていません (--path で出力先を指定してください)")
	}
	return filepath.Join(appdata, "ccwin-notify", "config.toml"), nil
}

// runInitWizard はウィザード本体。base を初期値として対話で更新した Config を返す。
func runInitWizard(ui initIO, base *config.Config) (*config.Config, error) {
	cfg := *base // 値コピー

	fmt.Fprintln(ui.out, "ccwin-notify の設定ウィザードを開始します。Enter でデフォルト値を使用します。")

	level, err := askChoice(ui, "ログレベル", []string{"debug", "info", "warn", "error"}, defaultIfEmpty(cfg.LogLevel, "info"))
	if err != nil {
		return nil, err
	}
	cfg.LogLevel = level

	toastEnabled, err := askBool(ui, "Windows Toast 通知を有効にする", cfg.Notifiers.Toast.Enabled)
	if err != nil {
		return nil, err
	}
	cfg.Notifiers.Toast.Enabled = toastEnabled

	soundEnabled, err := askBool(ui, "サウンド (WAV) 再生を有効にする", cfg.Notifiers.Sound.Enabled)
	if err != nil {
		return nil, err
	}
	cfg.Notifiers.Sound.Enabled = soundEnabled
	if soundEnabled {
		wav, err := askWavPath(ui, cfg.Notifiers.Sound.WavPath)
		if err != nil {
			return nil, err
		}
		cfg.Notifiers.Sound.WavPath = wav
	}

	cfg.Notifiers.Webhook.Discord, err = askWebhook(ui, "Discord", cfg.Notifiers.Webhook.Discord)
	if err != nil {
		return nil, err
	}

	cfg.Notifiers.Webhook.Slack, err = askWebhook(ui, "Slack", cfg.Notifiers.Webhook.Slack)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

func askWebhook(ui initIO, label string, ep config.WebhookEndpoint) (config.WebhookEndpoint, error) {
	enabled, err := askBool(ui, fmt.Sprintf("%s Webhook を有効にする", label), ep.Enabled)
	if err != nil {
		return ep, err
	}
	ep.Enabled = enabled
	if !enabled {
		return ep, nil
	}
	current := ep.URL.Reveal()
	for {
		raw, err := askString(ui, fmt.Sprintf("%s Webhook URL (https://...)", label), current)
		if err != nil {
			return ep, err
		}
		if err := validateWebhookURL(raw); err != nil {
			fmt.Fprintf(ui.out, "  -> %v 再入力してください。\n", err)
			current = raw
			continue
		}
		ep.URL = secret.SecretString(raw)
		return ep, nil
	}
}

// validateWebhookURL は config.validateWebhookEndpoint と同じ規則を適用する。
// ウィザードと最終 Validate() の判定がズレるとユーザーの入力がまるごと捨てられるため、
// 両者の規則は一致させる (loopback / private / link-local / multicast / unspecified を拒否)。
func validateWebhookURL(raw string) error {
	if raw == "" {
		return errors.New("URL が空です")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("URL の形式が不正です: %v", err)
	}
	if u.Scheme != "https" {
		return errors.New("URL は https:// で始まる必要があります")
	}
	if u.Host == "" {
		return errors.New("URL にホスト名が含まれていません")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return errors.New("URL のホストが loopback / private / link-local / multicast / unspecified IP です")
		}
	}
	return nil
}

func askWavPath(ui initIO, current string) (string, error) {
	for {
		raw, err := askString(ui, "WAV ファイルのフルパス", current)
		if err != nil {
			return "", err
		}
		if raw == "" {
			fmt.Fprintln(ui.out, "  -> パスが空です。再入力してください。")
			continue
		}
		if !strings.EqualFold(filepath.Ext(raw), ".wav") {
			fmt.Fprintln(ui.out, "  -> 拡張子が .wav ではありません。再入力してください。")
			current = raw
			continue
		}
		return raw, nil
	}
}

// askChoice は候補リストから 1 つを選ばせる。Enter で defaultVal が選ばれる。
func askChoice(ui initIO, label string, choices []string, defaultVal string) (string, error) {
	prompt := fmt.Sprintf("%s [%s] (default: %s): ", label, strings.Join(choices, "/"), defaultVal)
	for {
		fmt.Fprint(ui.out, prompt)
		line, err := readLine(ui.in)
		if err != nil {
			return "", err
		}
		if line == "" {
			return defaultVal, nil
		}
		for _, c := range choices {
			if strings.EqualFold(line, c) {
				return c, nil
			}
		}
		fmt.Fprintf(ui.out, "  -> %q は候補に含まれていません。\n", line)
	}
}

func askBool(ui initIO, label string, defaultVal bool) (bool, error) {
	def := "y/N"
	if defaultVal {
		def = "Y/n"
	}
	prompt := fmt.Sprintf("%s [%s]: ", label, def)
	for {
		fmt.Fprint(ui.out, prompt)
		line, err := readLine(ui.in)
		if err != nil {
			return false, err
		}
		if line == "" {
			return defaultVal, nil
		}
		switch strings.ToLower(line) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(ui.out, "  -> y / n で答えてください。")
	}
}

func askString(ui initIO, label, defaultVal string) (string, error) {
	if defaultVal != "" {
		fmt.Fprintf(ui.out, "%s (default: %s): ", label, defaultVal)
	} else {
		fmt.Fprintf(ui.out, "%s: ", label)
	}
	line, err := readLine(ui.in)
	if err != nil {
		return "", err
	}
	if line == "" {
		return defaultVal, nil
	}
	return line, nil
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && line != "" {
			return strings.TrimRight(line, "\r\n"), nil
		}
		if errors.Is(err, io.EOF) {
			return "", errInitAborted
		}
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func isYes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true
	}
	return false
}

func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// writeConfigTOML は cfg を path に TOML として書き出す。
// 親ディレクトリが無ければ作成する。
func writeConfigTOML(path string, cfg *config.Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	return nil
}

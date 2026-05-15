package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/t4ko0522/ccwin-notify/internal/config"
)

// runInitForTest は runInitWith をテスト用に呼び出して exit code と出力を返す。
func runInitForTest(t *testing.T, args []string) (stdout, stderr string, exit int) {
	t.Helper()
	var out, errBuf bytes.Buffer
	exit = runInitWith(args, initIO{out: &out, errOut: &errBuf})
	return out.String(), errBuf.String(), exit
}

// 何もフラグを渡さなければデフォルト設定の TOML が書き出され、Validate を通過する。
func TestInit_NewFile_AllDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	stdout, stderr, exit := runInitForTest(t, []string{"--path", path})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
	}
	if !strings.Contains(stdout, "設定を保存しました") {
		t.Errorf("保存メッセージ無し: %q", stdout)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel: got %q want info", cfg.LogLevel)
	}
	if !cfg.Notifiers.Toast.Enabled {
		t.Error("Toast はデフォルト enabled")
	}
	if !cfg.Notifiers.Sound.Enabled {
		t.Error("Sound はデフォルト enabled")
	}
}

// --log-level / --toast / --sound フラグが反映される。
func TestInit_Flags_OverrideDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	args := []string{
		"--path", path,
		"--log-level", "warn",
		"--toast", "false",
		"--sound", "false",
	}
	stdout, stderr, exit := runInitForTest(t, args)
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel: got %q want warn", cfg.LogLevel)
	}
	if cfg.Notifiers.Toast.Enabled {
		t.Error("Toast: false 指定だが enabled のまま")
	}
	if cfg.Notifiers.Sound.Enabled {
		t.Error("Sound: false 指定だが enabled のまま")
	}
}

// --discord-webhook で URL が設定され Discord が enabled になる。
func TestInit_DiscordWebhook_SetsURLAndEnables(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	url := "https://discord.com/api/webhooks/abc/def"
	stdout, stderr, exit := runInitForTest(t, []string{"--path", path, "--discord-webhook", url})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !cfg.Notifiers.Webhook.Discord.Enabled {
		t.Error("Discord: webhook URL 指定で enabled になっていない")
	}
	if got := cfg.Notifiers.Webhook.Discord.URL.Reveal(); got != url {
		t.Errorf("Discord URL: got %q want %q", got, url)
	}
}

// http:// 等の不正な webhook URL は exit 1。
func TestInit_DiscordWebhook_InvalidURL_Rejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	stdout, stderr, exit := runInitForTest(t, []string{
		"--path", path,
		"--discord-webhook", "http://example.com/hook",
	})
	if exit == 0 {
		t.Fatalf("invalid URL なのに exit=0 stdout=%q", stdout)
	}
	if !strings.Contains(stderr, "discord-webhook") {
		t.Errorf("stderr に --discord-webhook の文脈が無い: %q", stderr)
	}
}

// loopback IP の webhook URL は exit 1 (Config.Validate と判定一致)。
func TestInit_DiscordWebhook_LoopbackRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	_, stderr, exit := runInitForTest(t, []string{
		"--path", path,
		"--discord-webhook", "https://127.0.0.1/hook",
	})
	if exit == 0 {
		t.Fatal("loopback URL なのに exit=0")
	}
	if !strings.Contains(stderr, "loopback") {
		t.Errorf("loopback 拒否メッセージが無い: %q", stderr)
	}
}

// 既存ファイルがあり --force 無しなら exit 1 (TUI 廃止のため対話確認は無い)。
func TestInit_ExistingFile_WithoutForce_Fails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(path, []byte("log_level = \"debug\"\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	stdout, stderr, exit := runInitForTest(t, []string{"--path", path})
	if exit == 0 {
		t.Fatalf("既存ファイル + --force 無しなのに exit=0 stdout=%q", stdout)
	}
	if !strings.Contains(stderr, "--force") {
		t.Errorf("stderr に --force の案内が無い: %q", stderr)
	}

	// 元ファイルが書き換わっていないこと
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), `log_level = "debug"`) {
		t.Errorf("ファイルが書き換わっている: %q", string(data))
	}
}

// --force があれば既存ファイルを上書きする。
func TestInit_ExistingFile_WithForce_Overwrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(path, []byte("log_level = \"debug\"\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, stderr, exit := runInitForTest(t, []string{"--path", path, "--force"})
	if exit != 0 {
		t.Fatalf("--force でも書き出し失敗: exit=%d stderr=%q", exit, stderr)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel: got %q want info (defaultConfig で上書きされていない)", cfg.LogLevel)
	}
}

// --log-level の値が defaultConfig の許容外なら Validate で弾かれて exit 1。
func TestInit_InvalidLogLevel_RejectedByValidate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	_, stderr, exit := runInitForTest(t, []string{"--path", path, "--log-level", "verbose"})
	if exit == 0 {
		t.Fatal("log_level=verbose なのに exit=0")
	}
	if !strings.Contains(stderr, "log_level") {
		t.Errorf("stderr に log_level の文脈が無い: %q", stderr)
	}
}

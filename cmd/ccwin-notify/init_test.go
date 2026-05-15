package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/t4ko0522/ccwin-notify/internal/config"
)

// runInitWith をスクリプト入力で呼び出し、出力先 TOML を Load + Validate で検証する。
func runInitForTest(t *testing.T, args []string, input string) (stdout, stderr string, exit int) {
	t.Helper()
	in := bufio.NewReader(strings.NewReader(input))
	var out, errBuf bytes.Buffer
	exit = runInitWith(args, initIO{in: in, out: &out, errOut: &errBuf})
	return out.String(), errBuf.String(), exit
}

func TestInit_NewFile_AllDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// 全項目で Enter (デフォルト) を打つ。Discord/Slack は default false なのでスキップされる。
	// 順番: log_level, toast, sound, discord, slack
	input := strings.Repeat("\n", 5)

	stdout, stderr, exit := runInitForTest(t, []string{"--path", path}, input)
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
		t.Error("Toast enabled であるべき")
	}
	if cfg.Notifiers.Sound.Enabled {
		t.Error("Sound はデフォルト disabled")
	}
}

func TestInit_SoundWithWavPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	wav := filepath.Join(dir, "notify.wav")

	// log_level=warn, toast=Enter(default y), sound=y + wav path, discord=Enter(N), slack=Enter(N)
	input := strings.Join([]string{
		"warn",
		"",
		"y",
		wav,
		"",
		"",
		"",
	}, "\n") + "\n"

	stdout, stderr, exit := runInitForTest(t, []string{"--path", path}, input)
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
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel: got %q want warn", cfg.LogLevel)
	}
	if !cfg.Notifiers.Sound.Enabled {
		t.Error("Sound は enabled であるべき")
	}
	if cfg.Notifiers.Sound.WavPath != wav {
		t.Errorf("WavPath: got %q want %q", cfg.Notifiers.Sound.WavPath, wav)
	}
}

func TestInit_DiscordURL_RetryOnInvalid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// log_level, toast, sound=n, discord=y, http URL (reject) -> https URL (accept), slack=n
	input := strings.Join([]string{
		"info",
		"y",
		"n",
		"y",
		"http://example.com/hook",
		"https://discord.com/api/webhooks/abc/def",
		"n",
	}, "\n") + "\n"

	stdout, stderr, exit := runInitForTest(t, []string{"--path", path}, input)
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
	}
	if !strings.Contains(stdout, "https://") {
		// プロンプトに「https://」を含む文字列が出ていることを軽く確認
		t.Logf("stdout: %s", stdout)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !cfg.Notifiers.Webhook.Discord.Enabled {
		t.Error("Discord enabled")
	}
	if got := cfg.Notifiers.Webhook.Discord.URL.Reveal(); got != "https://discord.com/api/webhooks/abc/def" {
		t.Errorf("Discord URL: got %q", got)
	}
}

func TestInit_DiscordURL_RejectsLoopbackIP(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// loopback IP を最初に渡してリトライ → 正しい URL でパスすることを確認。
	// 退行回避: ウィザードが config.Validate() より緩いと、ここで通過 → 最終 Validate で
	// exit 1 になりユーザーの全入力が捨てられる。
	input := strings.Join([]string{
		"info",
		"y",
		"n",
		"y",
		"https://127.0.0.1/hook",
		"https://discord.com/api/webhooks/abc/def",
		"n",
	}, "\n") + "\n"

	stdout, stderr, exit := runInitForTest(t, []string{"--path", path}, input)
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
	}
	if !strings.Contains(stdout, "loopback") {
		t.Errorf("loopback 拒否メッセージが見えない: %q", stdout)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := cfg.Notifiers.Webhook.Discord.URL.Reveal(); got != "https://discord.com/api/webhooks/abc/def" {
		t.Errorf("Discord URL: got %q", got)
	}
}

func TestInit_ExistingFile_DeclineOverwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// 事前にダミーファイルを置く
	if err := writeFile(path, "log_level = \"debug\"\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 上書きプロンプトで "n"
	stdout, stderr, exit := runInitForTest(t, []string{"--path", path}, "n\n")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	if !strings.Contains(stdout, "中止しました") {
		t.Errorf("中止メッセージ無し: %q", stdout)
	}

	// 書き換わっていないこと
	data, err := readFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(data, `log_level = "debug"`) {
		t.Errorf("ファイルが書き換わっている: %q", data)
	}
}

func TestInit_ForceFlag_SkipsConfirm(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := writeFile(path, "log_level = \"debug\"\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// --force なので上書き確認は来ない。defaults 5 連発のみ。
	input := strings.Repeat("\n", 5)
	stdout, stderr, exit := runInitForTest(t, []string{"--path", path, "--force"}, input)
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
	}
	if strings.Contains(stdout, "上書きしますか") {
		t.Errorf("--force でも確認プロンプトが出た: %q", stdout)
	}
}

func TestInit_InvalidChoice_Reprompts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// log_level に "verbose" (無効) -> "info" を入れて成功させる。残りは default。
	input := strings.Join([]string{
		"verbose",
		"info",
		"",
		"",
		"",
		"",
	}, "\n") + "\n"

	stdout, stderr, exit := runInitForTest(t, []string{"--path", path}, input)
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	if !strings.Contains(stdout, `"verbose"`) {
		t.Errorf("invalid choice の再プロンプトが見えない: %q", stdout)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// cmd/ccwin-notify の main パッケージのテスト
// テスト ID: T-060b / H2 — 非 Windows 起動時の exit code 検証
// 受入条件: H2 — GOOS != "windows" で起動拒否 (exit 非ゼロ + stderr メッセージ)
//
// H2 の検証方法:
//   - go test -run=TestMain_NonWindows_ExitCode を helper process として起動
//   - 子プロセスが "ccwin-notify is Windows-only" を stderr に出力し exit 非ゼロで終了することを確認
//
// NOTE: main.go は GOOS != "windows" で exit 2 を返す (実装済み)。
// 非 Windows 環境での exit 2 確認は main_non_windows_test.go (//go:build !windows) で行う。
package main_test

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// T-060b / H2: main バイナリが Windows 以外で実行された場合の動作を検証
// helper process パターン: 子プロセスとして自分自身を起動し、exit code と stderr を確認
func TestMain_WindowsOnly_Behavior(t *testing.T) {
	t.Parallel()

	// Windows 上では GOOS=windows で起動するので "subcommand required" で exit 1
	// 非 Windows GOOS チェックを直接テストするには、バイナリをビルドして実行する必要がある
	// このテストは main.go の GOOS チェックが存在することと、
	// 実行時に exit 非ゼロで終了することを確認する

	if runtime.GOOS == "windows" {
		// Windows では subcommand required で exit 1 になることを確認
		exe, err := buildTestBinary(t)
		if err != nil {
			t.Skipf("バイナリビルドスキップ: %v", err)
		}

		cmd := exec.Command(exe)
		output, err := cmd.CombinedOutput()

		exitCode := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}

		t.Logf("H2 Windows: exit=%d, output=%q", exitCode, string(output))

		// Windows では "subcommand required" で exit 1
		if exitCode == 0 {
			t.Error("H2: main は exit 0 であってはならない (サブコマンドなしで起動拒否)")
		}
		if !strings.Contains(string(output), "subcommand required") {
			t.Errorf("H2: stderr に \"subcommand required\" が含まれていない: %q", string(output))
		}
		return
	}

	// 非 Windows: GOOS チェックにより "Windows-only" エラーで終了するはず
	exe, err := buildTestBinary(t)
	if err != nil {
		t.Skipf("バイナリビルドスキップ: %v", err)
	}

	cmd := exec.Command(exe)
	output, err := cmd.CombinedOutput()

	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}

	t.Logf("H2 非Windows: exit=%d, output=%q", exitCode, string(output))

	if exitCode == 0 {
		t.Error("H2: 非 Windows では exit 非ゼロであるべきだが exit 0 だった")
	}
	if !strings.Contains(string(output), "Windows-only") {
		t.Errorf("H2: stderr に \"Windows-only\" が含まれていない: %q", string(output))
	}
}

// T-061b / H2 (Green): 非 Windows exit code は 2 — main.go は os.Exit(2) を実装済み
// Windows 環境では build tag !windows のテスト (main_non_windows_test.go) が代わりに検証する。
// このテストは Windows 上では GOOS チェックを介した間接確認のみ行う。
func TestMain_NonWindows_ExitCode(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		// Windows では exit 2 の直接確認は不可。
		// main_non_windows_test.go (//go:build !windows) が非 Windows CI で検証する。
		t.Skip("H2: 非 Windows exit code (exit 2) の確認は //go:build !windows のテストで行う")
	}

	// 非 Windows: exit code 2 を期待 (main.go は os.Exit(2) を実装済み)
	exe, err := buildTestBinary(t)
	if err != nil {
		t.Skipf("バイナリビルドスキップ: %v", err)
	}

	cmd := exec.Command(exe)
	err = cmd.Run()

	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}

	// H2 仕様: exit 2 (main.go 実装済み)
	if exitCode != 2 {
		t.Errorf("H2: exit code = %d, want 2", exitCode)
	}
}

// buildTestBinary はテスト用にバイナリをビルドする
func buildTestBinary(t *testing.T) (string, error) {
	t.Helper()
	dir := t.TempDir()
	exe := dir + "/ccwin-notify-test"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, ".")
	cmd.Dir = "."
	// go build のワーキングディレクトリは cmd/ccwin-notify
	// go test は package dir で実行されるので "." がcmd/ccwin-notifyを指す
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("go build output: %s", output)
		return "", err
	}
	// Cleanup で exe を削除
	t.Cleanup(func() {
		_ = os.Remove(exe)
	})
	return exe, nil
}

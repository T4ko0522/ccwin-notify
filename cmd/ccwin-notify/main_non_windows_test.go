//go:build !windows

// cmd/ccwin-notify 非 Windows 起動拒否テスト (Phase 3 Retry 2)
// テスト ID: T-119
// 受入条件: H2 — 非 Windows で起動した場合 exit 2 + "Windows-only" メッセージ
//
// このファイルは !windows build tag を使用するため、Windows CI では実行されない。
// Linux / macOS CI で実行することで H2 の非 Windows 分岐を実証する。
// Windows 環境では go test -tags !windows ... でのみ評価可能。
package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

// T-119 / H2: 非 Windows で ccwin-notify バイナリを実行すると exit 2 + "Windows-only" が stderr に出る
func TestMain_NonWindows_ExitCode2(t *testing.T) {
	t.Parallel()

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

	t.Logf("H2 非 Windows: exit=%d, output=%q", exitCode, string(output))

	// H2 仕様: exit 2
	if exitCode != 2 {
		t.Errorf("H2: exit code = %d, want 2 (GOOS!='windows' → exit 2)", exitCode)
	}

	// "Windows-only" が stderr に含まれること
	if !strings.Contains(string(output), "Windows-only") {
		t.Errorf("H2: \"Windows-only\" が出力に含まれていない: %q", string(output))
	}
}

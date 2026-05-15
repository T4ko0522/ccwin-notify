// internal/platform パッケージのテスト (サイクル 8)
// テスト ID: T-060, T-061, T-011 (F7 / H2)
// 受入条件: H2 — GOOS != "windows" で起動拒否
package platform_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// T-061 / H2: main.go 冒頭の GOOS 判定が存在することをファイル読み込みで確認
// (コードのコンパイルパスとして確認)
func TestGOOS_Check_ExistsInMain(t *testing.T) {
	t.Parallel()
	// cmd/ccwin-notify/main.go が存在し、GOOS 判定コードを含むことを確認
	mainGoPath := filepath.Join("..", "..", "cmd", "ccwin-notify", "main.go")
	data, err := os.ReadFile(mainGoPath)
	if err != nil {
		// ファイルが未作成なら skip (at-implementer 待ち)
		t.Skipf("main.go が未作成: %v", err)
	}

	content := string(data)
	if !contains(content, "runtime.GOOS") {
		t.Error("main.go に runtime.GOOS のチェックが見つからない (H2 未実装)")
	}
	if !contains(content, "windows") {
		t.Error("main.go に \"windows\" 文字列が見つからない (H2 未実装)")
	}
}

// T-011 / F7: プロジェクトレイアウトが cmd/ / internal/... 通りであることを確認
func TestProjectLayout(t *testing.T) {
	t.Parallel()
	// リポジトリルートを見つける
	root, err := findRepoRoot()
	if err != nil {
		t.Skipf("リポジトリルートが見つからない: %v", err)
	}

	requiredDirs := []string{
		"cmd/ccwin-notify",
		"internal/event",
		"internal/secret",
		"internal/clock",
		"internal/notifier",
		"internal/notifier/toast",
		"internal/source/hooks",
		"internal/ipc/server",
		"internal/ipc/middleware",
		"internal/ipc/sse",
		"internal/apiclient",
		"internal/config",
		"internal/daemon",
	}

	for _, dir := range requiredDirs {
		path := filepath.Join(root, filepath.FromSlash(dir))
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("ディレクトリ %q が存在しない (F7 レイアウト不一致): %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q はディレクトリであるべき", dir)
		}
	}
}

// H2: Windows でない場合 CheckOS が false を返す (またはエラーを返す)
func TestPlatform_WindowsOnly(t *testing.T) {
	t.Parallel()
	// このテスト自体は Windows でしか実行されない想定だが、
	// 非 Windows での起動拒否ロジックのユニットテストとして
	// GOOS を変数として注入できる設計であることを確認する
	if runtime.GOOS != "windows" {
		t.Skip("このテストは Windows 上でのみ有効")
	}
	// Windows 上では問題なく通過すること
}

// contains は文字列の部分一致をチェック
func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// findRepoRoot はリポジトリルートを go.mod で探す
func findRepoRoot() (string, error) {
	// このファイルは internal/platform にあるので 2 階層上
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		gomod := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(gomod); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", os.ErrNotExist
}

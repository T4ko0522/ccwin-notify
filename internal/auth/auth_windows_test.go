//go:build windows

// Package auth のテスト (Windows 環境) — Phase 3 Retry 2
// テスト ID: T-131〜T-134
// 受入条件: A7 / D-24 (secret.token 生成・読込・ACL)
package auth_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/t4ko0522/ccwin-notify/internal/auth"
)

// T-131 / D-24: LoadOrCreate がトークンファイルを新規生成し、非空の Token を返す
func TestLoadOrCreate_NewToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.token")

	tok, err := auth.LoadOrCreate(context.Background(), path)
	if err != nil {
		t.Fatalf("LoadOrCreate (new): %v", err)
	}
	if tok.Reveal() == "" {
		t.Error("Token は空であってはならない")
	}
	// base64url 32 バイト = 43 文字以上
	if len(tok.Reveal()) < 40 {
		t.Errorf("Token が短すぎる: got %d chars, want >= 40", len(tok.Reveal()))
	}
}

// T-132 / D-24: LoadOrCreate が既存トークンを読み込む (同一値を返す)
func TestLoadOrCreate_ExistingToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.token")

	// 1 回目: 新規生成
	tok1, err := auth.LoadOrCreate(context.Background(), path)
	if err != nil {
		t.Fatalf("LoadOrCreate (create): %v", err)
	}

	// 2 回目: 既存ファイルを読込
	tok2, err := auth.LoadOrCreate(context.Background(), path)
	if err != nil {
		t.Fatalf("LoadOrCreate (reload): %v", err)
	}

	if tok1.Reveal() != tok2.Reveal() {
		t.Errorf("Token 不一致: 1回目=%q, 2回目=%q", tok1.Reveal(), tok2.Reveal())
	}
}

// T-133 / D-24: LoadOrCreate がファイルに書き込まれた実際の Token 文字列を返す
func TestLoadOrCreate_TokenInFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.token")

	tok, err := auth.LoadOrCreate(context.Background(), path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}

	// ファイル内容と一致するか確認 (末尾改行は TrimSpace で除去済み)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	fileContent := strings.TrimSpace(string(data))
	if fileContent != tok.Reveal() {
		t.Errorf("ファイル内容 %q と Token %q が不一致", fileContent, tok.Reveal())
	}
}

// T-134 / D-25: EnsureDirACL がディレクトリを作成し ACL を適用する
func TestEnsureDirACL_CreatesDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "ccwin-notify")

	if err := auth.EnsureDirACL(targetDir); err != nil {
		t.Fatalf("EnsureDirACL: %v", err)
	}

	// ディレクトリが存在することを確認
	info, err := os.Stat(targetDir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("EnsureDirACL: ディレクトリが作成されなかった")
	}
}

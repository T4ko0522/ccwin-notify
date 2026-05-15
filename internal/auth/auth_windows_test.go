//go:build windows

// Package auth のテスト (Windows 環境) — Phase 3 Retry 2
// テスト ID: T-131〜T-134
// 受入条件: A7 / D-24 (secret.token 生成・読込・ACL)
package auth_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

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

// M-05: 弱い token (短い ASCII) を書き込んでおくと LoadOrCreate がエラーを返す
func TestLoadOrCreate_ExistingToken_TooShort_FailsClosed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.token")

	// 16 文字の弱い token (base64url charset だが decode しても < 32 bytes)
	if err := os.WriteFile(path, []byte("aaaaaaaaaaaaaaaa"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := auth.LoadOrCreate(context.Background(), path)
	if err == nil {
		t.Fatal("短い token を受け付けてしまった (fail-closed されていない)")
	}
	if !strings.Contains(err.Error(), "token format invalid") {
		t.Errorf("error message に \"token format invalid\" を含むべき: %v", err)
	}
}

// M-05: base64url 違反の文字を含む token は拒否
func TestLoadOrCreate_ExistingToken_BadCharset_FailsClosed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.token")

	// "!@#$" 等の base64url 外文字を含む長め (43 char) のトークン
	if err := os.WriteFile(path, []byte("!@#$%^&*()_+abcdefghijklmnopqrstuvwxyz12345"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := auth.LoadOrCreate(context.Background(), path)
	if err == nil {
		t.Fatal("不正 charset の token を受け付けてしまった")
	}
}

// captureLogs は slog.Default() の出力を bytes.Buffer に切り替えて取得する。
func captureLogs(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	prev := slog.Default()
	buf := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return buf, func() { slog.SetDefault(prev) }
}

// H-02: 既存ファイル + DACL OK → INFO ログ "DACL ok"
func TestLoadOrCreate_ExistingToken_DACL_OK_Logs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.token")

	if _, err := auth.LoadOrCreate(context.Background(), path); err != nil {
		t.Fatalf("LoadOrCreate (create): %v", err)
	}

	buf, restore := captureLogs(t)
	defer restore()

	if _, err := auth.LoadOrCreate(context.Background(), path); err != nil {
		t.Fatalf("LoadOrCreate (reload): %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "DACL ok") {
		t.Errorf("INFO 'DACL ok' ログが含まれない: %s", out)
	}
	if strings.Contains(out, "DACL drift detected") {
		t.Errorf("drift 検出されないはず: %s", out)
	}
}

// H-02: 既存ファイル + DACL drift → WARN + 修復ログ
func TestLoadOrCreate_ExistingToken_DACL_Drift_RepairLogs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.token")

	if _, err := auth.LoadOrCreate(context.Background(), path); err != nil {
		t.Fatalf("LoadOrCreate (create): %v", err)
	}

	// DACL を意図的に drift させる: NULL DACL を設定 (allow-all = 全員アクセス可)
	secInfo := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	// nil DACL を SetNamedSecurityInfo で渡すには PROTECTED フラグ無しで明示
	emptySD, err := windows.NewSecurityDescriptor()
	if err != nil {
		t.Fatalf("NewSecurityDescriptor: %v", err)
	}
	if err := emptySD.SetDACL(nil, true, false); err != nil {
		t.Fatalf("SetDACL: %v", err)
	}
	dacl, _, _ := emptySD.DACL()
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, secInfo, nil, nil, dacl, nil); err != nil {
		t.Fatalf("SetNamedSecurityInfo (drift): %v", err)
	}

	buf, restore := captureLogs(t)
	defer restore()

	if _, err := auth.LoadOrCreate(context.Background(), path); err != nil {
		t.Fatalf("LoadOrCreate after drift: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "DACL drift detected") {
		t.Errorf("WARN 'DACL drift detected' が出力されない: %s", out)
	}
	if !strings.Contains(out, "DACL repaired") {
		t.Errorf("INFO 'DACL repaired' が出力されない: %s", out)
	}
}

var _ = io.Discard // bytes / io 未使用警告回避

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

//go:build windows

// Package auth は secret.token の生成・読込と Windows ACL 設定を提供する。
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/t4ko0522/ccwin-notify/internal/secret"
)

// Token は base64url(32 byte random) の不変な値。SecretString として扱う (D-24)。
type Token = secret.SecretString

// LoadOrCreate は secret.token を読み込み、無ければ生成 + Windows ACL を設定する (A7 / D-24)。
//
// Fail-closed: 生成直後の ACL 適用失敗 → token ファイルを削除してエラー返却 (daemon 起動拒否)。
// 既存ファイル読込時も DACL 検証 → 現ユーザー以外のエントリがあれば修復、失敗時は起動拒否。
func LoadOrCreate(ctx context.Context, path string) (Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("auth: read token: %w", err)
		}
		// 新規生成
		return createToken(path)
	}

	// 既存ファイルを読込 — 末尾改行等を除去してから使用 (M3R-04: daemon/client 両方で TrimSpace)
	tok := Token(secret.SecretString(strings.TrimSpace(string(data))))
	// ACL 再適用 (修復)
	if err := applyFileACL(path); err != nil {
		return "", fmt.Errorf("auth: ACL repair failed for existing token: %w", err)
	}
	return tok, nil
}

// EnsureDirACL は %APPDATA%/ccwin-notify/ ディレクトリの DACL を強制適用する (D-25)。
// daemon 起動の最初期 (Config.Load 前) に呼ぶ。
func EnsureDirACL(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("auth: mkdir %s: %w", dir, err)
	}
	return applyFileACL(dir)
}

func createToken(path string) (Token, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: rand: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)

	if err := os.WriteFile(path, []byte(tok), 0600); err != nil {
		return "", fmt.Errorf("auth: write token: %w", err)
	}

	if err := applyFileACL(path); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("auth: ACL apply failed (token deleted, daemon refused): %w", err)
	}

	return Token(secret.SecretString(tok)), nil
}

// applyFileACL は現ユーザー SID のみフルアクセス DACL を path に適用する (D-24 / D-25)。
// SE_DACL_PROTECTED で継承遮断。
func applyFileACL(path string) error {
	// 現ユーザー SID を動的取得
	sid, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("auth: currentUserSID: %w", err)
	}

	// ACE: 現ユーザー SID に GENERIC_ALL (= フルアクセス)
	access := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}

	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{access}, nil)
	if err != nil {
		return fmt.Errorf("auth: ACLFromEntries: %w", err)
	}

	// SE_DACL_PROTECTED で継承遮断し DACL を設定
	secInfo := windows.SECURITY_INFORMATION(
		windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION,
	)
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		secInfo,
		nil, nil, acl, nil,
	); err != nil {
		return fmt.Errorf("auth: SetNamedSecurityInfo: %w", err)
	}

	return nil
}

// currentUserSID は現プロセスの実行ユーザー SID を動的取得する (D-24)。
func currentUserSID() (*windows.SID, error) {
	tok, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, fmt.Errorf("OpenCurrentProcessToken: %w", err)
	}
	defer tok.Close()

	tu, err := tok.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("GetTokenUser: %w", err)
	}

	sidCopy, err := tu.User.Sid.Copy()
	if err != nil {
		return nil, fmt.Errorf("SID.Copy: %w", err)
	}
	return sidCopy, nil
}

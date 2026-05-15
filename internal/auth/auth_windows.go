//go:build windows

// Package auth は secret.token の生成・読込と Windows ACL 設定を提供する。
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unsafe"

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
	// DACL を検証 — drift 検出時のみ修復し、修復後に再検証して失敗なら fail-closed (H-02)
	drift, reason, verifyErr := verifyFileACL(path)
	if verifyErr != nil {
		slog.Default().Warn("auth: token file DACL verification error, attempting repair",
			"path", path, "error", verifyErr.Error())
		drift = true
	}
	if drift {
		slog.Default().Warn("auth: token file DACL drift detected, repairing",
			"path", path, "reason", reason)
		if err := applyFileACL(path); err != nil {
			return "", fmt.Errorf("auth: ACL repair failed for existing token: %w", err)
		}
		if drift2, reason2, err := verifyFileACL(path); err != nil || drift2 {
			return "", fmt.Errorf("auth: ACL repair did not converge (drift=%v reason=%q err=%v)", drift2, reason2, err)
		}
		slog.Default().Info("auth: token file DACL repaired", "path", path)
	} else {
		slog.Default().Info("auth: token file DACL ok", "path", path)
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

// verifyFileACL は path の DACL が期待値 (現ユーザー SID のみ FA + protected) と一致するか検査する (H-02)。
// 戻り値: drift = 期待と異なる、reason = 違いの説明、err = 検査自体が失敗 (drift=true 扱い)。
func verifyFileACL(path string) (drift bool, reason string, err error) {
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return true, "GetNamedSecurityInfo failed", fmt.Errorf("GetNamedSecurityInfo: %w", err)
	}

	control, _, err := sd.Control()
	if err != nil {
		return true, "Control failed", fmt.Errorf("Control: %w", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return true, "DACL not protected (inheritable)", nil
	}

	dacl, _, err := sd.DACL()
	if err != nil {
		return true, "DACL access failed", fmt.Errorf("DACL: %w", err)
	}
	if dacl == nil {
		return true, "DACL is null (allow-all)", nil
	}

	if dacl.AceCount != 1 {
		return true, fmt.Sprintf("DACL has %d ACEs, want 1", dacl.AceCount), nil
	}

	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		return true, "GetAce(0) failed", fmt.Errorf("GetAce: %w", err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return true, fmt.Sprintf("ACE type %d is not ACCESS_ALLOWED", ace.Header.AceType), nil
	}

	wantSID, err := currentUserSID()
	if err != nil {
		return true, "currentUserSID failed", fmt.Errorf("currentUserSID: %w", err)
	}
	wantStr := wantSID.String()
	gotSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	gotStr := gotSID.String()
	if gotStr != wantStr {
		return true, fmt.Sprintf("ACE SID %s != current user %s", gotStr, wantStr), nil
	}
	// GENERIC_ALL を SetNamedSecurityInfo で渡すと OS が標準権限へ展開する (= FILE_ALL_ACCESS 0x1F01FF)。
	// どちらでも「全許可」とみなす。
	const fileAllAccess = 0x1F01FF
	mask := uint32(ace.Mask)
	if mask&windows.GENERIC_ALL == 0 && mask&fileAllAccess != fileAllAccess {
		return true, fmt.Sprintf("ACE mask 0x%x missing full access", mask), nil
	}
	return false, "ok", nil
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

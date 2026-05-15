//go:build !windows

// Package auth は secret.token の生成・読込と ACL 設定を提供する。
// 非 Windows ビルドはスタブ (daemon 起動時に GOOS チェックで先にエラー)。
package auth

import (
	"context"
	"fmt"

	"github.com/t4ko0522/ccwin-notify/internal/secret"
)

// Token は base64url(32 byte random) の不変な値。SecretString として扱う (D-24)。
type Token = secret.SecretString

// LoadOrCreate は非 Windows では常にエラーを返す。
func LoadOrCreate(_ context.Context, _ string) (Token, error) {
	return "", fmt.Errorf("auth: not supported on this OS")
}

// EnsureDirACL は非 Windows では常にエラーを返す。
func EnsureDirACL(_ string) error {
	return fmt.Errorf("auth: not supported on this OS")
}

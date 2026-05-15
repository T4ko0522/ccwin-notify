//go:build !windows

// Package auth のテスト (非 Windows 環境) — Phase 3 Retry 2
// テスト ID: T-135〜T-136
// 受入条件: H2 (非 Windows は常にエラー)
package auth_test

import (
	"context"
	"testing"

	"github.com/t4ko0522/ccwin-notify/internal/auth"
)

// T-135 / H2: 非 Windows では LoadOrCreate が常にエラーを返す
func TestLoadOrCreate_NonWindows_Error(t *testing.T) {
	t.Parallel()
	_, err := auth.LoadOrCreate(context.Background(), "/tmp/nonexistent.token")
	if err == nil {
		t.Error("非 Windows: LoadOrCreate は常にエラーを返すべきだが nil だった")
	}
}

// T-136 / H2: 非 Windows では EnsureDirACL が常にエラーを返す
func TestEnsureDirACL_NonWindows_Error(t *testing.T) {
	t.Parallel()
	err := auth.EnsureDirACL("/tmp/ccwin-notify")
	if err == nil {
		t.Error("非 Windows: EnsureDirACL は常にエラーを返すべきだが nil だった")
	}
}

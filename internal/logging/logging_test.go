// Package logging のテスト (Phase 3 Retry 2)
// テスト ID: T-123〜T-125
// 受入条件: G1 (text/json ログ切替) / D5 (SecretString マスク)
package logging_test

import (
	"testing"

	"github.com/t4ko0522/ccwin-notify/internal/config"
	"github.com/t4ko0522/ccwin-notify/internal/logging"
)

// T-123 / G1: LogFormat="text" で非 nil Logger が返る
func TestNew_TextFormat(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{LogLevel: "info", LogFormat: "text"}
	logger := logging.New(cfg)
	if logger == nil {
		t.Error("logging.New(text): nil が返った")
	}
}

// T-124 / G1: LogFormat="json" で非 nil Logger が返る
func TestNew_JSONFormat(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{LogLevel: "debug", LogFormat: "json"}
	logger := logging.New(cfg)
	if logger == nil {
		t.Error("logging.New(json): nil が返った")
	}
}

// T-125 / G1: 各 LogLevel (debug/info/warn/error) で Logger が返る
func TestNew_AllLogLevels(t *testing.T) {
	t.Parallel()
	for _, lvl := range []string{"debug", "info", "warn", "error", "unknown"} {
		lvl := lvl
		t.Run(lvl, func(t *testing.T) {
			t.Parallel()
			cfg := &config.Config{LogLevel: lvl, LogFormat: "text"}
			logger := logging.New(cfg)
			if logger == nil {
				t.Errorf("logging.New(level=%q): nil が返った", lvl)
			}
		})
	}
}

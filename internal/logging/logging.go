// Package logging は slog.Logger を構築する。
package logging

import (
	"log/slog"
	"os"

	"github.com/t4ko0522/ccwin-notify/internal/config"
)

// New は config に従って Handler を構築 (text / json 切替、G1)。
// SecretString 型フィールドは slog が LogValuer を自動解決してマスクする (D5 / G4)。
func New(cfg *config.Config) *slog.Logger {
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	return slog.New(handler)
}

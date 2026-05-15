package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Write は cfg を path に TOML として書き出す。親ディレクトリが無ければ作成する。
// パーミッションは 0600 (Webhook URL など機微情報を含む可能性があるため)。
func Write(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()
	enc := toml.NewEncoder(f)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("config: encode %s: %w", path, err)
	}
	return nil
}

// EnsureExists は path (空なら [DefaultPath]) に config.toml が無ければ
// [Default] を書き出す。既存ファイルがある場合は何もせず nil を返す。
// パスが解決できない (DefaultPath が "") 場合も nil を返す (Load 側でデフォルト値起動になる)。
func EnsureExists(path string) error {
	resolved := path
	if resolved == "" {
		resolved = DefaultPath()
	}
	if resolved == "" {
		return nil
	}
	if _, err := os.Stat(resolved); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("config: stat %s: %w", resolved, err)
	}
	return Write(resolved, Default())
}

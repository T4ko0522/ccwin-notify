package config_test

import (
	"os"
	"testing"
)

// TestMain は config_test 全体で XDG_CONFIG_HOME を一時ディレクトリに固定し、
// 実機の `~/.config\ccwin-notify\config.toml` を読み込まないように隔離する。
//
// config.Load("") は DefaultPath() (= $XDG_CONFIG_HOME/ccwin-notify/config.toml)
// を見るため、これを TempDir に向ければ存在しないパスとなりデフォルト値起動となる。
// t.Setenv は t.Parallel と併用できないため、プロセス全体で 1 度だけ設定する。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ccwin-notify-config-test-*")
	if err != nil {
		panic("config_test: MkdirTemp: " + err.Error())
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic("config_test: Setenv XDG_CONFIG_HOME: " + err.Error())
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

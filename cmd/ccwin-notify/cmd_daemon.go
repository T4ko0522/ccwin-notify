package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/t4ko0522/ccwin-notify/internal/auth"
	"github.com/t4ko0522/ccwin-notify/internal/config"
	"github.com/t4ko0522/ccwin-notify/internal/daemon"
)

func runDaemon(args []string) {
	var configPath string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--config" || args[i] == "-config" {
			configPath = args[i+1]
			i++
		}
	}

	// M-04: デフォルト config パス利用時、Load より前に
	//   1) %APPDATA%/ccwin-notify (secret.token / daemon.port 保持)
	//   2) config.toml の親ディレクトリ (~/.config/ccwin-notify/ 等)
	// の DACL を整える。これで起動直前に他ユーザーに書き換えられた場合の改ざん
	// リスクを下げ、Webhook URL 等の機微情報を含む config.toml を保護する。
	// --config <他パス> 指定時は呼び出し側の責任とみなしてスキップ。
	if configPath == "" {
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			if err := auth.EnsureDirACL(filepath.Join(appdata, "ccwin-notify")); err != nil {
				fmt.Fprintf(os.Stderr, "ACL preflight error: %v\n", err)
				os.Exit(4)
			}
		}
		if configDir := filepath.Dir(config.DefaultPath()); configDir != "" && configDir != "." {
			if err := auth.EnsureDirACL(configDir); err != nil {
				fmt.Fprintf(os.Stderr, "config ACL preflight error: %v\n", err)
				os.Exit(4)
			}
		}
		// 親ディレクトリ DACL 確立後に config.toml が無ければ defaultConfig を
		// 書き出す。これによりユーザーが `ccwin init` を実行しなくても
		// config.toml は常に存在する状態になる。
		if err := config.EnsureExists(""); err != nil {
			fmt.Fprintf(os.Stderr, "config bootstrap error: %v\n", err)
			os.Exit(2)
		}
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(2)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config validation error: %v\n", err)
		os.Exit(2)
	}

	// daemon.Run は Windows でのみ実装されているが、GOOS チェックは main() で済み
	if err := daemon.Run(mainCtx(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		os.Exit(1)
	}
}

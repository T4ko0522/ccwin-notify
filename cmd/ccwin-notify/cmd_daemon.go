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

	// M-04: デフォルト config パス利用時、Load より前に %APPDATA%/ccwin-notify
	// の DACL を整える。これで config.toml が起動直前に他ユーザーに書き換え
	// られた場合の改ざんリスクを下げる。--config <他パス> 指定時は呼び出し側
	// の責任とみなしてスキップ。
	if configPath == "" {
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			if err := auth.EnsureDirACL(filepath.Join(appdata, "ccwin-notify")); err != nil {
				fmt.Fprintf(os.Stderr, "ACL preflight error: %v\n", err)
				os.Exit(4)
			}
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

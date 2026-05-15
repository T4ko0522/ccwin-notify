package main

import (
	"fmt"
	"os"

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

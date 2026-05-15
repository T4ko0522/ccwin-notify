package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/t4ko0522/ccwin-notify/internal/config"
)

func runConfig(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "config: subcommand required: show | path")
		os.Exit(1)
	}

	switch args[0] {
	case "show":
		cfg, err := config.Load("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "config show: %v\n", err)
			os.Exit(2)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "config show: marshal: %v\n", err)
			os.Exit(1)
		}

	case "path":
		p := config.DefaultPath()
		if p == "" {
			fmt.Fprintln(os.Stderr, "config path: ホームディレクトリが解決できません (XDG_CONFIG_HOME も USERPROFILE も未設定)")
			os.Exit(1)
		}
		fmt.Println(p)

	default:
		fmt.Fprintf(os.Stderr, "config: unknown subcommand %q (available: show | path)\n", args[0])
		os.Exit(1)
	}
}

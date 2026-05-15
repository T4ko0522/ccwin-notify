package main

import (
	"fmt"
	"os"
	"runtime"
)

// version はビルド時に -ldflags "-X main.version=..." で上書きされる。
// ローカル `go build` ではデフォルト値が使われる。
var version = "0.1.0"

func main() {
	if runtime.GOOS != "windows" {
		fmt.Fprintln(os.Stderr, "ccwin-notify is Windows-only (got GOOS="+runtime.GOOS+")")
		os.Exit(2)
	}

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "subcommand required: daemon | send | tui | config | init | version")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "daemon":
		runDaemon(os.Args[2:])
	case "send":
		runSend(os.Args[2:])
	case "tui":
		runTUI(os.Args[2:])
	case "config":
		runConfig(os.Args[2:])
	case "init":
		runInit(os.Args[2:])
	case "version":
		fmt.Println("ccwin-notify " + version)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %q\n", os.Args[1])
		fmt.Fprintln(os.Stderr, "available: daemon | send | tui | config | init | version")
		os.Exit(1)
	}
}

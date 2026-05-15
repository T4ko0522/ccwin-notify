package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/t4ko0522/ccwin-notify/internal/apiclient"
	"github.com/t4ko0522/ccwin-notify/internal/send"
)

func runSend(args []string) {
	var kind, titleOverride, bodyOverride string
	useStdin := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--kind", "-kind":
			if i+1 < len(args) {
				kind = args[i+1]
				i++
			}
		case "--title", "-title":
			if i+1 < len(args) {
				titleOverride = args[i+1]
				i++
			}
		case "--body", "-body":
			if i+1 < len(args) {
				bodyOverride = args[i+1]
				i++
			}
		case "--stdin":
			useStdin = true
		}
	}

	if kind == "" {
		fmt.Fprintln(os.Stderr, "send: --kind is required")
		os.Exit(1)
	}

	var rawJSON json.RawMessage
	if useStdin {
		scanner := bufio.NewScanner(os.Stdin)
		var lines []byte
		for scanner.Scan() {
			lines = append(lines, scanner.Bytes()...)
		}
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "send: stdin read error: %v\n", err)
			os.Exit(1)
		}
		rawJSON = json.RawMessage(lines)
	}

	ev, err := send.NormalizeHook(kind, rawJSON)
	if err != nil {
		fmt.Fprintf(os.Stderr, "send: normalize error: %v\n", err)
		os.Exit(1)
	}

	// --title / --body は NormalizeHook 後に上書きする
	// (Stop/SubagentStop kind は NormalizeHook 内で Title 固定上書きされるが、
	// --title 指定があれば優先する)。
	if titleOverride != "" {
		ev.Title = titleOverride
	}
	if bodyOverride != "" {
		ev.Body = bodyOverride
	}

	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		fmt.Fprintln(os.Stderr, "send: APPDATA not set")
		os.Exit(1)
	}
	ccwinDir := filepath.Join(appdata, "ccwin-notify")
	portfilePath := filepath.Join(ccwinDir, "daemon.port")
	tokenPath := filepath.Join(ccwinDir, "secret.token")

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "send: daemon not running: %v\n", err)
		os.Exit(4)
	}

	if err := client.PostEvent(context.Background(), ev); err != nil {
		fmt.Fprintf(os.Stderr, "send: post event error: %v\n", err)
		os.Exit(1)
	}
}

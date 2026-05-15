package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/t4ko0522/ccwin-notify/internal/apiclient"
	"github.com/t4ko0522/ccwin-notify/internal/tui"
)

func runTUI(args []string) {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		fmt.Fprintln(os.Stderr, "tui: APPDATA not set")
		os.Exit(1)
	}
	ccwinDir := filepath.Join(appdata, "ccwin-notify")
	portfilePath := filepath.Join(ccwinDir, "daemon.port")
	tokenPath := filepath.Join(ccwinDir, "secret.token")

	client, err := apiclient.New(portfilePath, tokenPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui: daemon not running: %v\n", err)
		os.Exit(4)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, err := client.StreamEvents(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui: stream events error: %v\n", err)
		os.Exit(1)
	}

	model := tui.NewModel(ctx, eventCh)
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui: program error: %v\n", err)
		os.Exit(1)
	}
}

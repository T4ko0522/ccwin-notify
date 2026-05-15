// sseprobe は ccwin-notify daemon の /v1/events/stream を一定時間購読し、
// 受信した event-published メッセージを行ごとに stdout に出すデバッグ用ユーティリティ。
//
// 使い方: go run ./testdata/sseprobe -duration 4s
// daemon の port/token は %APPDATA%\ccwin-notify\ から読む。
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type portFile struct {
	Port int `json:"port"`
}

func main() {
	duration := flag.Duration("duration", 4*time.Second, "SSE 購読時間")
	flag.Parse()

	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		fmt.Fprintln(os.Stderr, "sseprobe: APPDATA not set")
		os.Exit(1)
	}
	portPath := filepath.Join(appdata, "ccwin-notify", "daemon.port")
	tokenPath := filepath.Join(appdata, "ccwin-notify", "secret.token")

	pfBytes, err := os.ReadFile(portPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sseprobe: read port file: %v\n", err)
		os.Exit(1)
	}
	var pf portFile
	if err := json.Unmarshal(pfBytes, &pf); err != nil {
		fmt.Fprintf(os.Stderr, "sseprobe: parse port file: %v\n", err)
		os.Exit(1)
	}
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sseprobe: read token file: %v\n", err)
		os.Exit(1)
	}
	token := strings.TrimSpace(string(tokenBytes))

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	url := fmt.Sprintf("http://127.0.0.1:%d/v1/events/stream", pf.Port)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sseprobe: connect: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "sseprobe: status %d\n", resp.StatusCode)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "sseprobe: subscribed (port=%d, duration=%s)\n", pf.Port, *duration)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		fmt.Println(line)
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "sseprobe: scan: %v\n", err)
	}
	fmt.Fprintln(os.Stderr, "sseprobe: done")
}

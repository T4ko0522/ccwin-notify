// stream.go は StreamEvents の実装を提供する。
package apiclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
)

const (
	streamBufferSize = 64
	backoffInitial   = 1 * time.Second
	backoffFactor    = 2.0
	backoffMax       = 30 * time.Second
)

// StreamEvents は内部 goroutine で SSE を読み、チャネルに Event を push する。
// buffer=64。ctx cancel / server 切断でチャネルを close する。
// 内部で指数バックオフ再接続 (初回 1s, 係数 2.0, 上限 30s) を行い、ctx alive な間は継続。
// buffer 溢れは drop + WARN ログ (TUI を遅らせない)。
func (c *client) StreamEvents(ctx context.Context) (<-chan event.Event, error) {
	ch := make(chan event.Event, streamBufferSize)
	go c.streamLoop(ctx, ch)
	return ch, nil
}

// streamLoop は SSE 接続を維持し、Event を ch に push するループ。
func (c *client) streamLoop(ctx context.Context, ch chan event.Event) {
	defer close(ch)

	backoff := backoffInitial
	for {
		if ctx.Err() != nil {
			return
		}

		err := c.readSSE(ctx, ch)
		if err == nil || ctx.Err() != nil {
			return
		}

		// 再接続バックオフ
		slog.Warn("StreamEvents: SSE disconnected, reconnecting", "backoff", backoff, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		// バックオフを更新
		nextBackoff := time.Duration(float64(backoff) * backoffFactor)
		if nextBackoff > backoffMax {
			nextBackoff = backoffMax
		}
		backoff = nextBackoff
	}
}

// readSSE は単一の SSE 接続を確立し、イベントを読み込む。
func (c *client) readSSE(ctx context.Context, ch chan event.Event) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/events/stream", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE: unexpected status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	var dataLines []string

	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		line := scanner.Text()

		if line == "" {
			// 空行 = イベント区切り
			if len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")
				var ev event.Event
				if err := json.Unmarshal([]byte(data), &ev); err == nil {
					select {
					case ch <- ev:
					default:
						// buffer 溢れは drop + WARN
						slog.Warn("StreamEvents: channel buffer full, dropping event", "event_id", ev.ID)
					}
				}
				dataLines = nil
			}
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
		// event: / id: / コメント行は無視 (Event.ID は data JSON に含まれる)
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

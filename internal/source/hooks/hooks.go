// Package hooks は Hooks IPC ハンドラを提供する。
package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/ipc/middleware"
	"github.com/t4ko0522/ccwin-notify/internal/ipc/sse"
)

// eventRequest は POST /v1/events のリクエストボディ。
// Source フィールドは受け付けない — サーバー側で "hooks" を強制する (MUST 7 / B3R-02)。
type eventRequest struct {
	Kind  event.EventKind `json:"kind"`
	Title string          `json:"title"`
	Body  string          `json:"body"`
	Raw   json.RawMessage `json:"raw,omitempty"`
}

// HandleEvents は POST /v1/events ハンドラを返す。
// bus: EventSink, acceptCtx: 新規入力ゲート, expectedHash: Bearer 認証 SHA-256 ダイジェスト
func HandleEvents(bus event.EventSink, acceptCtx context.Context, expectedHash [32]byte) http.Handler {
	bearerMiddleware := middleware.RequireBearer(expectedHash)
	return bearerMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// acceptCtx が cancel されていたら 503
		if acceptCtx.Err() != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "shutting_down", "daemon is shutting down")
			return
		}

		var req eventRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}

		// Kind の妥当性確認
		if !event.ValidKinds[req.Kind] {
			writeJSONError(w, http.StatusBadRequest, "invalid_kind", "unknown EventKind '"+string(req.Kind)+"'")
			return
		}

		now := time.Now()
		ev := event.Event{
			ID:        event.NewID(),
			Kind:      req.Kind,
			Title:     req.Title,
			Body:      req.Body,
			Source:    "hooks",
			Timestamp: now,
			Raw:       req.Raw,
		}

		// Bus に Publish (acceptCtx を渡すと shutdown 時に ErrPublishCanceled が返る)
		if err := bus.Publish(acceptCtx, ev); err != nil {
			switch {
			case errors.Is(err, event.ErrDropped):
				writeJSONError(w, http.StatusServiceUnavailable, "queue_full", "event queue is full")
			case errors.Is(err, event.ErrPublishCanceled):
				writeJSONError(w, http.StatusServiceUnavailable, "queue_full", "event queue is full or shutting down")
			case errors.Is(err, event.ErrBusClosed):
				writeJSONError(w, http.StatusServiceUnavailable, "shutting_down", "daemon is shutting down")
			default:
				writeJSONError(w, http.StatusInternalServerError, "internal_error", err.Error())
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"event_id": ev.ID})
	}))
}

// HandleStream は GET /v1/events/stream (SSE) ハンドラを返す。
func HandleStream(sseHub *sse.Hub, acceptCtx context.Context, expectedHash [32]byte) http.Handler {
	bearerMiddleware := middleware.RequireBearer(expectedHash)
	return bearerMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// subCtx で Subscribe し、ハンドラ終了時に確実に hub から登録解除する (M3R subscriber cleanup)
		subCtx, cancelSub := context.WithCancel(r.Context())
		defer cancelSub()
		ch := sseHub.Subscribe(subCtx)
		pingTicker := time.NewTicker(30 * time.Second)
		defer pingTicker.Stop()

		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				switch msg.Kind {
				case "event-published":
					data, err := json.Marshal(msg.Payload)
					if err != nil {
						continue
					}
					evMsg, _ := msg.Payload.(event.Event)
					_, _ = w.Write([]byte("event: ccwin-event\nid: " + evMsg.ID + "\ndata: " + string(data) + "\n\n"))
					flusher.Flush()
				case "dispatch-result":
					data, err := json.Marshal(msg.Payload)
					if err != nil {
						continue
					}
					_, _ = w.Write([]byte("event: dispatch-result\ndata: " + string(data) + "\n\n"))
					flusher.Flush()
				}
			case <-pingTicker.C:
				_, _ = w.Write([]byte(": ping\n\n"))
				flusher.Flush()
			case <-r.Context().Done():
				return
			case <-acceptCtx.Done():
				return
			}
		}
	}))
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

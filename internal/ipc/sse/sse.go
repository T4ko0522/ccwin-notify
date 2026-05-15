// Package sse は SSE (Server-Sent Events) Hub を提供する。
package sse

import (
	"context"
	"log/slog"
	"sync"
)

// DispatchResult は Dispatcher が各 Notifier 発火後に SSE Hub へ送るメッセージ。
type DispatchResult struct {
	EventID  string
	Notifier string
	OK       bool
	Err      error
}

// SSEMessage は Hub が配信するメッセージの共用型。
type SSEMessage struct {
	Kind    string      // "event-published" | "dispatch-result"
	Payload interface{} // event.Event または DispatchResult
}

const subscriberBufferSize = 32

// maxSubscribers は Hub が同時に保持できる購読者数の上限 (M-06)。
// 超過時 Subscribe は ok=false を返し、呼び出し側 (HTTP handler) は 503 を返すこと。
const maxSubscribers = 16

// subscriber は単一の購読者を表す。
type subscriber struct {
	ch chan SSEMessage
}

// Hub は SSE 購読者の管理と非ブロッキング配信を担当する。
type Hub struct {
	mu     sync.Mutex
	subs   map[*subscriber]struct{}
	closed bool
}

// NewHub は Hub を生成する。
func NewHub() *Hub {
	return &Hub{
		subs: make(map[*subscriber]struct{}),
	}
}

// Publish はノンブロッキング送信。buffer 満杯の購読者は drop + WARN ログ。
// 購読者ごとの遅延が他購読者に伝播しない。
func (h *Hub) Publish(msg SSEMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return
	}

	for sub := range h.subs {
		select {
		case sub.ch <- msg:
		default:
			slog.Warn("SSE Hub: subscriber buffer full, dropping message")
		}
	}
}

// Subscribe は新規購読者を登録し receive-only チャネルを返す。
// ctx.Done() で自動登録解除。
// ok=false のとき: Hub が close 済み、または購読者上限 (maxSubscribers) を超えた場合。
// 上限超過時、呼び出し側は 503 Service Unavailable を返すこと (M-06)。
func (h *Hub) Subscribe(ctx context.Context) (<-chan SSEMessage, bool) {
	sub := &subscriber{
		ch: make(chan SSEMessage, subscriberBufferSize),
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		close(sub.ch)
		return sub.ch, false
	}
	if len(h.subs) >= maxSubscribers {
		h.mu.Unlock()
		close(sub.ch)
		slog.Warn("SSE Hub: subscriber limit reached, rejecting new subscriber",
			"limit", maxSubscribers)
		return sub.ch, false
	}
	h.subs[sub] = struct{}{}
	h.mu.Unlock()

	// ctx.Done() で自動解除
	go func() {
		<-ctx.Done()
		h.mu.Lock()
		if _, ok := h.subs[sub]; ok {
			delete(h.subs, sub)
			close(sub.ch)
		}
		h.mu.Unlock()
	}()

	return sub.ch, true
}

// Close は全購読者を切断 (shutdown 時)。
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return
	}
	h.closed = true
	for sub := range h.subs {
		close(sub.ch)
	}
	h.subs = make(map[*subscriber]struct{})
}

// Package send は ccwin-notify send サブコマンドの Hook→Event 変換ロジックを提供する。
package send

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
)

// stopHook は Stop フックの stdin JSON 構造。
type stopHook struct {
	LastAssistantMessage string `json:"last_assistant_message"`
}

// notificationHook は Notification フックの stdin JSON 構造。
type notificationHook struct {
	Title   string `json:"title"`
	Message string `json:"message"`
}

// subagentStopHook は SubagentStop フックの stdin JSON 構造。
type subagentStopHook struct {
	AgentType            string `json:"agent_type"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

// NormalizeHook は Hooks stdin JSON を §8.1.1 のマッピング規則に従い event.Event に変換する。
// kind は "Stop" / "Notification" / "SubagentStop" のいずれか。
// 未知の Kind は error を返す。必須フィールド欠落は空文字として許容 (空文字許容 / A1)。
func NormalizeHook(kind string, rawJSON json.RawMessage) (event.Event, error) {
	ek := event.EventKind(kind)
	if !event.ValidKinds[ek] {
		return event.Event{}, fmt.Errorf("send: unknown Hook kind %q", kind)
	}

	ev := event.Event{
		ID:        event.NewID(),
		Kind:      ek,
		Source:    "hooks",
		Timestamp: time.Now(),
		Raw:       rawJSON,
	}

	switch ek {
	case event.KindStop:
		var h stopHook
		_ = json.Unmarshal(rawJSON, &h)
		ev.Title = "Claude Code: response complete"
		ev.Body = h.LastAssistantMessage

	case event.KindNotification:
		var h notificationHook
		_ = json.Unmarshal(rawJSON, &h)
		ev.Title = h.Title
		if ev.Title == "" {
			ev.Title = "Claude Code"
		}
		ev.Body = h.Message

	case event.KindSubagentStop:
		var h subagentStopHook
		_ = json.Unmarshal(rawJSON, &h)
		if h.AgentType != "" {
			ev.Title = "Subagent done: " + h.AgentType
		} else {
			ev.Title = "Subagent done"
		}
		ev.Body = h.LastAssistantMessage

	default:
		// KindIdle / KindProcessStarted / KindProcessStopped は Hooks 経由では使わないが
		// ValidKinds に含まれるため、フィールド変換なしで素通し
		ev.Title = kind
	}

	return ev, nil
}

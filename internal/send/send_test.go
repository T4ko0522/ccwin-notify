// internal/send パッケージのユニットテスト
// テスト ID: T-040〜T-044 の実装版
// 受入条件: A1 (CLI 側) — Hook→Event マッピング (§8.1.1)
package send_test

import (
	"encoding/json"
	"testing"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/send"
)

// T-040: Stop フック → Event.Kind=Stop / Title=固定 / Body=last_assistant_message
func TestNormalizeHook_Stop(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"last_assistant_message": "task completed"}`)
	ev, err := send.NormalizeHook("Stop", raw)
	if err != nil {
		t.Fatalf("NormalizeHook Stop: %v", err)
	}
	if ev.Kind != event.KindStop {
		t.Errorf("Kind: got %q, want %q", ev.Kind, event.KindStop)
	}
	if ev.Title != "Claude Code: response complete" {
		t.Errorf("Title: got %q, want %q", ev.Title, "Claude Code: response complete")
	}
	if ev.Body != "task completed" {
		t.Errorf("Body: got %q, want %q", ev.Body, "task completed")
	}
	if ev.Source != "hooks" {
		t.Errorf("Source: got %q, want \"hooks\"", ev.Source)
	}
}

// T-041: Notification フック → Title=title / Body=message
func TestNormalizeHook_Notification(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"title": "Input required", "message": "Please respond"}`)
	ev, err := send.NormalizeHook("Notification", raw)
	if err != nil {
		t.Fatalf("NormalizeHook Notification: %v", err)
	}
	if ev.Kind != event.KindNotification {
		t.Errorf("Kind: got %q, want %q", ev.Kind, event.KindNotification)
	}
	if ev.Title != "Input required" {
		t.Errorf("Title: got %q, want %q", ev.Title, "Input required")
	}
	if ev.Body != "Please respond" {
		t.Errorf("Body: got %q, want %q", ev.Body, "Please respond")
	}
}

// T-041b: Notification title が空なら "Claude Code" になる
func TestNormalizeHook_Notification_EmptyTitle(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"title": "", "message": "some message"}`)
	ev, err := send.NormalizeHook("Notification", raw)
	if err != nil {
		t.Fatalf("NormalizeHook Notification empty title: %v", err)
	}
	if ev.Title != "Claude Code" {
		t.Errorf("Title (empty): got %q, want \"Claude Code\"", ev.Title)
	}
}

// T-042: SubagentStop フック → Title="Subagent done: <agent_type>"
func TestNormalizeHook_SubagentStop(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"agent_type": "code_generator", "last_assistant_message": "code generated"}`)
	ev, err := send.NormalizeHook("SubagentStop", raw)
	if err != nil {
		t.Fatalf("NormalizeHook SubagentStop: %v", err)
	}
	if ev.Kind != event.KindSubagentStop {
		t.Errorf("Kind: got %q, want %q", ev.Kind, event.KindSubagentStop)
	}
	if ev.Title != "Subagent done: code_generator" {
		t.Errorf("Title: got %q, want %q", ev.Title, "Subagent done: code_generator")
	}
	if ev.Body != "code generated" {
		t.Errorf("Body: got %q, want %q", ev.Body, "code generated")
	}
}

// T-042b: agent_type 欠落時 → "Subagent done"
func TestNormalizeHook_SubagentStop_NoAgentType(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"last_assistant_message": "done"}`)
	ev, err := send.NormalizeHook("SubagentStop", raw)
	if err != nil {
		t.Fatalf("NormalizeHook SubagentStop no agent_type: %v", err)
	}
	if ev.Title != "Subagent done" {
		t.Errorf("Title (no agent_type): got %q, want \"Subagent done\"", ev.Title)
	}
}

// T-043: 必須フィールド欠落時は空文字許容
func TestNormalizeHook_Stop_MissingFields(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"session_id": "abc"}`)
	ev, err := send.NormalizeHook("Stop", raw)
	if err != nil {
		t.Fatalf("NormalizeHook Stop missing fields: %v", err)
	}
	if ev.Body != "" {
		t.Errorf("Body (missing): got %q, want \"\"", ev.Body)
	}
}

// T-044: 未知の Kind → エラー返却
func TestNormalizeHook_UnknownKind(t *testing.T) {
	t.Parallel()
	_, err := send.NormalizeHook("UnknownKindXyz", json.RawMessage(`{}`))
	if err == nil {
		t.Error("Unknown kind: error が返るべきだが nil だった")
	}
}

// Raw フィールドに stdin JSON 全体が保持される
func TestNormalizeHook_RawPreserved(t *testing.T) {
	t.Parallel()
	rawInput := json.RawMessage(`{"last_assistant_message": "done", "session_id": "abc"}`)
	ev, err := send.NormalizeHook("Stop", rawInput)
	if err != nil {
		t.Fatalf("NormalizeHook: %v", err)
	}
	if string(ev.Raw) != string(rawInput) {
		t.Errorf("Raw: got %s, want %s", ev.Raw, rawInput)
	}
}

package sessionlog

import (
	"strings"
	"testing"

	"github.com/t4ko0522/ccwin-notify/internal/event"
)

func TestParseLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		line     string
		bodyMax  int
		wantFire bool
		wantKind event.EventKind
		wantBody string
		wantErr  bool
	}{
		{
			name:     "assistant + end_turn → Stop で発火",
			line:     `{"type":"assistant","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"こんにちは"}]}}`,
			bodyMax:  200,
			wantFire: true,
			wantKind: event.KindStop,
			wantBody: "こんにちは",
		},
		{
			name:     "assistant + stop_sequence → Stop で発火",
			line:     `{"type":"assistant","message":{"stop_reason":"stop_sequence","content":[{"type":"text","text":"hi"}]}}`,
			bodyMax:  200,
			wantFire: true,
			wantKind: event.KindStop,
			wantBody: "hi",
		},
		{
			// tool_use 行は Claude Code が回答時にまとめて jsonl に書く挙動があり、
			// 「呼び出し瞬間」の検知に使えないため、AskUserQuestion / ExitPlanMode を含めて
			// jsonl 監視では一切発火させない。即時通知は wezterm Source のターミナル監視で行う。
			name:     "tool_use 行 (AskUserQuestion 含む) → 一切発火しない",
			line:     `{"type":"assistant","message":{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"x","name":"AskUserQuestion"}]}}`,
			bodyMax:  200,
			wantFire: false,
		},
		{
			name:     "tool_use 行 (ExitPlanMode 含む) → 一切発火しない",
			line:     `{"type":"assistant","message":{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"x","name":"ExitPlanMode"}]}}`,
			bodyMax:  200,
			wantFire: false,
		},
		{
			name:     "tool_use 行 (通常 tool Bash) → 発火しない",
			line:     `{"type":"assistant","message":{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"x","name":"Bash"}]}}`,
			bodyMax:  200,
			wantFire: false,
		},
		{
			name:     "type=user → 発火しない",
			line:     `{"type":"user","message":{"role":"user"}}`,
			bodyMax:  200,
			wantFire: false,
		},
		{
			name:     "type=system → 発火しない",
			line:     `{"type":"system","content":"..."}`,
			bodyMax:  200,
			wantFire: false,
		},
		{
			name:     "type=attachment → 発火しない",
			line:     `{"type":"attachment"}`,
			bodyMax:  200,
			wantFire: false,
		},
		{
			name:     "stop_reason 欠如 → 発火しない",
			line:     `{"type":"assistant","message":{"content":[{"type":"text","text":"x"}]}}`,
			bodyMax:  200,
			wantFire: false,
		},
		{
			name:     "message 欠如 → 発火しない",
			line:     `{"type":"assistant"}`,
			bodyMax:  200,
			wantFire: false,
		},
		{
			// 1 ターン中に thinking のみ end_turn 行と text end_turn 行が連続するケースがあり、
			// 前者で発火すると Body 空の重複 Toast が出てしまうため発火しない仕様にした。
			name:     "text content なし (thinking のみ) → 発火しない",
			line:     `{"type":"assistant","message":{"stop_reason":"end_turn","content":[{"type":"thinking","thinking":"..."}]}}`,
			bodyMax:  200,
			wantFire: false,
		},
		{
			name:     "text content が空文字列 → 発火しない",
			line:     `{"type":"assistant","message":{"stop_reason":"end_turn","content":[{"type":"text","text":""}]}}`,
			bodyMax:  200,
			wantFire: false,
		},
		{
			name:     "複数 text content → 最初の text のみ採用",
			line:     `{"type":"assistant","message":{"stop_reason":"end_turn","content":[{"type":"thinking","thinking":"x"},{"type":"text","text":"first"},{"type":"text","text":"second"}]}}`,
			bodyMax:  200,
			wantFire: true,
			wantKind: event.KindStop,
			wantBody: "first",
		},
		{
			name:     "text が BodyMaxLen を超える → rune truncate + 末尾 …",
			line:     `{"type":"assistant","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"` + strings.Repeat("あ", 50) + `"}]}}`,
			bodyMax:  10,
			wantFire: true,
			wantKind: event.KindStop,
			wantBody: strings.Repeat("あ", 10) + "…",
		},
		{
			name:     "bodyMax <= 0 → デフォルト 200 が使われ truncate されない",
			line:     `{"type":"assistant","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"short"}]}}`,
			bodyMax:  0,
			wantFire: true,
			wantKind: event.KindStop,
			wantBody: "short",
		},
		{
			name:    "不正 JSON → error",
			line:    `not a json`,
			bodyMax: 200,
			wantErr: true,
		},
		{
			name:     "空行 → 発火しない、error なし",
			line:     ``,
			bodyMax:  200,
			wantFire: false,
		},
		{
			name:     "空白のみ → 発火しない、error なし",
			line:     `   `,
			bodyMax:  200,
			wantFire: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := parseLine([]byte(tt.line), tt.bodyMax)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("err: got nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("err: got %v, want nil", err)
			}
			if res.Fire != tt.wantFire {
				t.Errorf("Fire: got %v, want %v", res.Fire, tt.wantFire)
			}
			if tt.wantFire && res.Kind != tt.wantKind {
				t.Errorf("Kind: got %q, want %q", res.Kind, tt.wantKind)
			}
			if tt.wantFire && res.Body != tt.wantBody {
				t.Errorf("Body: got %q, want %q", res.Body, tt.wantBody)
			}
		})
	}
}

func TestParseLineWithFormat_Codex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		line     string
		bodyMax  int
		wantFire bool
		wantBody string
		wantErr  bool
	}{
		{
			name:     "assistant final_answer response_item fires",
			line:     `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}],"phase":"final_answer"}}`,
			bodyMax:  200,
			wantFire: true,
			wantBody: "done",
		},
		{
			name:     "commentary response_item does not fire",
			line:     `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"working"}],"phase":"commentary"}}`,
			bodyMax:  200,
			wantFire: false,
		},
		{
			name:     "event_msg mirror does not fire to avoid duplicates",
			line:     `{"type":"event_msg","payload":{"type":"agent_message","message":"done","phase":"final_answer"}}`,
			bodyMax:  200,
			wantFire: false,
		},
		{
			name:     "user message does not fire",
			line:     `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}`,
			bodyMax:  200,
			wantFire: false,
		},
		{
			name:     "long output truncates",
			line:     `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + strings.Repeat("a", 20) + `"}],"phase":"final_answer"}}`,
			bodyMax:  5,
			wantFire: true,
			wantBody: "aaaaa…",
		},
		{
			name:    "invalid JSON errors",
			line:    `{invalid`,
			bodyMax: 200,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := parseLineWithFormat([]byte(tt.line), tt.bodyMax, FormatCodex)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("err: got nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("err: got %v, want nil", err)
			}
			if res.Fire != tt.wantFire {
				t.Errorf("Fire: got %v, want %v", res.Fire, tt.wantFire)
			}
			if tt.wantFire {
				if res.Kind != event.KindStop {
					t.Errorf("Kind: got %q, want %q", res.Kind, event.KindStop)
				}
				if res.Title != codexDefaultTitle {
					t.Errorf("Title: got %q, want %q", res.Title, codexDefaultTitle)
				}
				if res.Body != tt.wantBody {
					t.Errorf("Body: got %q, want %q", res.Body, tt.wantBody)
				}
			}
		})
	}
}

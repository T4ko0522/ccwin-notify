package sessionlog

import (
	"bytes"
	"encoding/json"

	"github.com/t4ko0522/ccwin-notify/internal/event"
)

// defaultBodyMaxLen は parseLine の BodyMaxLen 既定値 (D-05)。
const defaultBodyMaxLen = 200

// lineParseResult は parseLine の判定結果。Fire=false なら他フィールドは無視。
type lineParseResult struct {
	Fire  bool
	Kind  event.EventKind
	Title string
	Body  string
}

// assistantPayload は jsonl の "assistant" 行の最小スキーマ。
// 未知フィールドは無視 (encoding/json の既定動作)。
type assistantPayload struct {
	Type    string `json:"type"`
	Message *struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

// parseLine は jsonl 1 行をパースし、Stop event 発火対象かを判定する。
//
//   - 発火条件: type=="assistant" && message.stop_reason in {"end_turn","stop_sequence"}
//     かつ message.content[] に少なくとも 1 つの type=="text" を含む (D-01 / D-12 改訂)。
//   - 1 ターン中に「thinking のみで end_turn」と「text で end_turn」が連続して書かれるケースがあり、
//     前者で発火すると Body 空の重複 Toast が出てしまうため text なし行は発火しない。
//   - Body: 最初の type=="text" の text を bodyMaxLen 文字で truncate (rune 単位)。
//   - 不正 JSON は error を返す。空行 / 空白のみは Fire=false, err=nil を返す。
//
// 注: AskUserQuestion / ExitPlanMode の検知はここではしない。Claude Code は
// これらの tool_use 行を呼び出し時点では jsonl に flush せず、ユーザー回答時に
// まとめて書き出すため、jsonl 監視では「呼び出し瞬間」を捕捉できない。
// PreToolUse hook も組み込み UI パネル (AskUserQuestion / ExitPlanMode) では発火しないため、
// 即時通知は internal/source/wezterm の `wezterm cli get-text` 経由のターミナル監視で行う。
func parseLine(line []byte, bodyMaxLen int) (lineParseResult, error) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return lineParseResult{}, nil
	}
	if bodyMaxLen <= 0 {
		bodyMaxLen = defaultBodyMaxLen
	}

	var p assistantPayload
	if err := json.Unmarshal(trimmed, &p); err != nil {
		return lineParseResult{}, err
	}

	if p.Type != "assistant" || p.Message == nil {
		return lineParseResult{}, nil
	}

	switch p.Message.StopReason {
	case "end_turn", "stop_sequence":
		body := ""
		for _, c := range p.Message.Content {
			if c.Type == "text" && c.Text != "" {
				body = truncateRunes(c.Text, bodyMaxLen)
				break
			}
		}
		if body == "" {
			// text content が無い (thinking のみ等) end_turn は発火しない。
			return lineParseResult{}, nil
		}
		return lineParseResult{
			Fire:  true,
			Kind:  event.KindStop,
			Title: defaultTitle,
			Body:  body,
		}, nil

	default:
		return lineParseResult{}, nil
	}
}

// defaultTitle は end_turn 検知時の Toast Title (D-05)。
const defaultTitle = "Claude finished"

// truncateRunes は s を rune 単位で n 個に切り詰め、超過時は末尾に "…" を付ける。
// rune 境界を壊さない (D-13)。
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

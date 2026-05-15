// Package tui は bubbletea ベースのライブログ TUI を提供する。
package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/t4ko0522/ccwin-notify/internal/event"
)

// maxLines は m.lines の上限。長時間稼働でアンバウンド成長しないように先頭から切り捨てる (P-H-01)。
const maxLines = 1024

// eventArrivedMsg は SSE チャネルから Event を受信したときの tea.Msg。
type eventArrivedMsg struct {
	event.Event
}

// streamClosedMsg は SSE チャネルが close されたときの tea.Msg。
type streamClosedMsg struct{}

// logLine は TUI に表示する1行分のログ。
type logLine struct {
	ts    time.Time
	kind  event.EventKind
	title string
	body  string
}

// Model は bubbletea の Model インターフェースを実装するライブログビューア。
type Model struct {
	ctx     context.Context
	eventCh <-chan event.Event
	lines   []logLine
	width   int
	height  int
	closed  bool
}

// NewModel は apiclient から取得した event channel を受け取り Model を生成する。
func NewModel(ctx context.Context, eventCh <-chan event.Event) Model {
	return Model{
		ctx:     ctx,
		eventCh: eventCh,
	}
}

// NewForTest はテスト用の Model を生成する。NewModel と同等だが test 専用名で公開。
func NewForTest(ctx context.Context, eventCh <-chan event.Event) Model {
	return NewModel(ctx, eventCh)
}

// LogLineCount は現在記録されているログ行数を返す (テスト用)。
func (m Model) LogLineCount() int {
	return len(m.lines)
}

// Init は初期 Cmd として最初の waitForEvent を返す。
func (m Model) Init() tea.Cmd {
	return WaitForEvent(m.eventCh)
}

// Update は tea.Msg を受け取り Model を更新する (純関数)。
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case eventArrivedMsg:
		m.lines = append(m.lines, logLine{
			ts:    v.Timestamp,
			kind:  v.Kind,
			title: v.Title,
			body:  v.Body,
		})
		// P-H-01: 上限を超えたら先頭を切り捨てる (新しいバッキング配列にコピー)
		if len(m.lines) > maxLines {
			trimmed := make([]logLine, maxLines)
			copy(trimmed, m.lines[len(m.lines)-maxLines:])
			m.lines = trimmed
		}
		// 次のイベントを待つ Cmd を返す
		return m, WaitForEvent(m.eventCh)

	case streamClosedMsg:
		m.closed = true
		return m, nil

	case tea.KeyMsg:
		switch v.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
	}

	return m, nil
}

var (
	kindStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
	titleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	timeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// View は現在の Model を文字列としてレンダリングする (純関数)。
func (m Model) View() string {
	if m.closed {
		return "Stream closed. Press q to quit.\n"
	}

	out := ""
	// 最新 N 行を表示 (height が設定されていれば制限)
	start := 0
	if m.height > 2 && len(m.lines) > m.height-2 {
		start = len(m.lines) - (m.height - 2)
	}

	for _, l := range m.lines[start:] {
		ts := timeStyle.Render(l.ts.Format("15:04:05"))
		kind := kindStyle.Render(fmt.Sprintf("[%s]", l.kind))
		title := titleStyle.Render(l.title)
		line := fmt.Sprintf("%s %s %s", ts, kind, title)
		if l.body != "" {
			line += " — " + l.body
		}
		out += line + "\n"
	}

	out += timeStyle.Render("Press q to quit") + "\n"
	return out
}

// WaitForEvent は event channel から1件読む tea.Cmd を返す。
// channel が close された場合は streamClosedMsg を返す。
// D-41: apiclient 側 goroutine がバッファリング、TUI 側は waitForEvent の Cmd で1件ずつ受信。
func WaitForEvent(ch <-chan event.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamClosedMsg{}
		}
		return eventArrivedMsg{ev}
	}
}

// SimulateEventArrived はテスト用: eventArrivedMsg を Model に渡して Update を呼ぶ。
func SimulateEventArrived(m Model, ev event.Event) (Model, tea.Cmd) {
	newModel, cmd := m.Update(eventArrivedMsg{ev})
	return newModel.(Model), cmd
}

// SimulateStreamClosed はテスト用: streamClosedMsg を Model に渡して Update を呼ぶ。
func SimulateStreamClosed(m Model) (Model, tea.Cmd) {
	newModel, cmd := m.Update(streamClosedMsg{})
	return newModel.(Model), cmd
}

// IsClosed はテスト用: Model の closed フラグを返す。
func (m Model) IsClosed() bool {
	return m.closed
}

// AsEventArrivedMsg は tea.Msg が eventArrivedMsg かどうかを確認し、Event を返す。
func AsEventArrivedMsg(msg tea.Msg) (event.Event, bool) {
	v, ok := msg.(eventArrivedMsg)
	if !ok {
		return event.Event{}, false
	}
	return v.Event, true
}

// IsStreamClosedMsg は tea.Msg が streamClosedMsg かどうかを確認する。
func IsStreamClosedMsg(msg tea.Msg) bool {
	_, ok := msg.(streamClosedMsg)
	return ok
}

// SimulateKeyMsg はテスト用: tea.KeyMsg を Model に渡して Update を呼ぶ。
func SimulateKeyMsg(m Model, key string) (Model, tea.Cmd) {
	newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return newModel.(Model), cmd
}

// SimulateWindowSizeMsg はテスト用: tea.WindowSizeMsg を Model に渡して Update を呼ぶ。
func SimulateWindowSizeMsg(m Model, width, height int) (Model, tea.Cmd) {
	newModel, cmd := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return newModel.(Model), cmd
}

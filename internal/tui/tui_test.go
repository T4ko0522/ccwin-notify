// internal/tui パッケージのユニットテスト (サイクル B)
// テスト ID: T-070〜T-072
// 受入条件: TUI-3 / M-TEA-CMD — Model.Update を純関数テスト (bubbletea NewProgram は起動しない)
package tui_test

import (
	"context"
	"testing"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/tui"
)

// T-070 / TUI-3: Model.Update(eventArrivedMsg{...}) でログ行数が1増える (純関数テスト)
func TestModel_Update_EventArrived_IncreasesLogCount(t *testing.T) {
	t.Parallel()

	eventCh := make(chan event.Event, 10)
	ctx := context.Background()
	model := tui.NewForTest(ctx, eventCh)
	initialLines := model.LogLineCount()

	ev := event.Event{Kind: event.KindStop, Title: "test"}
	updatedModel, _ := tui.SimulateEventArrived(model, ev)

	if updatedModel.LogLineCount() != initialLines+1 {
		t.Errorf("LogLineCount: got %d, want %d", updatedModel.LogLineCount(), initialLines+1)
	}
}

// T-071 / M-TEA-CMD: WaitForEvent(ch) がchanから1件読んでeventArrivedMsg相当のMsgを返す
func TestWaitForEvent_ReturnsEventMsg(t *testing.T) {
	t.Parallel()

	ch := make(chan event.Event, 1)
	ev := event.Event{Kind: event.KindStop, Title: "test"}
	ch <- ev

	cmd := tui.WaitForEvent(ch)
	msg := cmd()

	arrived, ok := tui.AsEventArrivedMsg(msg)
	if !ok {
		t.Fatalf("AsEventArrivedMsg: msg の型が event arrived msg でない: %T", msg)
	}
	if arrived.Kind != event.KindStop {
		t.Errorf("arrived.Kind: got %q, want %q", arrived.Kind, event.KindStop)
	}
	if arrived.Title != "test" {
		t.Errorf("arrived.Title: got %q, want \"test\"", arrived.Title)
	}
}

// T-072 / M-TEA-CMD: WaitForEvent(ch) がchan close後にstreamClosedMsg相当を返す
func TestWaitForEvent_StreamClosed(t *testing.T) {
	t.Parallel()

	ch := make(chan event.Event)
	close(ch)

	cmd := tui.WaitForEvent(ch)
	msg := cmd()

	if !tui.IsStreamClosedMsg(msg) {
		t.Errorf("IsStreamClosedMsg: got %T, want streamClosedMsg", msg)
	}
}

// T-072b / M-TEA-CMD: Update(eventArrivedMsg) が WaitForEvent(eventCh) Cmd を返す
func TestModel_Update_EventArrived_ReturnsNextCmd(t *testing.T) {
	t.Parallel()

	ch := make(chan event.Event, 2)
	ctx := context.Background()
	model := tui.NewForTest(ctx, ch)

	ev := event.Event{Kind: event.KindNotification, Title: "next"}
	_, cmd := tui.SimulateEventArrived(model, ev)

	if cmd == nil {
		t.Error("SimulateEventArrived: cmd は nil であってはならない (次の WaitForEvent が返るべき)")
	}
}

// T-073 / TUI-3: Update(streamClosedMsg) で closed=true になり cmd が nil になる
func TestModel_Update_StreamClosed(t *testing.T) {
	t.Parallel()
	ch := make(chan event.Event)
	ctx := context.Background()
	model := tui.NewForTest(ctx, ch)

	if model.IsClosed() {
		t.Fatal("初期状態で IsClosed() が true: 想定外")
	}

	// SimulateStreamClosed で streamClosedMsg を渡す
	updated, cmd := tui.SimulateStreamClosed(model)

	if !updated.IsClosed() {
		t.Error("Update(streamClosedMsg) 後: IsClosed() が false — closed フラグが立っていない")
	}
	if cmd != nil {
		t.Errorf("Update(streamClosedMsg) 後: cmd は nil であるべき、got %T", cmd)
	}
}

// T-151: Model.Init() が非 nil の tea.Cmd を返す
func TestModel_Init_ReturnsCmd(t *testing.T) {
	t.Parallel()
	ch := make(chan event.Event, 1)
	ctx := context.Background()
	model := tui.NewForTest(ctx, ch)

	cmd := model.Init()
	if cmd == nil {
		t.Error("Init(): cmd は nil であってはならない (WaitForEvent を返すべき)")
	}
}

// T-152: Model.View() がストリーム未クローズの場合 "Press q to quit" を含む文字列を返す
func TestModel_View_NotClosed(t *testing.T) {
	t.Parallel()
	ch := make(chan event.Event)
	ctx := context.Background()
	model := tui.NewForTest(ctx, ch)

	view := model.View()
	if view == "" {
		t.Error("View(): 空文字が返された")
	}
	// "Press q to quit" が含まれること
	const wantFragment = "Press q to quit"
	if !containsStr(view, wantFragment) {
		t.Errorf("View(): %q が含まれていない: got %q", wantFragment, view)
	}
}

// T-153: Model.View() がストリームクローズ後 "Stream closed" を含む文字列を返す
func TestModel_View_Closed(t *testing.T) {
	t.Parallel()
	ch := make(chan event.Event)
	ctx := context.Background()
	model := tui.NewForTest(ctx, ch)
	closed, _ := tui.SimulateStreamClosed(model)

	view := closed.View()
	const wantFragment = "Stream closed"
	if !containsStr(view, wantFragment) {
		t.Errorf("View() (closed): %q が含まれていない: got %q", wantFragment, view)
	}
}

// T-154: Update(tea.KeyMsg "q") が tea.Quit を返す
func TestModel_Update_QuitKey(t *testing.T) {
	t.Parallel()
	ch := make(chan event.Event)
	ctx := context.Background()
	model := tui.NewForTest(ctx, ch)

	_, cmd := tui.SimulateKeyMsg(model, "q")
	if cmd == nil {
		t.Error("Update(KeyMsg q): cmd は nil であってはならない (tea.Quit を返すべき)")
	}
}

// T-155: Update(tea.WindowSizeMsg) がウィンドウサイズを記録する
func TestModel_Update_WindowSize(t *testing.T) {
	t.Parallel()
	ch := make(chan event.Event)
	ctx := context.Background()
	model := tui.NewForTest(ctx, ch)

	updated, _ := tui.SimulateWindowSizeMsg(model, 80, 24)
	// サイズ反映後の View が正常に呼べること (パニックしない)
	_ = updated.View()
}

// T-156: View() でログ行がある場合、ログが含まれている
func TestModel_View_WithLogLines(t *testing.T) {
	t.Parallel()
	ch := make(chan event.Event)
	ctx := context.Background()
	model := tui.NewForTest(ctx, ch)

	ev := event.Event{
		Kind:  event.KindStop,
		Title: "view-test-title",
	}
	updated, _ := tui.SimulateEventArrived(model, ev)
	view := updated.View()

	const wantFragment = "view-test-title"
	if !containsStr(view, wantFragment) {
		t.Errorf("View() with logs: %q が含まれていない: got %q", wantFragment, view)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

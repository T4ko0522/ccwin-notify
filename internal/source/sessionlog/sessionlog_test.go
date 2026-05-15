package sessionlog_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/source/sessionlog"
)

// fakePublisher は EventPublisher のテスト実装 (process Source のテストと同型)。
type fakePublisher struct {
	mu       sync.Mutex
	received []event.Event
}

func (f *fakePublisher) Publish(_ context.Context, ev event.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.received = append(f.received, ev)
	return nil
}

func (f *fakePublisher) Events() []event.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]event.Event, len(f.received))
	copy(out, f.received)
	return out
}

// inMemoryWatcher は fsnotify を介さず手でイベントを流せるテスト用 Watcher。
type inMemoryWatcher struct {
	events chan sessionlog.WatchEvent
	errors chan error

	mu    sync.Mutex
	added []string
}

func newInMemoryWatcher() *inMemoryWatcher {
	return &inMemoryWatcher{
		events: make(chan sessionlog.WatchEvent, 32),
		errors: make(chan error, 4),
	}
}

func (w *inMemoryWatcher) Add(p string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.added = append(w.added, p)
	return nil
}

func (w *inMemoryWatcher) Remove(_ string) error                { return nil }
func (w *inMemoryWatcher) Events() <-chan sessionlog.WatchEvent { return w.events }
func (w *inMemoryWatcher) Errors() <-chan error                 { return w.errors }
func (w *inMemoryWatcher) Close() error {
	// 二重 close を避けるため select で防御
	defer func() { _ = recover() }()
	close(w.events)
	close(w.errors)
	return nil
}

func (w *inMemoryWatcher) Added() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, len(w.added))
	copy(out, w.added)
	return out
}

// fireEvent はテストから 1 件の WatchEvent を流す。
func (w *inMemoryWatcher) fireEvent(path string, op sessionlog.WatchOp) {
	w.events <- sessionlog.WatchEvent{Path: path, Op: op}
}

const (
	endTurnLine = `{"type":"assistant","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`
	toolUseLine = `{"type":"assistant","message":{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"x"}]}}`
	userLine    = `{"type":"user","message":{"role":"user"}}`
	badLine     = `not a json`
)

// waitForEventCount は publisher の Event 数が n 件になるまで最大 d 待つ。到達しなければ fail。
func waitForEventCount(t *testing.T, pub *fakePublisher, n int, d time.Duration) []event.Event {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if got := len(pub.Events()); got >= n {
			return pub.Events()
		}
		time.Sleep(10 * time.Millisecond)
	}
	return pub.Events()
}

// A4: 起動時に既存 jsonl の end_turn 行は発火しない
func TestSource_InitialFiles_DoNotFire(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(filepath.Join(projDir, "p1"), 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(projDir, "p1", "s1.jsonl")
	if err := os.WriteFile(jsonl, []byte(endTurnLine+"\n"+endTurnLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pub := &fakePublisher{}
	w := newInMemoryWatcher()
	src := sessionlog.New(pub, sessionlog.Config{
		ProjectsDir: projDir,
		NewWatcher:  func() (sessionlog.Watcher, error) { return w, nil },
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = src.Run(ctx)
		close(done)
	}()

	// initial scan が走るのを少し待つ
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	if got := len(pub.Events()); got != 0 {
		t.Errorf("初期既存ファイルから発火された: %d 件 (want 0)", got)
	}
}

// A1: 既存ファイルに end_turn 行が追記されたら発火する
func TestSource_NewLineWithEndTurn_Fires(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(filepath.Join(projDir, "p1"), 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(projDir, "p1", "s1.jsonl")
	// 起動時の既存内容 (発火対象外)
	if err := os.WriteFile(jsonl, []byte(userLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pub := &fakePublisher{}
	w := newInMemoryWatcher()
	src := sessionlog.New(pub, sessionlog.Config{
		ProjectsDir: projDir,
		NewWatcher:  func() (sessionlog.Watcher, error) { return w, nil },
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = src.Run(ctx) }()

	// initial scan 完了を待つ
	time.Sleep(80 * time.Millisecond)

	// 追記 (end_turn) → Write イベントを流す
	f, err := os.OpenFile(jsonl, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(endTurnLine + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	w.fireEvent(jsonl, sessionlog.OpWrite)

	events := waitForEventCount(t, pub, 1, 2*time.Second)
	if len(events) != 1 {
		t.Fatalf("Publish 件数: got %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Kind != event.KindStop {
		t.Errorf("Kind: got %q, want %q", ev.Kind, event.KindStop)
	}
	if ev.Source != "sessionlog" {
		t.Errorf("Source: got %q, want \"sessionlog\"", ev.Source)
	}
	if ev.Body != "done" {
		t.Errorf("Body: got %q, want %q", ev.Body, "done")
	}
	if ev.ID == "" {
		t.Error("Event.ID が空")
	}
}

// A3: tool_use 行は発火しない
func TestSource_NewLineWithToolUse_NoFire(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(filepath.Join(projDir, "p1"), 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(projDir, "p1", "s1.jsonl")
	if err := os.WriteFile(jsonl, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	pub := &fakePublisher{}
	w := newInMemoryWatcher()
	src := sessionlog.New(pub, sessionlog.Config{
		ProjectsDir: projDir,
		NewWatcher:  func() (sessionlog.Watcher, error) { return w, nil },
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = src.Run(ctx) }()

	time.Sleep(80 * time.Millisecond)

	f, err := os.OpenFile(jsonl, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(toolUseLine + "\n")
	f.WriteString(userLine + "\n")
	f.Close()
	w.fireEvent(jsonl, sessionlog.OpWrite)

	time.Sleep(200 * time.Millisecond)
	if got := len(pub.Events()); got != 0 {
		t.Errorf("発火してはいけない行で発火した: %d 件", got)
	}
}

// 新規 .jsonl ファイルが作成され end_turn 行が書かれたら発火 (新セッション対応)
func TestSource_NewFileCreated_FiresOnFirstLine(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(filepath.Join(projDir, "p1"), 0o755); err != nil {
		t.Fatal(err)
	}

	pub := &fakePublisher{}
	w := newInMemoryWatcher()
	src := sessionlog.New(pub, sessionlog.Config{
		ProjectsDir: projDir,
		NewWatcher:  func() (sessionlog.Watcher, error) { return w, nil },
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = src.Run(ctx) }()

	time.Sleep(80 * time.Millisecond)

	// 新規ファイル作成 → Create + Write を流す
	jsonl := filepath.Join(projDir, "p1", "new_session.jsonl")
	if err := os.WriteFile(jsonl, []byte(endTurnLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.fireEvent(jsonl, sessionlog.OpCreate|sessionlog.OpWrite)

	events := waitForEventCount(t, pub, 1, 2*time.Second)
	if len(events) != 1 {
		t.Fatalf("Publish 件数: got %d, want 1", len(events))
	}
}

// 不正 JSON 行があってもクラッシュせず継続 (D-07 同等)
func TestSource_InvalidJSONLine_Continues(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(filepath.Join(projDir, "p1"), 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(projDir, "p1", "s1.jsonl")
	if err := os.WriteFile(jsonl, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	pub := &fakePublisher{}
	w := newInMemoryWatcher()
	src := sessionlog.New(pub, sessionlog.Config{
		ProjectsDir: projDir,
		NewWatcher:  func() (sessionlog.Watcher, error) { return w, nil },
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = src.Run(ctx) }()

	time.Sleep(80 * time.Millisecond)

	// 不正 JSON → 正常な end_turn の順で append
	f, _ := os.OpenFile(jsonl, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(badLine + "\n")
	f.WriteString(endTurnLine + "\n")
	f.Close()
	w.fireEvent(jsonl, sessionlog.OpWrite)

	events := waitForEventCount(t, pub, 1, 2*time.Second)
	if len(events) != 1 {
		t.Fatalf("Publish 件数: got %d, want 1 (不正 JSON はスキップして次行 parse 継続)", len(events))
	}
}

// 改行未終端の行は次回 Write まで保留される (fragment 対策)
func TestSource_FragmentedLine_WaitsForNewline(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(filepath.Join(projDir, "p1"), 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(projDir, "p1", "s1.jsonl")
	if err := os.WriteFile(jsonl, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	pub := &fakePublisher{}
	w := newInMemoryWatcher()
	src := sessionlog.New(pub, sessionlog.Config{
		ProjectsDir: projDir,
		NewWatcher:  func() (sessionlog.Watcher, error) { return w, nil },
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = src.Run(ctx) }()

	time.Sleep(80 * time.Millisecond)

	// 1) 行を改行抜きで書く → Write イベント (このタイミングでは発火しないはず)
	f1, _ := os.OpenFile(jsonl, os.O_APPEND|os.O_WRONLY, 0o644)
	f1.WriteString(endTurnLine)
	f1.Close()
	w.fireEvent(jsonl, sessionlog.OpWrite)
	time.Sleep(150 * time.Millisecond)
	if got := len(pub.Events()); got != 0 {
		t.Fatalf("改行前に発火された: %d 件", got)
	}

	// 2) 改行を追加で書く → Write イベント (このタイミングで発火するはず)
	f2, _ := os.OpenFile(jsonl, os.O_APPEND|os.O_WRONLY, 0o644)
	f2.WriteString("\n")
	f2.Close()
	w.fireEvent(jsonl, sessionlog.OpWrite)

	events := waitForEventCount(t, pub, 1, 2*time.Second)
	if len(events) != 1 {
		t.Fatalf("Publish 件数: got %d, want 1", len(events))
	}
}

// .jsonl の Remove で offsets エントリが削除される (リーク防止 / 観察可能なのは「再 Create 時に 0 から読む」こと)
func TestSource_RemovedFile_OffsetCleared(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(filepath.Join(projDir, "p1"), 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(projDir, "p1", "s1.jsonl")
	// 初期サイズが大きいファイル (= 起動時 offset が大きい)
	initial := userLine + "\n" + userLine + "\n" + userLine + "\n"
	if err := os.WriteFile(jsonl, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	pub := &fakePublisher{}
	w := newInMemoryWatcher()
	src := sessionlog.New(pub, sessionlog.Config{
		ProjectsDir: projDir,
		NewWatcher:  func() (sessionlog.Watcher, error) { return w, nil },
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = src.Run(ctx) }()

	time.Sleep(80 * time.Millisecond)

	// Remove
	os.Remove(jsonl)
	w.fireEvent(jsonl, sessionlog.OpRemove)
	time.Sleep(50 * time.Millisecond)

	// 同じ path で再 Create + Write (新セッションがたまたま同名で作られた想定)
	if err := os.WriteFile(jsonl, []byte(endTurnLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.fireEvent(jsonl, sessionlog.OpCreate|sessionlog.OpWrite)

	events := waitForEventCount(t, pub, 1, 2*time.Second)
	if len(events) != 1 {
		t.Fatalf("Remove → 再 Create 後の end_turn が発火しなかった: got %d, want 1", len(events))
	}
}

// ProjectsDir が存在しないときはクラッシュせず idle (ctx 待ち) (D-10)
func TestSource_ProjectsDirMissing_DoesNotCrash(t *testing.T) {
	t.Parallel()
	pub := &fakePublisher{}
	src := sessionlog.New(pub, sessionlog.Config{
		ProjectsDir: filepath.Join(os.TempDir(), "ccwin-notify-nonexistent-xyzzy"),
		NewWatcher: func() (sessionlog.Watcher, error) {
			t.Fatal("ProjectsDir 不在時に Watcher を生成してはいけない")
			return nil, nil
		},
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := src.Run(ctx); err == nil {
		t.Error("ctx timeout 時に Run は ctx.Err() を返すべき")
	}
}

// New のデフォルト値が適用される
func TestNew_DefaultsApplied(t *testing.T) {
	t.Parallel()
	pub := &fakePublisher{}
	src := sessionlog.New(pub, sessionlog.Config{}, nil)
	if src == nil {
		t.Fatal("New returned nil")
	}
	// ctx 即 cancel で Run が ctx.Err() を返す (= ProjectsDir 解決後にイベントループまで進める)
	// ただし ProjectsDir が無い環境では Stat エラーで idle に入って ctx 待ちになる ─ どちらでも ctx.Err()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := src.Run(ctx); err == nil {
		t.Error("ctx timeout 時に Run は ctx.Err() を返すべき")
	}
}

// Polling が fsnotify Write を受けなくても追記を拾うことを検証 (fsnotify 遅延補完)。
// 既存ファイルに追記し、Watcher.fireEvent を呼ばずに PollInterval だけ待つ。
func TestSource_PollingPicksUpAppendWithoutFsnotify(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(filepath.Join(projDir, "p1"), 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(projDir, "p1", "s1.jsonl")
	if err := os.WriteFile(jsonl, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	pub := &fakePublisher{}
	w := newInMemoryWatcher()
	src := sessionlog.New(pub, sessionlog.Config{
		ProjectsDir:  projDir,
		PollInterval: 40 * time.Millisecond,
		NewWatcher:   func() (sessionlog.Watcher, error) { return w, nil },
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = src.Run(ctx) }()

	// initial scan 完了を待つ
	time.Sleep(80 * time.Millisecond)

	// 追記する (Watcher.fireEvent は呼ばない = fsnotify Write が来ないシナリオ)
	f, err := os.OpenFile(jsonl, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(endTurnLine + "\n")
	f.Close()

	// Polling 2 回分を待つ
	events := waitForEventCount(t, pub, 1, 2*time.Second)
	if len(events) != 1 {
		t.Fatalf("Polling で発火しなかった: got %d, want 1", len(events))
	}
}

// 新しいサブディレクトリ (= 新プロジェクト) が Create されたら Watcher.Add される (D-11)
func TestSource_NewSubdirCreated_IsWatched(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	projDir := filepath.Join(root, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	pub := &fakePublisher{}
	w := newInMemoryWatcher()
	src := sessionlog.New(pub, sessionlog.Config{
		ProjectsDir: projDir,
		NewWatcher:  func() (sessionlog.Watcher, error) { return w, nil },
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = src.Run(ctx) }()

	time.Sleep(80 * time.Millisecond)

	// 新サブディレクトリ作成 → Create イベント
	newProj := filepath.Join(projDir, "new_project")
	if err := os.Mkdir(newProj, 0o755); err != nil {
		t.Fatal(err)
	}
	w.fireEvent(newProj, sessionlog.OpCreate)
	time.Sleep(80 * time.Millisecond)

	addedAfter := w.Added()
	foundNew := false
	for _, p := range addedAfter {
		if p == newProj {
			foundNew = true
			break
		}
	}
	if !foundNew {
		t.Errorf("新サブディレクトリ %q が Watcher.Add されていない (added=%v)", newProj, addedAfter)
	}
}

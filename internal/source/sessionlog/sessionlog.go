// Package sessionlog は Claude Code のセッションログ (jsonl) を fsnotify で監視し、
// アシスタント応答の終了 (stop_reason in {end_turn, stop_sequence}) を検知して
// event.KindStop を Bus に Publish するソース実装。
//
// Hooks 経路 (settings.json) に依存せず、Claude Code core が必ず書き出す
// 永続化ログを直接観測することで、ターン終了通知の信頼性を確保する。
package sessionlog

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
)

// EventPublisher は event.Bus.Publish の抽象 (process.EventPublisher と同形)。
// orchestrator から busPublisher を渡す。
type EventPublisher interface {
	Publish(ctx context.Context, e event.Event) error
}

// WatchOp は fsnotify から抽象化したファイルシステムイベント種別 (bit field)。
type WatchOp int

const (
	OpCreate WatchOp = 1 << iota
	OpWrite
	OpRemove
	OpRename
)

// WatchEvent は Watcher が通知する 1 件のイベント。
type WatchEvent struct {
	Path string
	Op   WatchOp
}

// Watcher は fsnotify を抽象化したインターフェース (D-09)。
// テストでは inMemoryWatcher を差し替える。
type Watcher interface {
	Add(path string) error
	Remove(path string) error
	Events() <-chan WatchEvent
	Errors() <-chan error
	Close() error
}

// Config は Source の挙動を制御する設定。
type Config struct {
	// ProjectsDir は監視する Claude Code projects ディレクトリ。
	// 空のとき $USERPROFILE/.claude/projects を既定値とする。
	ProjectsDir string
	// BodyMaxLen は event.Body の truncate 長 (rune 単位)。<=0 はデフォルト 200。
	BodyMaxLen int
	// PollInterval は fsnotify を補完する Stat ポーリング間隔。<=0 で 1s。
	// Claude Code が tool_use 行を書いた直後にバッファを flush しないため
	// fsnotify Write が遅延するケースがあり、これを polling で補う (出した瞬間に検知できるように)。
	PollInterval time.Duration
	// NewWatcher が nil なら fsnotify ベースの実装が使われる (D-09 テスト容易性)。
	NewWatcher func() (Watcher, error)
}

// Source は jsonl 監視ソース本体。
type Source struct {
	publisher EventPublisher
	cfg       Config
	logger    *slog.Logger

	mu      sync.Mutex
	offsets map[string]int64
}

// New は Source を生成する。
// cfg のフィールドが未指定なら以下の既定値を適用:
//   - ProjectsDir 空: $USERPROFILE/.claude/projects
//   - BodyMaxLen <=0: 200
//   - NewWatcher nil: fsnotify ベース実装
func New(publisher EventPublisher, cfg Config, logger *slog.Logger) *Source {
	if cfg.BodyMaxLen <= 0 {
		cfg.BodyMaxLen = defaultBodyMaxLen
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.NewWatcher == nil {
		cfg.NewWatcher = newFsnotifyWatcher
	}
	if cfg.ProjectsDir == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			cfg.ProjectsDir = filepath.Join(home, ".claude", "projects")
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Source{
		publisher: publisher,
		cfg:       cfg,
		logger:    logger,
		offsets:   map[string]int64{},
	}
}

// Run は ctx が cancel されるまでブロックする。
//   - 起動時: ProjectsDir 配下のサブディレクトリを Watcher.Add し、既存 .jsonl の Size を offset 初期値とする
//     (= 起動前の行は発火しない / D-06)。
//   - イベントループ: Watcher.Events を受けてサブディレクトリ追加 / 新規 jsonl / 追記読み込み / 削除を処理。
//   - ProjectsDir が存在しないときはクラッシュせず Warn を出して ctx を待つ (D-10)。
func (s *Source) Run(ctx context.Context) error {
	if s.cfg.ProjectsDir == "" {
		s.logger.Warn("sessionlog source: projects_dir not resolved, source idle")
		<-ctx.Done()
		return ctx.Err()
	}
	if _, err := os.Stat(s.cfg.ProjectsDir); err != nil {
		s.logger.Warn("sessionlog source: projects_dir not found, source idle",
			"dir", s.cfg.ProjectsDir, "err", err.Error())
		<-ctx.Done()
		return ctx.Err()
	}

	// Windows の junction / symlink を実体パスに解決する。
	// ~/.claude が junction (例: dotfiles\.config\claude を指す) の場合、
	// fsnotify (ReadDirectoryChangesW) は junction 越しの Write を通知しないので
	// 実体ディレクトリを watch する必要がある。
	// filepath.EvalSymlinks は Windows の junction では失敗するケースがあるため
	// 親要素を順に os.Readlink で辿る resolveJunctionPath を自前実装する。
	if resolved, err := resolveJunctionPath(s.cfg.ProjectsDir); err != nil {
		s.logger.Warn("sessionlog source: resolve junction failed (using original path)",
			"dir", s.cfg.ProjectsDir, "err", err.Error())
	} else if resolved != s.cfg.ProjectsDir {
		s.logger.Info("sessionlog source: resolved symlink/junction",
			"from", s.cfg.ProjectsDir, "to", resolved)
		s.cfg.ProjectsDir = resolved
	}

	watcher, err := s.cfg.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	s.initialScan(watcher)
	s.logger.Info("sessionlog source: ready",
		"dir", s.cfg.ProjectsDir, "tracked_files", len(s.offsets),
		"poll_interval", s.cfg.PollInterval)

	pollTicker := time.NewTicker(s.cfg.PollInterval)
	defer pollTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-watcher.Events():
			if !ok {
				return nil
			}
			s.handleEvent(ctx, watcher, ev)
		case werr, ok := <-watcher.Errors():
			if !ok {
				// Errors チャネルが閉じてもループは続ける
				continue
			}
			if werr != nil {
				s.logger.Warn("sessionlog source: watcher error", "err", werr.Error())
			}
		case <-pollTicker.C:
			s.pollKnownFiles(ctx)
		}
	}
}

// initialScan は起動時に ProjectsDir とその直下サブディレクトリを Watcher.Add し、
// 各 .jsonl の現在サイズを offset 初期値として記録する (D-06)。
func (s *Source) initialScan(w Watcher) {
	if err := w.Add(s.cfg.ProjectsDir); err != nil {
		s.logger.Warn("sessionlog source: add projects_dir failed",
			"dir", s.cfg.ProjectsDir, "err", err.Error())
		return
	}
	entries, err := os.ReadDir(s.cfg.ProjectsDir)
	if err != nil {
		s.logger.Warn("sessionlog source: read projects_dir failed", "err", err.Error())
		return
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		sub := filepath.Join(s.cfg.ProjectsDir, ent.Name())
		if err := w.Add(sub); err != nil {
			s.logger.Warn("sessionlog source: add subdir failed",
				"dir", sub, "err", err.Error())
			continue
		}
		files, err := os.ReadDir(sub)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			fpath := filepath.Join(sub, f.Name())
			st, err := os.Stat(fpath)
			if err != nil {
				continue
			}
			s.mu.Lock()
			s.offsets[fpath] = st.Size()
			s.mu.Unlock()
		}
	}
}

// handleEvent は 1 件の WatchEvent を処理する。
//   - サブディレクトリの Create: Watcher.Add する (新プロジェクト追従 / D-11)
//   - .jsonl の Create: offset=0 で読み込み開始 (新セッション)
//   - .jsonl の Write: 前回 offset 以降を追記読み込み
//   - .jsonl の Remove/Rename: offsets から削除
func (s *Source) handleEvent(ctx context.Context, w Watcher, ev WatchEvent) {
	// Create 経由でディレクトリが現れたら Watcher.Add
	if ev.Op&OpCreate != 0 {
		if info, err := os.Stat(ev.Path); err == nil && info.IsDir() {
			if err := w.Add(ev.Path); err != nil {
				s.logger.Warn("sessionlog source: add new subdir failed",
					"dir", ev.Path, "err", err.Error())
			}
			return
		}
	}

	// jsonl 以外は無視
	if !strings.HasSuffix(ev.Path, ".jsonl") {
		return
	}

	if ev.Op&(OpRemove|OpRename) != 0 {
		s.mu.Lock()
		delete(s.offsets, ev.Path)
		s.mu.Unlock()
		return
	}

	if ev.Op&OpCreate != 0 {
		s.mu.Lock()
		// 新規ファイルの場合は 0 から読む (= 過去行スキップではない)
		if _, known := s.offsets[ev.Path]; !known {
			s.offsets[ev.Path] = 0
		}
		s.mu.Unlock()
	}

	if ev.Op&(OpWrite|OpCreate) != 0 {
		s.readNewLines(ctx, ev.Path)
	}
}

// resolveJunctionPath は与えられたパスの先祖要素を順に os.Readlink して
// junction / symlink を解決し、最終的な実体パスを返す。
//   - p 自身が link なら target に置き換える
//   - そうでなければ親方向に辿り、最初に Readlink できた要素を target に差し替え、
//     残りのサブパスを append して再構築する
//   - どの祖先も link でなければ filepath.Clean(p) を返す (no-op)
//
// EvalSymlinks が Windows の junction で "path not found" を返すケースの代替。
func resolveJunctionPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p, err
	}
	// p 自身が link?
	if t, err := os.Readlink(abs); err == nil && t != "" {
		// link target がさらに link の可能性もあるが、現実的に 1 段で十分なので 1 度だけ解決
		return filepath.Clean(t), nil
	}
	rest := ""
	cur := abs
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			// ルート到達: link は見つからなかった
			return filepath.Clean(abs), nil
		}
		base := filepath.Base(cur)
		if rest == "" {
			rest = base
		} else {
			rest = filepath.Join(base, rest)
		}
		cur = parent
		if t, err := os.Readlink(cur); err == nil && t != "" {
			return filepath.Clean(filepath.Join(t, rest)), nil
		}
	}
}

// pollKnownFiles は offsets に登録済みの jsonl ファイルすべてに対して
// 現在サイズを Stat し、offset より大きければ readNewLines を呼ぶ。
//
// Claude Code が tool_use 行を書いた直後にバッファを flush しないケースがあり、
// fsnotify が Write イベントを次の書き込みまで遅らせる挙動が観測された。
// このポーリングで fsnotify が取りこぼした追記分を確実に拾う。
func (s *Source) pollKnownFiles(ctx context.Context) {
	s.mu.Lock()
	paths := make([]string, 0, len(s.offsets))
	for p := range s.offsets {
		paths = append(paths, p)
	}
	s.mu.Unlock()

	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		s.mu.Lock()
		offset := s.offsets[p]
		s.mu.Unlock()
		if st.Size() > offset {
			s.readNewLines(ctx, p)
		}
	}
}

// readNewLines は path の前回 offset 以降を 1 行ずつ読み、parseLine の判定で Stop event を Publish する。
// 改行で終わらない末尾の fragment は次回 Write イベントを待つため offset を進めない。
func (s *Source) readNewLines(ctx context.Context, path string) {
	s.mu.Lock()
	offset := s.offsets[path]
	s.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.logger.Warn("sessionlog source: open failed",
				"path", path, "err", err.Error())
		}
		return
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		s.logger.Warn("sessionlog source: seek failed",
			"path", path, "err", err.Error())
		return
	}

	r := bufio.NewReaderSize(f, 64*1024)
	newOffset := offset
	for {
		line, rerr := r.ReadString('\n')
		if errors.Is(rerr, io.EOF) {
			// 改行で終わらない残部は次回 Write で完成するので offset は進めない
			break
		}
		if rerr != nil {
			s.logger.Warn("sessionlog source: read failed",
				"path", path, "err", rerr.Error())
			break
		}
		newOffset += int64(len(line))

		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			continue
		}
		res, perr := parseLine([]byte(trimmed), s.cfg.BodyMaxLen)
		if perr != nil {
			s.logger.Debug("sessionlog source: parse failed",
				"path", path, "err", perr.Error())
			continue
		}
		if !res.Fire {
			continue
		}
		ev := event.Event{
			ID:        event.NewID(),
			Kind:      res.Kind,
			Title:     res.Title,
			Body:      res.Body,
			Source:    "sessionlog",
			Timestamp: time.Now(),
		}
		s.logger.Info("sessionlog source: publish",
			"kind", res.Kind, "title", res.Title)
		if perr := s.publisher.Publish(ctx, ev); perr != nil {
			s.logger.Warn("sessionlog source: publish failed", "err", perr.Error())
		}
	}

	s.mu.Lock()
	s.offsets[path] = newOffset
	s.mu.Unlock()
}

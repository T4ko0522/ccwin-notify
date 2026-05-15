package sessionlog

import (
	"github.com/fsnotify/fsnotify"
)

// fsnotifyWatcher は fsnotify.Watcher を Watcher インターフェースに適合させるラッパ。
// fsnotify の Op を WatchOp に変換し、自身の Events チャネルに転送する goroutine を持つ。
type fsnotifyWatcher struct {
	w      *fsnotify.Watcher
	events chan WatchEvent
	done   chan struct{}
}

func newFsnotifyWatcher() (Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	fsw := &fsnotifyWatcher{
		w:      fw,
		events: make(chan WatchEvent, 64),
		done:   make(chan struct{}),
	}
	go fsw.translate()
	return fsw, nil
}

// translate は fsnotify.Events を WatchEvent に変換して自前チャネルに転送する。
// fsw.done が閉じられたら終了する。
func (f *fsnotifyWatcher) translate() {
	for {
		select {
		case <-f.done:
			return
		case ev, ok := <-f.w.Events:
			if !ok {
				return
			}
			op := WatchOp(0)
			if ev.Op&fsnotify.Create != 0 {
				op |= OpCreate
			}
			if ev.Op&fsnotify.Write != 0 {
				op |= OpWrite
			}
			if ev.Op&fsnotify.Remove != 0 {
				op |= OpRemove
			}
			if ev.Op&fsnotify.Rename != 0 {
				op |= OpRename
			}
			if op == 0 {
				continue
			}
			select {
			case f.events <- WatchEvent{Path: ev.Name, Op: op}:
			case <-f.done:
				return
			}
		}
	}
}

func (f *fsnotifyWatcher) Add(p string) error        { return f.w.Add(p) }
func (f *fsnotifyWatcher) Remove(p string) error     { return f.w.Remove(p) }
func (f *fsnotifyWatcher) Events() <-chan WatchEvent { return f.events }
func (f *fsnotifyWatcher) Errors() <-chan error      { return f.w.Errors }

// Close は translate goroutine を停止し fsnotify.Watcher を閉じる。
// 二重 Close を避けるため done チャネルの状態をチェックする。
func (f *fsnotifyWatcher) Close() error {
	select {
	case <-f.done:
		// 既に閉じている
		return nil
	default:
		close(f.done)
	}
	return f.w.Close()
}

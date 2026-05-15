// Package wezterm は WezTerm ターミナルの可視テキストを `wezterm cli get-text` で
// 定期取得し、Claude Code の AskUserQuestion / ExitPlanMode 等
// ユーザー入力待ち UI が表示された瞬間を検知して Notification event を発火するソース。
//
// Claude Code の AskUserQuestion / ExitPlanMode は組み込み UI パネルとして実装され、
// PreToolUse hook を発火せず、jsonl にも応答時までフラッシュされない。そのため
// ターミナルに描画された UI 文字列パターン (例: "Enter to select · ↑/↓ to navigate · Esc to cancel")
// を観測することが現状で唯一の検知手段となる。
//
// 実装方針:
//   - 1 秒間隔で wezterm cli get-text を実行 (PollInterval で調整可)
//   - 取得テキストに Signature を部分一致検索
//   - false→true 状態遷移時のみ Bus に Publish (重複抑止)
//
// WezTerm 専用。他のターミナル (Windows Terminal / Alacritty / cmd 等) では動作しない。
package wezterm

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/event"
)

// EventPublisher は event.Bus.Publish の抽象 (sessionlog.EventPublisher と同形)。
type EventPublisher interface {
	Publish(ctx context.Context, e event.Event) error
}

// TextFetcher は WezTerm pane の現在の可視テキストを返す抽象。
// テストでは差し替え可能。本番では wezterm.exe を exec する実装が入る。
type TextFetcher interface {
	FetchText(ctx context.Context) (string, error)
}

// Config は wezterm Source の挙動を制御する設定。
type Config struct {
	// PaneID は監視対象の wezterm pane ID。負数のとき 0 を既定とする。
	PaneID int
	// PollInterval は wezterm cli 呼び出し間隔。<=0 で 1s。
	PollInterval time.Duration
	// Signature は Notification を発火するための部分一致文字列。
	// 空のとき defaultSignature を使う。
	Signature string
	// Title は発火する Notification event の Title。空のとき defaultTitle。
	Title string
	// Body は発火する Notification event の Body。空のとき defaultBody。
	Body string
	// Fetcher が nil なら wezterm cli を exec する本番実装が使われる。
	Fetcher TextFetcher
}

// defaultSignature は Claude Code の選択 UI に必ず表示される行末ヒント。
// AskUserQuestion / ExitPlanMode / 内部の permission prompt 等、選択操作を要する
// すべての UI パネルで共通して描画されることを実機 PoC で確認済み。
const defaultSignature = "Enter to select"

const (
	defaultTitle = "Claude needs your input"
	defaultBody  = "Waiting for your response"
)

// Source は WezTerm pane 監視ソース本体。
type Source struct {
	publisher EventPublisher
	cfg       Config
	logger    *slog.Logger

	// detected は前回 poll での Signature 検出状態。
	// false→true への遷移時のみ Publish する (重複抑止)。
	detected bool
}

// New は Source を生成する。Config の未指定フィールドには既定値を適用。
func New(publisher EventPublisher, cfg Config, logger *slog.Logger) *Source {
	if cfg.PaneID < 0 {
		cfg.PaneID = 0
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.Signature == "" {
		cfg.Signature = defaultSignature
	}
	if cfg.Title == "" {
		cfg.Title = defaultTitle
	}
	if cfg.Body == "" {
		cfg.Body = defaultBody
	}
	if cfg.Fetcher == nil {
		cfg.Fetcher = &wezCliFetcher{paneID: cfg.PaneID}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Source{
		publisher: publisher,
		cfg:       cfg,
		logger:    logger,
	}
}

// Run は ctx が cancel されるまで定期 polling を行う。
//   - 毎 PollInterval で FetchText を呼ぶ。
//   - 取得テキストに Signature が現れた瞬間 (false→true) のみ Notification を Publish。
//   - true→false 遷移は無音 (UI が閉じただけ)。
//   - FetchText 失敗 (wezterm.exe 不在等) は debug ログだけ出して継続する。
//     初回起動直後の一時的な失敗 (wezterm 起動前) を想定。
func (s *Source) Run(ctx context.Context) error {
	s.logger.Info("wezterm source: ready",
		"pane_id", s.cfg.PaneID,
		"poll_interval", s.cfg.PollInterval,
		"signature", s.cfg.Signature)

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.pollOnce(ctx)
		}
	}
}

// pollOnce は 1 回分の poll を実行する。テストから直接呼ぶこともできる。
func (s *Source) pollOnce(ctx context.Context) {
	text, err := s.cfg.Fetcher.FetchText(ctx)
	if err != nil {
		s.logger.Debug("wezterm source: fetch text failed", "err", err.Error())
		return
	}

	matched := strings.Contains(text, s.cfg.Signature)
	if matched && !s.detected {
		// false→true: ユーザー入力待ち UI が出現
		ev := event.Event{
			ID:        event.NewID(),
			Kind:      event.KindNotification,
			Title:     s.cfg.Title,
			Body:      s.cfg.Body,
			Source:    "wezterm",
			Timestamp: time.Now(),
		}
		s.logger.Info("wezterm source: publish",
			"kind", ev.Kind, "title", ev.Title)
		if perr := s.publisher.Publish(ctx, ev); perr != nil {
			s.logger.Warn("wezterm source: publish failed", "err", perr.Error())
		}
	}
	s.detected = matched
}

// wezCliFetcher は wezterm.exe を起動して pane text を取得する本番実装。
type wezCliFetcher struct {
	paneID int
}

// FetchText は `wezterm cli get-text --pane-id <id>` を実行して標準出力を返す。
// wezterm.exe が PATH にない / 該当 pane が存在しない場合はエラー。
func (f *wezCliFetcher) FetchText(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "wezterm", "cli", "get-text", "--pane-id", fmt.Sprintf("%d", f.paneID))
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("wezterm: get-text: %w", err)
	}
	return string(out), nil
}

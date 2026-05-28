// Package daemon のハンドラ群 (HandleStatus / HandleTest / HandleHealthz)。
package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/t4ko0522/ccwin-notify/internal/apiclient"
	"github.com/t4ko0522/ccwin-notify/internal/config"
	"github.com/t4ko0522/ccwin-notify/internal/event"
	"github.com/t4ko0522/ccwin-notify/internal/ipc/sse"
	"github.com/t4ko0522/ccwin-notify/internal/notifier"
	"github.com/t4ko0522/ccwin-notify/internal/secret"
)

const version = "0.1.0"

// maxRequestBodyBytes は POST 系ハンドラのリクエストボディ上限 (DoS 対策)。
const maxRequestBodyBytes = 64 * 1024

// HandleHealthz は GET /v1/healthz ハンドラ (認証不要)。
func HandleHealthz(startedAt time.Time, v string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := r.Context(), func() {}
		_ = ctx
		defer cancel()

		resp := map[string]interface{}{
			"status":         "ok",
			"app":            "ccwin-notify",
			"version":        v,
			"pid":            pidSelf(),
			"started_at":     startedAt.Format(time.RFC3339),
			"uptime_seconds": int(time.Since(startedAt).Seconds()),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// HandleStatus は GET /v1/status ハンドラ (Bearer required — caller で auth を適用する)。
func HandleStatus(d *Dispatcher, bus event.Bus, sseHub *sse.Hub, cfg *config.Config, authToken secret.SecretString, startedAt time.Time, portFile string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Queue stat
		queueStat := apiclient.QueueStat{
			Len:      0, // Bus は現時点で Len を公開しない (PENDING: Bus.Stat)
			Capacity: cfg.Queue.Capacity,
			Policy:   cfg.Queue.Policy,
		}

		// Sources stat
		sources := map[string]apiclient.SourceStat{
			"hooks": {
				Enabled: cfg.Sources.Hooks.Enabled,
				State:   stateString(cfg.Sources.Hooks.Enabled),
			},
			"process": {
				Enabled: cfg.Sources.Process.Enabled,
				State:   stateString(cfg.Sources.Process.Enabled),
			},
			"sessionlog": {
				Enabled: cfg.Sources.Sessionlog.Enabled,
				State:   stateString(cfg.Sources.Sessionlog.Enabled),
			},
			"codexlog": {
				Enabled: cfg.Sources.Codexlog.Enabled,
				State:   stateString(cfg.Sources.Codexlog.Enabled),
			},
			"wezterm": {
				Enabled: cfg.Sources.Wezterm.Enabled,
				State:   stateString(cfg.Sources.Wezterm.Enabled),
			},
		}

		// Notifiers stat
		notifiers := map[string]apiclient.NotifierStat{
			"toast": {
				Enabled: cfg.Notifiers.Toast.Enabled,
			},
			"sound": {
				Enabled: cfg.Notifiers.Sound.Enabled,
			},
			"webhook.discord": {
				Enabled: cfg.Notifiers.Webhook.Discord.Enabled,
			},
			"webhook.slack": {
				Enabled: cfg.Notifiers.Webhook.Slack.Enabled,
			},
		}

		// Auth stat
		authStat := apiclient.AuthStat{
			TokenPresent: true,
			TokenValue:   authToken,
		}

		resp := apiclient.Status{
			PID:       pidSelf(),
			Version:   version,
			StartedAt: startedAt,
			PortFile:  portFile,
			Queue:     queueStat,
			Sources:   sources,
			Notifiers: notifiers,
			Auth:      authStat,
			LastPanic: "",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// stateString は enabled フラグを "running" / "disabled" に変換する。
func stateString(enabled bool) string {
	if enabled {
		return "running"
	}
	return "disabled"
}

// HandleTest は POST /v1/test ハンドラ (D-35 / D-40)。
// target_notifier + kind を検証して d.SubmitAndCollect を呼ぶ。
// 409 Conflict: target_notifier が disabled / kind_mask 不一致
// 503: dispatcher closing
func HandleTest(d *Dispatcher, notifiers []notifier.Notifier, notifiersCfg config.NotifiersConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Kind           event.EventKind `json:"kind"`
			TargetNotifier string          `json:"target_notifier"`
			Title          string          `json:"title"`
			Body           string          `json:"body"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeJSONErr(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds 64KiB")
				return
			}
			writeJSONErr(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}

		if !event.ValidKinds[req.Kind] {
			writeJSONErr(w, http.StatusBadRequest, "invalid_kind", "unknown EventKind '"+string(req.Kind)+"'")
			return
		}

		now := time.Now()
		ev := event.Event{
			ID:        event.NewID(),
			Kind:      req.Kind,
			Title:     req.Title,
			Body:      req.Body,
			Source:    "test",
			Timestamp: now,
		}

		// target_notifier = "all" または 特定名の場合の検証
		if req.TargetNotifier != "all" && req.TargetNotifier != "" {
			ok, reason := checkNotifierEnabled(req.TargetNotifier, req.Kind, notifiers, notifiersCfg)
			if !ok {
				writeJSONErr(w, http.StatusConflict, "conflict_disabled",
					"notifier '"+req.TargetNotifier+"' is disabled (or '"+string(req.Kind)+"' is masked): "+reason)
				return
			}
		}

		out, err := d.SubmitAndCollectTargeted(ev, req.TargetNotifier)
		if err != nil {
			if errors.Is(err, ErrDispatcherClosing) {
				writeJSONErr(w, http.StatusServiceUnavailable, "shutting_down", "daemon is shutting down")
				return
			}
			writeJSONErr(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}

		var results []apiclient.NotifyOutcome
		for res := range out {
			errStr := ""
			if res.Err != nil {
				errStr = res.Err.Error()
			}
			results = append(results, apiclient.NotifyOutcome{
				Notifier: res.Notifier,
				OK:       res.OK,
				Error:    errStr,
			})
		}
		if results == nil {
			results = []apiclient.NotifyOutcome{}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiclient.TestResult{
			EventID: ev.ID,
			Results: results,
		})
	}
}

// checkNotifierEnabled は指定 Notifier が enabled かつ kind_mask に合致するか確認する (D-40)。
func checkNotifierEnabled(target string, kind event.EventKind, notifiers []notifier.Notifier, cfg config.NotifiersConfig) (bool, string) {
	switch target {
	case "toast":
		if !cfg.Toast.Enabled {
			return false, "toast is disabled"
		}
		if len(cfg.Toast.KindMask) > 0 && !cfg.Toast.KindMask[kind] {
			return false, string(kind) + " is masked for toast"
		}
	case "sound":
		if !cfg.Sound.Enabled {
			return false, "sound is disabled"
		}
		if len(cfg.Sound.KindMask) > 0 && !cfg.Sound.KindMask[kind] {
			return false, string(kind) + " is masked for sound"
		}
	case "webhook.discord":
		if !cfg.Webhook.Discord.Enabled {
			return false, "webhook.discord is disabled"
		}
		if len(cfg.Webhook.Discord.KindMask) > 0 && !cfg.Webhook.Discord.KindMask[kind] {
			return false, string(kind) + " is masked for webhook.discord"
		}
	case "webhook.slack":
		if !cfg.Webhook.Slack.Enabled {
			return false, "webhook.slack is disabled"
		}
		if len(cfg.Webhook.Slack.KindMask) > 0 && !cfg.Webhook.Slack.KindMask[kind] {
			return false, string(kind) + " is masked for webhook.slack"
		}
	default:
		return false, "unknown notifier '" + target + "'"
	}
	return true, ""
}

func writeJSONErr(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

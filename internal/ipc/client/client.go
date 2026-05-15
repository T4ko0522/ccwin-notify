package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
)

// PortfileContent は %APPDATA%/ccwin-notify/daemon.port の JSON スキーマ (§4.1 拡張版)。
type PortfileContent struct {
	App              string `json:"app"`
	Version          string `json:"version"`
	PID              int    `json:"pid"`
	Port             int    `json:"port"`
	StartedAt        string `json:"started_at"`
	TokenFingerprint string `json:"token_fingerprint"`
}

// ErrPortfileInvalid は portfile のフォーマットが不正。
var ErrPortfileInvalid = errors.New("portfile: invalid format")

// ReadPortfile は portfile を読み込み PortfileContent を返す。
// ファイル不在は os.ErrNotExist でラップされた error を返す。
// フォーマット不一致は ErrPortfileInvalid を返す。
func ReadPortfile(path string) (*PortfileContent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("portfile read: %w", err)
	}
	var pf PortfileContent
	if err := json.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPortfileInvalid, err)
	}
	if pf.App != "ccwin-notify" {
		return nil, fmt.Errorf("%w: app field mismatch (got %q)", ErrPortfileInvalid, pf.App)
	}
	if pf.Port <= 0 {
		return nil, fmt.Errorf("%w: invalid port %d", ErrPortfileInvalid, pf.Port)
	}
	return &pf, nil
}

// NewHTTPClient は Bearer 付きの *http.Client を返すヘルパー。
// apiclient が内部で利用する。
func NewHTTPClient(token string) *http.Client {
	return &http.Client{
		Transport: &bearerTransport{
			token: token,
			base:  http.DefaultTransport,
		},
	}
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Request をコピーしてヘッダを追加 (元の Request を変更しない)
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

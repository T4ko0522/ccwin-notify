// internal/ipc/middleware パッケージの Bearer 認証テスト (サイクル 3)
// テスト ID: T-020〜T-025
// 受入条件: A6 / A7 / I2 / M-BEARER-HASH (D-23 / D-39 / §4.2.1)
// Bearer は SHA-256 ハッシュ固定長 32 byte 比較
package middleware_test

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/t4ko0522/ccwin-notify/internal/ipc/middleware"
)

// テスト用のトークン
const testToken = "test-bearer-token-abcdef1234567890"

// testExpectedHash は testToken の SHA-256 ダイジェスト
var testExpectedHash = sha256.Sum256([]byte(testToken))

// 認証ミドルウェアを適用したテスト用サーバを構築する
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/protected", middleware.RequireBearer(testExpectedHash)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}),
	))
	return httptest.NewServer(mux)
}

// T-025: 正しい Bearer token → 200
func TestRequireBearer_ValidToken(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/protected", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("ステータス: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// T-020: Authorization ヘッダ欠落 → 401
func TestRequireBearer_MissingHeader(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/protected", nil)
	// Authorization ヘッダを付けない

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("ヘッダ欠落: got %d, want 401", resp.StatusCode)
	}
}

// T-021: プレフィクス不正 ("Bearer " なし、トークン直書き) → 401
func TestRequireBearer_MissingBearerPrefix(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/protected", nil)
	req.Header.Set("Authorization", testToken) // "Bearer " プレフィクスなし

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("プレフィクス不正: got %d, want 401", resp.StatusCode)
	}
}

// T-022: 複数の Authorization ヘッダ → 401
func TestRequireBearer_MultipleAuthorizationHeaders(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/protected", nil)
	req.Header.Add("Authorization", "Bearer "+testToken)
	req.Header.Add("Authorization", "Bearer "+testToken) // 2 回追加

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("複数ヘッダ: got %d, want 401", resp.StatusCode)
	}
}

// T-023: 空の Authorization ヘッダ → 401
func TestRequireBearer_EmptyHeader(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/protected", nil)
	req.Header.Set("Authorization", "") // 空文字

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("空ヘッダ: got %d, want 401", resp.StatusCode)
	}
}

// T-024 / M-BEARER-HASH: 長さの異なるトークン → 401 (SHA-256 ハッシュ後に 32 byte 同士比較なので constant-time)
func TestRequireBearer_DifferentLengthToken(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	defer srv.Close()

	// 短いトークン (長さ異なる)
	shortToken := "short"
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/protected", nil)
	req.Header.Set("Authorization", "Bearer "+shortToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("短いトークン: got %d, want 401", resp.StatusCode)
	}
}

// T-024 / M-BEARER-HASH: 別ユーザーの同長トークン → 401
func TestRequireBearer_WrongToken(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	defer srv.Close()

	// testToken と同じ長さだが内容が異なるトークン
	wrongToken := "wrong-bearer-token-xxxxxxxx1234567890"
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/protected", nil)
	req.Header.Set("Authorization", "Bearer "+wrongToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("間違いトークン: got %d, want 401", resp.StatusCode)
	}
}

// T-024 / M-BEARER-HASH: SHA-256 ハッシュ化して比較されていることを確認
// 同じトークンでも異なるハッシュを expected に渡すと 401 になる
func TestRequireBearer_HashComparison(t *testing.T) {
	t.Parallel()
	// 別のハッシュを expected に設定したサーバを作る
	otherHash := sha256.Sum256([]byte("different-expected-token"))
	mux := http.NewServeMux()
	mux.Handle("/protected", middleware.RequireBearer(otherHash)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// testToken は otherHash と一致しない → 401
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/protected", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP リクエスト失敗: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("ハッシュ不一致: got %d, want 401", resp.StatusCode)
	}
}

// Package middleware は HTTP ミドルウェアを提供する。
package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// RequireBearer は Bearer 認証ミドルウェアを返す。
// expected は secret.token の平文を sha256.Sum256 した 32 byte 固定長ダイジェスト (D-39)。
//
// 認証パース手順 (D-23 / §4.2.1):
//  1. r.Header.Values("Authorization") の len != 1 → 401
//  2. strings.CutPrefix で "Bearer " 前方一致確認、不一致 → 401
//  3. 受信平文を sha256.Sum256 → 32 byte ダイジェスト
//  4. subtle.ConstantTimeCompare(received[:], expected[:]) != 1 → 401
func RequireBearer(expected [32]byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			vs := r.Header.Values("Authorization")
			if len(vs) != 1 {
				writeUnauthorized(w)
				return
			}
			v, ok := strings.CutPrefix(vs[0], "Bearer ")
			if !ok {
				writeUnauthorized(w)
				return
			}
			received := sha256.Sum256([]byte(v))
			if subtle.ConstantTimeCompare(received[:], expected[:]) != 1 {
				writeUnauthorized(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"code":    "unauthorized",
			"message": "missing or invalid authorization token",
		},
	})
}

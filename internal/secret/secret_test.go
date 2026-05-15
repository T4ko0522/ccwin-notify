// internal/secret パッケージのユニットテスト (サイクル 1)
// テスト ID: T-006, T-007, T-008
// 受入条件: D5 / G4 / I4 — SecretString のログマスク / JSON マスク
package secret_test

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/t4ko0522/ccwin-notify/internal/secret"
)

// T-006: LogValue() が非空文字に対して "***" を返す
func TestSecretString_LogValue_Masked(t *testing.T) {
	t.Parallel()
	s := secret.SecretString("super-secret-token-abc123")
	val := s.LogValue()

	// slog.Value.String() は "***" を返すはず
	if val.String() != "***" {
		t.Errorf("LogValue().String(): got %q, want \"***\"", val.String())
	}
}

// T-006: LogValue() が空文字に対して "" を返す (長さリーク防止)
func TestSecretString_LogValue_Empty(t *testing.T) {
	t.Parallel()
	s := secret.SecretString("")
	val := s.LogValue()

	// 空文字の場合は "" を返す仕様 (3_contract.md §internal/secret より)
	if val.String() != "" {
		t.Errorf("空SecretStringのLogValue().String(): got %q, want \"\"", val.String())
	}
}

// T-007: Reveal() が実値を返す
func TestSecretString_Reveal(t *testing.T) {
	t.Parallel()
	original := "actual-secret-value-xyz"
	s := secret.SecretString(original)

	if s.Reveal() != original {
		t.Errorf("Reveal(): got %q, want %q", s.Reveal(), original)
	}
}

// slog を使ったとき SecretString が自動マスクされることを確認する統合テスト
func TestSecretString_SlogAutoMask(t *testing.T) {
	t.Parallel()
	// slog.LogValuer interface を実装していれば slog が自動的に LogValue() を呼ぶ
	var lv slog.LogValuer = secret.SecretString("test-secret")
	val := lv.LogValue()
	if val.String() != "***" {
		t.Errorf("slog.LogValuer経由: got %q, want \"***\"", val.String())
	}
}

// T-148 / D5: UnmarshalJSON が JSON 文字列を SecretString にデシリアライズする
func TestSecretString_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	type payload struct {
		Token secret.SecretString `json:"token"`
	}

	data := []byte(`{"token":"actual-value-xyz"}`)
	var p payload
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if p.Token.Reveal() != "actual-value-xyz" {
		t.Errorf("UnmarshalJSON: got %q, want \"actual-value-xyz\"", p.Token.Reveal())
	}
}

// T-008: MarshalJSON が常に "***" を返す (D5 / G4 / I4 / plan §3.4.2)
func TestSecretString_MarshalJSON_Masked(t *testing.T) {
	t.Parallel()

	type payload struct {
		Token secret.SecretString `json:"token"`
	}

	cases := []struct {
		name  string
		input secret.SecretString
	}{
		{"非空文字", "super-secret-token-abc123"},
		{"空文字", ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := json.Marshal(payload{Token: tc.input})
			if err != nil {
				t.Fatalf("json.Marshal error: %v", err)
			}
			got := string(b)
			const wantFragment = `"token":"***"`
			if !strings.Contains(got, wantFragment) {
				t.Errorf("json.Marshal: got %q, want fragment %q", got, wantFragment)
			}
		})
	}
}

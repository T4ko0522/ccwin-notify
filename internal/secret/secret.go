package secret

import (
	"encoding/json"
	"log/slog"
)

// SecretString は slog 出力時に "***" にマスクされる文字列型 (leaf パッケージ / D-19)。
// config と logging の双方から参照される。
type SecretString string

// Reveal は実際の値を取り出す。ログ出力以外の経路 (HTTP リクエスト送信等) でのみ使用する。
func (s SecretString) Reveal() string { return string(s) }

// LogValue は slog Handler が呼ぶ。
// 空でも実値でも "***" に統一し、長さ差で実値を推測されない (D5 / G4)。
// ただし空文字は "" を返す (空か否かの存在自体は隠さない)。
func (s SecretString) LogValue() slog.Value {
	if s == "" {
		return slog.StringValue("")
	}
	return slog.StringValue("***")
}

// MarshalJSON は json.Encoder に "***" を返す (D5 / G4 / I4)。
// /v1/status 等の JSON レスポンスにシークレット平文が漏洩しないよう保護する。
// 空文字でも実値でも常に "***" を出力する (長さ情報も隠す)。
func (s SecretString) MarshalJSON() ([]byte, error) {
	return []byte(`"***"`), nil
}

// UnmarshalJSON は JSON デシリアライズ時に呼ばれる。
// "***" または任意の文字列を SecretString にセットする (TOML / JSON 両対応)。
func (s *SecretString) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	*s = SecretString(str)
	return nil
}

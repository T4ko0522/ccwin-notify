// Package secret は機密文字列を安全に扱うための型を提供する。
//
// # SecretString
//
// [SecretString] は平文の文字列を保持しながら、slog / fmt による誤ったログ出力を防ぐ型。
//
//   - [SecretString.Reveal] で平文を取得する (意図的な参照のみ許可)。
//   - [SecretString.LogValue] は slog.LogValuer を実装し、空文字列のときは
//     空の slog.Value を、それ以外は "***" を返す。これにより slog ハンドラが
//     自動的に値をマスクするため、設定ファイル中の webhook URL やトークンが
//     ログに漏洩しない。
//
// # 使用例
//
//	s := secret.SecretString("https://hooks.slack.com/services/...")
//	raw := s.Reveal() // 平文取得
//	slog.Info("config loaded", "webhook_url", s) // ログには "***" と出力される
package secret

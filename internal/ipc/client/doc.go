// Package client は ccwin-notify デーモンへの接続に必要な補助機能を提供する。
//
// # portfile
//
// デーモンは起動時にランダムポートで HTTP サーバーを立て、
// 接続情報を %APPDATA%/ccwin-notify/daemon.port に JSON 形式で書き出す。
// クライアント (TUI / send CLI) は [ReadPortfile] でこのファイルを読み込み、
// デーモンの所在を特定する。
//
// JSON スキーマ ([PortfileContent]):
//
//	{
//	  "app":               "ccwin-notify",  // 必須。一致しない場合は ErrPortfileInvalid
//	  "version":           "0.1.0",         // バイナリバージョン
//	  "pid":               12345,           // デーモンの PID
//	  "port":              54321,           // HTTP サーバーのリッスンポート (> 0 必須)
//	  "started_at":        "2026-05-15T...", // RFC 3339 形式の起動日時
//	  "token_fingerprint": "abc123..."      // トークンの SHA-256 ハッシュ先頭 16 文字 (確認用)
//	}
//
// フォーマット不一致・app フィールド不一致・port <= 0 の場合は [ErrPortfileInvalid] が返る。
// ファイル不在の場合は [os.ErrNotExist] でラップされた error が返る。
//
// # HTTP クライアント
//
// [NewHTTPClient] は Bearer トークンを自動付与する [*http.Client] を返す。
// 返却したクライアントはすべてのリクエストに Authorization: Bearer <token> ヘッダーを
// 付与するため、呼び出し側は認証ヘッダーを個別に設定する必要がない。
package client

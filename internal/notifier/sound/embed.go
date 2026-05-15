package sound

import _ "embed"

// defaultWAV は WavPath 未指定時に再生される同梱の通知音。
//
// バイト列は internal/notifier/sound/assets/default.wav から取り込む。
// このファイルはリポジトリで管理しており、利用者は同パスに任意の WAV を
// 配置し直してから go build することで通知音を差し替えられる。
//
//go:embed assets/default.wav
var defaultWAV []byte

// DefaultWAV は同梱 WAV のバイト列を返す (テスト用)。
func DefaultWAV() []byte { return defaultWAV }

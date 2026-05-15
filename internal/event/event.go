package event

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// EventKind は内部で扱うイベント種別。Hooks のフック名と必ずしも 1:1 ではない。
type EventKind string

const (
	KindStop           EventKind = "Stop"
	KindNotification   EventKind = "Notification"
	KindSubagentStop   EventKind = "SubagentStop"
	KindIdle           EventKind = "Idle"           // プロセス監視由来
	KindProcessStarted EventKind = "ProcessStarted" // プロセス監視由来
	KindProcessStopped EventKind = "ProcessStopped" // プロセス監視由来
)

// ValidKinds は有効な EventKind の集合。CLI や IPC ハンドラの検証に使う。
var ValidKinds = map[EventKind]bool{
	KindStop:           true,
	KindNotification:   true,
	KindSubagentStop:   true,
	KindIdle:           true,
	KindProcessStarted: true,
	KindProcessStopped: true,
}

// Event は EventSource から発生し Notifier が消費する正規化済みイベント。
// ID は oklog/ulid/v2 で生成する (ソート可能 + 相関キー)。
// Notifier は Source フィールドを参照しなくても動作可能 (A4)。
type Event struct {
	ID        string          `json:"id"`
	Kind      EventKind       `json:"kind"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Source    string          `json:"source"`
	Timestamp time.Time       `json:"timestamp"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

// DropPolicy は bounded queue が溢れた際の挙動。
type DropPolicy string

const (
	DropOldest DropPolicy = "drop-oldest"
	DropNewest DropPolicy = "drop-newest"
	DropBlock  DropPolicy = "block"
)

// ValidDropPolicies は有効な DropPolicy の集合。
var ValidDropPolicies = map[DropPolicy]bool{
	DropOldest: true,
	DropNewest: true,
	DropBlock:  true,
}

// sentinel errors
var (
	// ErrDropped は DropNewest policy で capacity 超過時に新着が破棄された。
	ErrDropped = errors.New("event: dropped (queue full)")
	// ErrPublishCanceled は DropBlock policy で ctx 期限切れ / キャンセル時。
	ErrPublishCanceled = errors.New("event: publish canceled")
	// ErrBusClosed は Close 済みの Bus に Publish した。
	ErrBusClosed = errors.New("event: bus closed")
)

// ulidEntropy は crypto/rand ベースの monotonic entropy。全 ULID 生成で共有する (M3R-01 修正)。
var (
	ulidEntropy   = ulid.Monotonic(rand.Reader, 0)
	ulidEntropyMu sync.Mutex
)

// NewID は crypto/rand ベースの ULID 文字列を生成する。
// 同一マイクロ秒でも monotonic entropy により衝突しない (G2 / M3R-01)。
func NewID() string {
	ulidEntropyMu.Lock()
	defer ulidEntropyMu.Unlock()
	return ulid.MustNew(ulid.Now(), ulidEntropy).String()
}

// EventSink は Source から Bus への書き口だけを切り出した最小インターフェース。
// Source は drop policy / capacity を一切知らずに Publish できる (D-20)。
type EventSink interface {
	Publish(ctx context.Context, e Event) error
}

// Bus は EventSource → Dispatcher の集約点。EventSink を満たす。
type Bus interface {
	EventSink
	// Subscribe は Dispatcher が単一購読する想定 (single-consumer)。
	Subscribe() <-chan Event
	// Close は新規 Publish を遮断し、Subscribe チャネルを閉じる前に in-flight を drain する。
	// ctx 期限切れで強制クローズ。
	Close(ctx context.Context) error
}

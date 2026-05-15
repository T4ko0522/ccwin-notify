//go:build !windows

package daemon

import (
	"context"
	"fmt"
	"os"

	"github.com/t4ko0522/ccwin-notify/internal/config"
)

func pidSelf() int {
	return os.Getpid()
}

// Run は非 Windows では常にエラーを返す。
// daemon はメインの GOOS チェック (main.go) より後にはここに到達しない。
func Run(_ context.Context, _ *config.Config) error {
	return fmt.Errorf("daemon: not supported on this OS")
}

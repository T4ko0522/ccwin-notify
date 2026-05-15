package main

import "context"

// mainCtx は main goroutine のルート context を返す。
// 実際の signal 処理は daemon.Run 内で行う。
func mainCtx() context.Context {
	return context.Background()
}

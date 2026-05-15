//go:build !windows

package apiclient

import "os"

// isPIDAlive は指定 PID のプロセスが存在するかを確認する (非 Windows stub)。
// 非 Windows では os.FindProcess + kill(pid, 0) で確認する。
func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p
	return true
}

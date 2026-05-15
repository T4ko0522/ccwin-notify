//go:build windows

package apiclient

import "golang.org/x/sys/windows"

// isPIDAlive は指定 PID のプロセスが存在するかを確認する (Windows 専用)。
// OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION) + GetExitCodeProcess で判定する。
// GetExitCodeProcess が STILL_ACTIVE(259) を返す場合のみ生存とみなす。
func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid),
	)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}

	return exitCode == 259 // STILL_ACTIVE
}

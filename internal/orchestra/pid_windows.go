//go:build windows

package orchestra

import "syscall"

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
	errorAccessDenied              = syscall.Errno(5)
)

// pidAlive сообщает, жив ли процесс с данным PID.
func pidAlive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		// Нет доступа — процесс существует, но принадлежит другому пользователю.
		return err == errorAccessDenied
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}

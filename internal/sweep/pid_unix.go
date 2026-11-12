//go:build !windows

package sweep

import (
	"errors"
	"syscall"
)

// pidAlive сообщает, жив ли процесс с данным PID.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

//go:build !windows

package orchestra

import (
	"errors"
	"syscall"
)

// pidAlive сообщает, жив ли процесс с данным PID.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

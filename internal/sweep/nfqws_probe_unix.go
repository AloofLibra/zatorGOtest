//go:build unix

package sweep

import (
	"os"
	"os/exec"
	"syscall"
)

func applyProbeCredential(cmd *exec.Cmd, uid, gid uint32) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uid, Gid: gid}
	cmd.SysProcAttr.Setpgid = true
}

func killProcessGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	// Отрицательный PID — вся группа процессов.
	return syscall.Kill(-pid, syscall.SIGTERM)
}

func probeAlive(p *os.Process) error {
	if p == nil {
		return os.ErrProcessDone
	}
	return p.Signal(syscall.Signal(0))
}

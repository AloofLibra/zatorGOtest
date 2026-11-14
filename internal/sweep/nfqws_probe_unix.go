//go:build unix

package sweep

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func applyProbeCredential(cmd *exec.Cmd, uid, gid uint32) {
	// Не ставим Credential: NFQUEUE требует start от root, затем nfqws2
	// сам сбрасывает привилегии через --user= (см. buildArgs). Credential
	// до exec оставлял процесс без CAP_NET_ADMIN → мгновенный zombie на Keenetic.
	_ = uid
	_ = gid
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
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

// probeProcessRunning — true только для реально исполняемого процесса (не zombie).
func probeProcessRunning(p *os.Process) bool {
	if p == nil {
		return false
	}
	if err := probeAlive(p); err != nil {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", p.Pid))
	if err != nil {
		return false
	}
	// Формат: pid (comm) state ... — state сразу после последней ')'.
	s := string(data)
	idx := strings.LastIndex(s, ") ")
	if idx < 0 || idx+2 >= len(s) {
		return true
	}
	state := s[idx+2]
	return state != 'Z' && state != 'X'
}

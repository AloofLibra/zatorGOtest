package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/platform"
	"github.com/AloofLibra/zator/internal/service"
)

const (
	fwEnsureStateFile = "fw_ensure.txt"
	keeneticHookPath  = "/opt/etc/ndm/netfilter.d/000-zapret2.sh"
)

// FwHookPresent — установлен ли Keenetic/Merlin fw-хук или доступен init restart-fw.
func (a *App) FwHookPresent() bool {
	if _, err := os.Stat(keeneticHookPath); err == nil {
		return true
	}
	switch service.DetectHook(a.Platform) {
	case service.HookKeenetic, service.HookMerlin:
		return true
	}
	return a.resolveZapretInit() != ""
}

// ArgsPresent — есть ли непустой nfqws2.args.
func (a *App) ArgsPresent() bool {
	st, err := os.Stat(filepath.Join(a.Paths.ZapretRoot, "nfqws2.args"))
	return err == nil && st.Size() > 0
}

// FwEnsureStatus читает итог последнего EnsureFirewallApplied: "ok"|"skip"|"fail"|"".
func (a *App) FwEnsureStatus() (status, detail string) {
	b, err := os.ReadFile(filepath.Join(a.Paths.StateDir, fwEnsureStateFile))
	if err != nil {
		return "", ""
	}
	lines := splitTwo(string(b))
	return lines[0], lines[1]
}

func (a *App) writeFwEnsure(status, detail string) {
	_ = os.MkdirAll(a.Paths.StateDir, 0o755)
	_ = fsutil.AtomicWriteFile(filepath.Join(a.Paths.StateDir, fwEnsureStateFile),
		[]byte(status+"\n"+detail+"\n"), 0o644)
}

func (a *App) resolveZapretInit() string {
	cands := []string{}
	if a.Platform.InitSystem == platform.InitProcd {
		cands = append(cands, "/etc/init.d/zapret2")
	}
	cands = append(cands,
		filepath.Join(a.Paths.ZapretRoot, "init.d", "sysv", "zapret2"),
		"/opt/etc/init.d/S90-zapret2",
		"/etc/init.d/zapret2",
	)
	for _, p := range cands {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// EnsureFirewallApplied delegates firewall reconciliation to zapret2's native init.
func (a *App) EnsureFirewallApplied(ctx context.Context) error {
	if a.Platform.Kind == "" {
		a.writeFwEnsure("skip", "platform not detected")
		return nil
	}
	init := a.resolveZapretInit()
	if init == "" {
		err := fmt.Errorf("upstream zapret2 init not found")
		a.writeFwEnsure("fail", err.Error())
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, init, "restart-fw")
	out, err := cmd.CombinedOutput()
	detail := string(out)
	if len(detail) > 200 {
		detail = detail[:200]
	}
	if err != nil {
		msg := fmt.Sprintf("%s restart-fw: %v %s", init, err, detail)
		a.writeFwEnsure("fail", msg)
		return fmt.Errorf("fw ensure: %w", err)
	}
	a.writeFwEnsure("ok", init)
	return nil
}

func splitTwo(s string) [2]string {
	var out [2]string
	i := 0
	start := 0
	for n := 0; n < len(s) && i < 2; n++ {
		if s[n] == '\n' {
			out[i] = trimSpaceASCII(s[start:n])
			i++
			start = n + 1
		}
	}
	if i < 2 && start < len(s) {
		out[i] = trimSpaceASCII(s[start:])
	}
	return out
}

func trimSpaceASCII(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 {
		c := s[len(s)-1]
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

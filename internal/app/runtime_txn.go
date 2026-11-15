package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/service"
)

// RestartFunc перезапускает zapret2 (обёртка над service.Manager).
type RestartFunc func(ctx context.Context) error

// RestartZapretTransactional: snapshot config+args → Apply → restart →
// EnsureFirewall → проверка файлов → при fail откат snapshot и повторный restart.
func (a *App) RestartZapretTransactional(ctx context.Context, restart RestartFunc) error {
	if restart == nil {
		return fmt.Errorf("runtime: не задан restart")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfgPath := a.Paths.ZapretConfig
	argsPath := filepath.Join(a.Paths.ZapretRoot, "nfqws2.args")
	snapCFG, _ := os.ReadFile(cfgPath)
	snapArgs, _ := os.ReadFile(argsPath)

	if _, err := a.ApplyZapretRuntime(); err != nil {
		return fmt.Errorf("runtime apply: %w", err)
	}
	a.SetNfqwsWantRunning(true)
	if err := restart(ctx); err != nil {
		return fmt.Errorf("runtime restart: %w", err)
	}
	if err := a.ApplyWssizeFirewall(ctx); err != nil {
		_ = restoreSnap(cfgPath, snapCFG)
		_ = restoreSnap(argsPath, snapArgs)
		_ = restart(ctx)
		return fmt.Errorf("runtime WSSIZE: %w (выполнен откат config/args)", err)
	}
	_ = a.EnsureFirewallApplied(ctx)

	if err := a.verifyRuntimeFiles(); err != nil {
		_ = restoreSnap(cfgPath, snapCFG)
		_ = restoreSnap(argsPath, snapArgs)
		_ = restart(ctx)
		return fmt.Errorf("runtime verify: %w (выполнен откат config/args)", err)
	}
	return nil
}

// verifyRuntimeFiles — обязательные файлы после apply/restart.
// Наличие процесса nfqws2 — soft (в CI/fake-svc процесса нет).
func (a *App) verifyRuntimeFiles() error {
	if _, err := os.Stat(a.Paths.ZapretConfig); err != nil {
		return fmt.Errorf("нет zapret2 config")
	}
	args := filepath.Join(a.Paths.ZapretRoot, "nfqws2.args")
	st, err := os.Stat(args)
	if err != nil || st.Size() == 0 {
		return fmt.Errorf("нет или пуст nfqws2.args")
	}
	return nil
}

// VerifyRuntimeProcess — опциональная проверка живого nfqws2 (после реального start).
func (a *App) VerifyRuntimeProcess(wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		if service.IsNfqws2Running() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("nfqws2 не запущен")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func restoreSnap(path string, data []byte) error {
	if data == nil {
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return fsutil.AtomicWriteFile(path, data, 0o644)
}

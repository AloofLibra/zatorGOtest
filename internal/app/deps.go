package app

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/deps"
	"github.com/AloofLibra/zator/internal/platform"
)

// EnsureDependencies проверяет (и по возможности ставит) зависимости под
// текущую платформу. На Windows — no-op.
func (a *App) EnsureDependencies(ctx context.Context) (deps.Report, error) {
	opts := deps.Options{
		InstallPackages: runtime.GOOS != "windows",
		LoadModules:     runtime.GOOS != "windows",
	}
	rep := deps.Ensure(ctx, a.Platform, opts)
	if !rep.OK() {
		return rep, fmt.Errorf("зависимости не готовы: %s", strings.Join(rep.CriticalMissing, ", "))
	}
	return rep, nil
}

// ApplyDetectedIfaces проставляет IFACE_LAN/WAN по системе, если они не заданы
// или остались шаблонными. Возвращает итоговые значения.
// На OpenWrt IFACE_* не используются — no-op без ошибки.
func (a *App) ApplyDetectedIfaces() (lan, wan string, err error) {
	if a.Platform.Kind == platform.KindOpenWRT {
		return "", "", nil
	}
	cfgPath := a.Paths.ZapretConfig
	if cfgPath == "" {
		return "", "", fmt.Errorf("путь к config zapret2 не задан")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return "", "", err
	}
	changed, err := a.ensureNetworkIfaces(cfg)
	if err != nil {
		return "", "", err
	}
	if changed {
		if err := cfg.Save(); err != nil {
			return "", "", err
		}
	}
	lan, _ = cfg.Get(config.IfaceLAN)
	wan, _ = cfg.Get(config.IfaceWAN)
	if lan == "" || wan == "" {
		return lan, wan, fmt.Errorf("IFACE_LAN/WAN не заданы после автоопределения (LAN=%q WAN=%q)", lan, wan)
	}
	return lan, wan, nil
}

// WriteDepsReport печатает отчёт EnsureDependencies в w.
func WriteDepsReport(w io.Writer, rep deps.Report) {
	if w == nil {
		return
	}
	fmt.Fprintln(w, "Проверка зависимостей:")
	fmt.Fprintln(w, rep.Summary())
}

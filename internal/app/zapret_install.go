package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/platform"
	"github.com/AloofLibra/zator/internal/sysprofile"
	"github.com/AloofLibra/zator/internal/uiembed"
	"github.com/AloofLibra/zator/internal/zapret"
)

// InstallZapretOptions — параметры установки zapret2 поверх раскладки zator.
type InstallZapretOptions struct {
	Force       bool
	Version     string
	IncludeLua  bool
	SkipService bool
	Timeout     time.Duration
	// Log — куда писать ход проверки зависимостей и IFACE (обычно stdout).
	Log io.Writer
}

// InstallZapret скачивает последнюю (или указанную) сборку zapret2, раскладывает
// runtime (bin/common/init.d/lua), ставит config и поднимает службы zapret2+Web UI.
func (a *App) InstallZapret(ctx context.Context, opts InstallZapretOptions) (*zapret.Result, error) {
	if err := a.Paths.EnsureDirs(); err != nil {
		return nil, err
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	src, err := zapret.ParseSource(a.Config.ZapretSource)
	if err != nil {
		src = zapret.DefaultSource
	}
	force := opts.Force
	// Старые установки могли положить только binaries без init.d/common либо
	// повредить openrc/zapret2 на v0.1.43-test15 — переустанавливаем runtime.
	if !force && needsZapretReinstall(a.Platform, a.Paths.ZapretRoot) {
		force = true
	}
	zopts := zapret.Options{
		Source:     src,
		Version:    opts.Version,
		Force:      force,
		IncludeLua: true,
		MirrorBase: a.Config.Mirror,
	}
	log := opts.Log
	if log == nil {
		log = io.Discard
	}
	// Сначала системные зависимости (iptables/ipset/модули) — иначе firewall
	// после старта служб молча не поднимется.
	fmt.Fprintln(log, "проверка зависимостей под", a.Platform.String(), "...")
	depCtx, depCancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Minute)
	rep, depErr := a.EnsureDependencies(depCtx)
	depCancel()
	WriteDepsReport(log, rep)
	if depErr != nil {
		return nil, fmt.Errorf("зависимости: %w", depErr)
	}

	res, err := zapret.Install(cctx, a.Paths, a.Platform, zopts)
	if err != nil {
		return res, err
	}
	if data, err := uiembed.ConfigDefaultAt(a.Paths); err == nil {
		_ = os.WriteFile(filepath.Join(a.Paths.ZapretRoot, "config.default"), data, 0o644)
		if err := a.seedZapretConfig(data); err != nil {
			return res, err
		}
	}
	if err := a.syncAssets(); err != nil {
		return res, fmt.Errorf("ресурсы zator: %w", err)
	}
	lan, wan, ifaceErr := a.ApplyDetectedIfaces()
	if ifaceErr != nil {
		if a.isRouterHost() {
			return res, fmt.Errorf("IFACE_WAN/LAN: %w", ifaceErr)
		}
		fmt.Fprintf(log, "предупреждение IFACE: %v\n", ifaceErr)
	} else if lan != "" || wan != "" {
		fmt.Fprintf(log, "интерфейсы: IFACE_LAN=%s IFACE_WAN=%s\n", lan, wan)
	}

	if opts.SkipService {
		if _, err := a.PrepareZapretRuntime(); err != nil {
			return res, err
		}
		return res, nil
	}
	// Отдельный бюджет на службы: download мог съесть почти весь cctx.
	sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer scancel()
	if err := a.StartServices(sctx, BootstrapOptions{}); err != nil {
		return res, err
	}
	return res, nil
}

func needsZapretReinstall(plat platform.Platform, zapretRoot string) bool {
	prof := sysprofile.Resolve(plat, sysprofile.Options{})
	if !prof.ValidateUpstreamZapret(zapretRoot) {
		return true
	}
	return legacyZapretInit(filepath.Join(zapretRoot, "init.d", "sysv", "zapret2"))
}

func legacyZapretInit(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), "z2r: оверрайды апстримных")
}

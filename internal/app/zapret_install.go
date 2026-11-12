package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
}

// InstallZapret скачивает latest zapret2 (или указанную версию), готовит
// runtime и автоматически ставит/запускает службы zapret2 + Web UI.
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
	ver := opts.Version
	if ver == "" {
		ver = "latest"
	}
	zopts := zapret.Options{
		Source:     src,
		Version:    ver,
		Force:      opts.Force,
		IncludeLua: true, // upstream lua-desync (zapret-lib.lua и т.п.) нужен nfqws2
		MirrorBase: a.Config.Mirror,
	}
	res, err := zapret.Install(cctx, a.Paths, a.Platform, zopts)
	if err != nil {
		return res, err
	}
	if data, err := uiembed.ConfigDefault(); err == nil {
		if err := a.seedZapretConfig(data); err != nil {
			return res, err
		}
		_ = os.WriteFile(filepath.Join(a.Paths.ZapretRoot, "config.default"), data, 0o644)
	}
	if err := a.syncAssets(); err != nil {
		return res, fmt.Errorf("ресурсы zator: %w", err)
	}
	if opts.SkipService {
		if _, err := a.PrepareZapretRuntime(); err != nil {
			return res, err
		}
		return res, nil
	}
	if err := a.StartServices(cctx, BootstrapOptions{SkipService: false}); err != nil {
		return res, fmt.Errorf("автозапуск: %w", err)
	}
	return res, nil
}

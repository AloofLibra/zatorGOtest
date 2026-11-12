package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/AloofLibra/zator/internal/service"
	"github.com/AloofLibra/zator/internal/uiembed"
	"github.com/AloofLibra/zator/internal/zapret"
)

// InstallZapretOptions — параметры установки zapret2 поверх раскладки zator.
type InstallZapretOptions struct {
	// Force переустанавливает бинарники даже при совпадении версии.
	Force bool
	// Version — версия zapret2; пусто — DefaultVer.
	Version string
	// IncludeLua — распаковать upstream lua из архива zapret2.
	IncludeLua bool
	// SkipService — не ставить/запускать службу.
	SkipService bool
	// Timeout — таймаут загрузки (по умолчанию 5 минут).
	Timeout time.Duration
}

// InstallZapret скачивает бинарники zapret2 своим установщиком (без
// install_easy.sh), кладёт config.default при отсутствии config и ставит службу.
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
	zopts := zapret.Options{
		Source:     src,
		Version:    opts.Version,
		Force:      opts.Force,
		IncludeLua: opts.IncludeLua,
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
		// Также кладём копию шаблона рядом с config.
		_ = os.WriteFile(filepath.Join(a.Paths.ZapretRoot, "config.default"), data, 0o644)
	}
	if err := a.syncAssets(); err != nil {
		return res, fmt.Errorf("ресурсы zator: %w", err)
	}
	if opts.SkipService {
		return res, nil
	}
	mgr, err := service.New(service.Config{Paths: a.Paths, Platform: a.Platform})
	if err != nil {
		return res, fmt.Errorf("служба zapret2: %w", err)
	}
	if err := mgr.Install(cctx, service.UnitZapret2); err != nil {
		return res, fmt.Errorf("установка службы zapret2: %w", err)
	}
	if err := mgr.Restart(cctx, service.UnitZapret2); err != nil {
		// На Windows/без root restart может не сработать — не валим всю установку.
		return res, fmt.Errorf("zapret2 установлен, но запуск службы не удался: %w", err)
	}
	return res, nil
}

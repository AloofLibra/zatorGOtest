package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/googlevideo"
	"github.com/AloofLibra/zator/internal/netcheck"
	"github.com/AloofLibra/zator/internal/orchestra"
	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/sweep"
)

var (
	gvSeederMu sync.Mutex
	gvSeeder   *googlevideo.Seeder
	gvProbeMu  sync.Mutex // сериализация живых проб профиля 2
)

// GooglevideoSeedStatus — статус фонового автопробоя для Domains UI.
func (a *App) GooglevideoSeedStatus() googlevideo.SeedStatus {
	gvSeederMu.Lock()
	s := gvSeeder
	gvSeederMu.Unlock()
	if s == nil {
		return googlevideo.SeedStatus{State: "disabled"}
	}
	return s.Status()
}

// StartGooglevideoSeeder поднимает фоновый seed новых sn-* → auto_locked.
func (a *App) StartGooglevideoSeeder(ctx context.Context) {
	gvSeederMu.Lock()
	defer gvSeederMu.Unlock()
	if gvSeeder != nil {
		return
	}
	store := orchestra.NewStore(a.Paths.StateOrchestraDir())
	checker := netcheck.NewChecker(netcheck.DefaultOptions())
	env := &sweep.Env{
		Locks:   store,
		Checker: checker,
		Settle:  2300 * time.Millisecond,
	}
	probe := func(pctx context.Context, fqdn string, strategy int) (bool, string, error) {
		return a.probeGooglevideoStrategy(pctx, env, store, fqdn, strategy)
	}
	seeder := googlevideo.NewSeeder(googlevideo.SeederConfig{
		LivePath:  paths.LiveAutoLockedTSV(),
		StatePath: a.Paths.AutoLockedTSV,
		ListHosts: func() ([]string, error) {
			_, _ = a.SyncGooglevideoHosts()
			return a.GooglevideoHosts().List()
		},
		Probe:        probe,
		DefaultStrat: 41,
		MaxStrat:     44,
		TailMax:      8,
		Interval:     45 * time.Second,
		Cooldown:     2 * time.Second,
		OnSeeded: func(hostKey string, strategy int) {
			_ = hostKey
			_ = strategy
			a.EnsureGooglevideoMidstream(context.Background())
		},
	})
	gvSeeder = seeder
	seeder.Start(ctx)
}

// StopGooglevideoSeeder останавливает seed-воркер.
func (a *App) StopGooglevideoSeeder() {
	gvSeederMu.Lock()
	s := gvSeeder
	gvSeeder = nil
	gvSeederMu.Unlock()
	if s != nil {
		s.Stop()
	}
}

// probeGooglevideoStrategy — живая проба: временный лок профиля 2 → TLS check → restore.
// Изолированный NfqwsProbe тяжёл для фона; сериализуем через gvProbeMu.
func (a *App) probeGooglevideoStrategy(ctx context.Context, env *sweep.Env, store *orchestra.Store, fqdn string, strategy int) (bool, string, error) {
	if fqdn == "" || strategy < 1 {
		return false, "", fmt.Errorf("некорректная проба")
	}
	gvProbeMu.Lock()
	defer gvProbeMu.Unlock()

	const scope = orchestra.ScopeDefault
	prev, prevErr := store.GetLock(scope, "2", "tls")
	hadPrev := prevErr == nil && prev > 0 && prev != orchestra.StrategyAuto

	res, err := env.Try(ctx, sweep.TryRequest{
		Profile:  2,
		Strategy: strategy,
		Label:    "gv-seed",
		URL:      "https://" + fqdn + "/",
		TLSPref:  netcheck.PrefAny,
	})
	// Восстановить прежний лок (или снять временный).
	if hadPrev {
		_ = store.SetLock(scope, "2", "tls", prev)
	} else {
		_ = store.ClearLock(scope, "2", "tls")
	}
	if err != nil {
		return false, "", err
	}
	ok := res.Verdict == sweep.TokenOK || res.Verdict == sweep.TokenWarn
	return ok, res.Short, nil
}

// EnsureGooglevideoMidstream — sync hosts + ipv4 для WSSIZE mark/ipset + custom.d hook.
func (a *App) EnsureGooglevideoMidstream(ctx context.Context) {
	if a == nil {
		return
	}
	_, _ = a.SyncGooglevideoHosts()
	_, _ = a.RefreshGooglevideoIPv4(ctx, false)
	cfg, err := config.Load(a.Paths.ZapretConfig)
	if err != nil {
		return
	}
	// Не форсим ENABLE, если пользователь выключил — только sync ARGS/SKIP и hook.
	_, _ = cfg.SyncWssizeRampVars()
	cfg.StripWssizeShellIf()
	_ = cfg.Save()
	_ = a.SyncCustomDHooks(cfg)
}

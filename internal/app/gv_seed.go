package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/firewall"
	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/googlevideo"
	"github.com/AloofLibra/zator/internal/netcheck"
	"github.com/AloofLibra/zator/internal/orchestra"
	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/sweep"
)

var (
	gvSeederMu sync.Mutex
	gvSeeder   *googlevideo.Seeder
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
	seeder := googlevideo.NewSeeder(googlevideo.SeederConfig{
		LivePath:  paths.LiveAutoLockedTSV(),
		StatePath: a.Paths.AutoLockedTSV,
		ListHosts: func() ([]string, error) {
			_, _ = a.SyncGooglevideoHosts()
			return a.GooglevideoHosts().List()
		},
		ProbeBatch:    a.probeGooglevideoCandidates,
		DefaultStrat:  41,
		MaxStrat:      44,
		TailMax:       8,
		Interval:      45 * time.Second,
		Cooldown:      2 * time.Second,
		RetryInterval: time.Hour,
		OnSeeded: func(hostKey string, strategy int) {
			_ = hostKey
			_ = strategy
			a.touchAutoLockedReloadStamp()
			a.EnsureGooglevideoMidstream(context.Background())
		},
	})
	gvSeeder = seeder
	seeder.Start(ctx)
}

// probeGooglevideoCandidates запускает одну ограниченную изолированную сессию
// profile 2. Проба использует тот же nfqws2-конфиг/WSSIZE, что и SuperSweep,
// но собственные очередь, lock-файлы и временный рабочий каталог.
func (a *App) probeGooglevideoCandidates(ctx context.Context, fqdn string, candidates []int, onTry func(int)) (strategy int, detail string, retErr error) {
	if a == nil || len(candidates) == 0 {
		return 0, "", fmt.Errorf("googlevideo seed probe is not configured")
	}
	cfg, err := config.Load(a.Paths.ZapretConfig)
	if err != nil {
		return 0, "", fmt.Errorf("read zapret2 config: %w", err)
	}
	resolveCtx, resolveCancel := context.WithTimeout(ctx, 20*time.Second)
	media, err := netcheck.ResolveYouTubeMedia(resolveCtx, netcheck.DefaultYouTubeVideoID, netcheck.DefaultOptions())
	resolveCancel()
	if err != nil {
		return 0, "", fmt.Errorf("resolve YouTube media for Googlevideo probe: %w", err)
	}
	workDir, err := os.MkdirTemp("/tmp", "z2r-gv-seed-")
	if err != nil {
		return 0, "", fmt.Errorf("create isolated Googlevideo probe directory: %w", err)
	}
	probeStarted := false
	defer func() {
		if !probeStarted || retErr == nil {
			_ = os.RemoveAll(workDir)
		}
	}()
	if err := os.Chmod(workDir, 0o755); err != nil {
		return 0, "", fmt.Errorf("prepare isolated Googlevideo probe directory: %w", err)
	}
	orchestraDir := filepath.Join(workDir, "orchestra")
	probeStore := orchestra.NewStore(orchestraDir)
	probeStore.MaxStrategy = cfg.ProfileMaxStrategyOrDefault
	probe, err := sweep.NewNfqwsProbe(sweep.NfqwsProbeConfig{
		Paths:        a.Paths,
		WorkDir:      workDir,
		OrchestraDir: orchestraDir,
		Config:       cfg,
	})
	if err != nil {
		return 0, "", err
	}
	opts := netcheck.DefaultOptions()
	opts.WaitBoth = true
	opts.DialContext = netcheck.BindPortRangeDialer(firewall.SuperSweepPortFirst, firewall.SuperSweepPortLast)
	coord, err := sweep.New(sweep.Config{
		Dir:         filepath.Join(a.Paths.StateDir, "supersweep"),
		Store:       orchestra.NewStore(a.Paths.StateOrchestraDir()),
		ProbeStore:  probeStore,
		Probe:       probe,
		Checker:     netcheck.NewChecker(opts),
		MaxStrategy: cfg.ProfileMaxStrategyOrDefault,
	})
	if err != nil {
		return 0, "", err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	probeStarted = true
	return coord.ProbeGooglevideoCandidates(probeCtx, fqdn, candidates, media, onTry)
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

// touchAutoLockedReloadStamp сигналит Lua (slm_merge_auto_locked_from_disk), что TSV обновился.
func (a *App) touchAutoLockedReloadStamp() {
	_ = os.MkdirAll("/tmp", 0o755)
	_ = fsutil.AtomicWriteFile("/tmp/z2r-auto-locked.reload",
		[]byte(fmt.Sprintf("%d\n", time.Now().UnixNano())), 0o644)
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

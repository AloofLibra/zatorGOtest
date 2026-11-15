package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/firewall"
	"github.com/AloofLibra/zator/internal/googlevideo"
	"github.com/AloofLibra/zator/internal/telemetry"
)

var (
	gvMu         sync.Mutex
	gvListener   *googlevideo.Manager
	gvCollect    *googlevideo.Collector
	gvUploadStop context.CancelFunc
	gvUploadDone chan struct{}
)

// GooglevideoHosts возвращает обёртку над lists/googlevideo-hosts.txt.
func (a *App) GooglevideoHosts() *googlevideo.HostsFile {
	return googlevideo.NewHostsFile(a.Paths.GooglevideoHostsFile())
}

func (a *App) newGooglevideoCollector() *googlevideo.Collector {
	centralPath := filepath.Join(a.Paths.ListsDir, "googlevideo-ipv4.txt")
	var central googlevideo.CentralChecker
	cl := &googlevideo.CentralList{File: centralPath}
	if _, err := cl.Load(); err == nil {
		central = cl
	}
	_ = os.MkdirAll(a.Paths.GooglevideoStateDir(), 0o755)
	return googlevideo.NewCollector(googlevideo.CollectorConfig{
		Dir:       a.Paths.GooglevideoStateDir(),
		HostsFile: a.Paths.GooglevideoHostsFile(),
		Central:   central,
	})
}

// GooglevideoCollector возвращает (и при необходимости создаёт) локальный коллектор.
func (a *App) GooglevideoCollector() *googlevideo.Collector {
	gvMu.Lock()
	defer gvMu.Unlock()
	if gvCollect == nil {
		gvCollect = a.newGooglevideoCollector()
	}
	return gvCollect
}

// SyncGooglevideoHosts сливает SNI-лог и local.tsv в googlevideo-hosts.txt.
// При появлении новых хостов подтягивает ipv4 для WSSIZE.
func (a *App) SyncGooglevideoHosts() (added int, err error) {
	h := a.GooglevideoHosts()
	n1, err1 := h.MergeSNILog(a.Paths.GooglevideoSNILog())
	if err1 != nil {
		return 0, err1
	}
	n2, err2 := h.MergeLocalRows(a.GooglevideoCollector().Local())
	if err2 != nil {
		return n1, err2
	}
	added = n1 + n2
	if added > 0 {
		_, _ = a.RefreshGooglevideoIPv4(context.Background(), false)
	}
	return added, nil
}

// GooglevideoCollectionStatus — enabled|disabled для settings.
func (a *App) GooglevideoCollectionStatus() string {
	gvMu.Lock()
	running := gvListener != nil && gvListener.Running()
	gvMu.Unlock()
	if running {
		return "enabled"
	}
	mode := googlevideo.DetectMode(context.Background(), googlevideo.DetectOptions{
		SnifferPath: filepath.Join(a.Paths.ZatorRoot, googlevideo.DefaultSnifferRelPath),
	})
	if mode == googlevideo.ModeOff {
		return "disabled"
	}
	return "enabled"
}

// StartGooglevideoCollection поднимает DNS-listener (если доступен) и
// один раз сливает уже накопленные host в файл. Также стартует seed-воркер
// и mid-stream sync (ipv4/WSSIZE).
func (a *App) StartGooglevideoCollection(ctx context.Context) error {
	_, _ = a.SyncGooglevideoHosts()
	a.EnsureGooglevideoMidstream(ctx)
	a.StartGooglevideoSeeder(ctx)

	gvMu.Lock()
	defer gvMu.Unlock()
	if gvListener != nil && gvListener.Running() {
		return nil
	}
	if gvCollect == nil {
		gvCollect = a.newGooglevideoCollector()
	}
	mode := googlevideo.DetectMode(ctx, googlevideo.DetectOptions{
		SnifferPath: filepath.Join(a.Paths.ZatorRoot, googlevideo.DefaultSnifferRelPath),
	})
	if mode == googlevideo.ModeOff {
		return nil
	}
	mgr, err := googlevideo.NewManager(googlevideo.ManagerConfig{
		Mode:        mode,
		SnifferPath: filepath.Join(a.Paths.ZatorRoot, googlevideo.DefaultSnifferRelPath),
		Observer:    gvCollect,
		OnObserved:  a.addGooglevideoIPToKernelSet,
	})
	if err != nil {
		return err
	}
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("googlevideo listener: %w", err)
	}
	gvListener = mgr
	a.startGooglevideoUploader(ctx, gvCollect)
	return nil
}

func (a *App) addGooglevideoIPToKernelSet(obs googlevideo.Observation, result googlevideo.ObserveResult) error {
	if result.Central {
		return nil
	}
	cfg, err := config.Load(a.Paths.ZapretConfig)
	if err != nil {
		return err
	}
	wcfg, err := firewall.WssizeConfigFromGetter(cfg.Get, a.Paths.ZatorRoot)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return firewall.NewWssize(wcfg, firewall.ExecRunner{}).AddIPs(ctx, []string{obs.IP})
}

// StopGooglevideoCollection останавливает listener (для тестов/shutdown).
func (a *App) StopGooglevideoCollection() {
	a.StopGooglevideoSeeder()
	gvMu.Lock()
	stopUpload, uploadDone := gvUploadStop, gvUploadDone
	gvUploadStop, gvUploadDone = nil, nil
	defer gvMu.Unlock()
	if stopUpload != nil {
		stopUpload()
	}
	if gvListener != nil {
		_ = gvListener.Stop()
		gvListener = nil
	}
	if uploadDone != nil {
		<-uploadDone
	}
}

func (a *App) startGooglevideoUploader(parent context.Context, collector *googlevideo.Collector) {
	if gvUploadStop != nil || collector == nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	client := telemetry.New(a.Paths)
	uploader, err := googlevideo.NewUploader(googlevideo.UploaderConfig{
		Collector: collector,
		Central:   a.GooglevideoCentralList(),
		IngestURL: googlevideo.IngestURLFromEnv(),
		Consent: func() googlevideo.Consent {
			settings, err := client.LoadSettings()
			if err != nil || !settings.Enabled || strings.TrimSpace(settings.UUID) == "" {
				return googlevideo.Consent{}
			}
			return googlevideo.Consent{Enabled: true, UUID: settings.UUID, Token: os.Getenv(telemetry.EnvStatsToken)}
		},
	})
	if err != nil {
		cancel()
		return
	}
	done := make(chan struct{})
	gvUploadStop, gvUploadDone = cancel, done
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := uploader.Upload(ctx, false); err != nil {
					// Очередь остаётся на диске и будет повторена в следующий цикл.
					continue
				}
			}
		}
	}()
}

// GooglevideoCentralList — кеш lists/googlevideo-ipv4.txt для WSSIZE mark/ipset.
// По умолчанию только локальный файл (из assets); удалённый feed — через
// Z2R_GOOGLEVIDEO_IPS_URL.
func (a *App) GooglevideoCentralList() *googlevideo.CentralList {
	return &googlevideo.CentralList{
		File: filepath.Join(a.Paths.ListsDir, googlevideo.ListFileName),
		URL:  strings.TrimSpace(os.Getenv(googlevideo.EnvListURL)),
	}
}

// RefreshGooglevideoIPv4 обновляет центральный ipv4-список (TTL или force).
// Нужен для mid-stream: WSSIZE lite маркирует только адреса из этого файла/ipset.
func (a *App) RefreshGooglevideoIPv4(ctx context.Context, force bool) (googlevideo.UpdateResult, error) {
	if err := os.MkdirAll(a.Paths.ListsDir, 0o755); err != nil {
		return googlevideo.UpdateResult{}, err
	}
	cl := a.GooglevideoCentralList()
	// Подмешиваем локально наблюдаемые IP (коллектор) в OnUpdate не нужно —
	// они живут отдельно; central list — публичный CIDR/IP feed.
	return cl.Update(ctx, force)
}

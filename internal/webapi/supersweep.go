package webapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator/internal/firewall"
	"github.com/AloofLibra/zator/internal/netcheck"
	"github.com/AloofLibra/zator/internal/orchestra"
	"github.com/AloofLibra/zator/internal/sweep"
)

// sweepDir — каталог прогресса суперавтопрогона.
func (s *Server) sweepDir() string { return filepath.Join(os.TempDir(), "zator", "supersweep") }

// probeDir — рабочий каталог изолированного nfqws2.
func (s *Server) probeDir() string { return filepath.Join(os.TempDir(), "zator", "supersweep-probe") }

// sweepRunningLocked — true, если sweeper уже создан и Running (без ленивого New).
// Вызывать под runtimeMu или без — берёт sweepMu сам.
func (s *Server) sweepRunningLocked() bool {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	return s.sweeper != nil && s.sweeper.Running()
}

// sweeperOrDefault возвращает Sweeper; по умолчанию лениво создаёт
// *sweep.Coordinator с изолированным NfqwsProbe и приватным ProbeStore.
func (s *Server) sweeperOrDefault() (Sweeper, error) {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	if s.sweeper != nil {
		return s.sweeper, nil
	}

	cfgLive, installed := s.readConfig()
	if !installed {
		return nil, s.notInstalledErr()
	}

	probeStore := orchestra.NewStore(filepath.Join(s.probeDir(), "orchestra"))
	probeStore.MaxStrategy = s.maxStrategy

	probe, err := sweep.NewNfqwsProbe(sweep.NfqwsProbeConfig{
		Paths:        s.paths,
		WorkDir:      s.probeDir(),
		OrchestraDir: probeStore.Dir(),
		Config:       cfgLive,
	})
	if err != nil {
		return nil, err
	}

	opts := netcheck.DefaultOptions()
	opts.DialContext = netcheck.BindPortRangeDialer(
		firewall.SuperSweepPortFirst, firewall.SuperSweepPortLast)
	checker := netcheck.NewChecker(opts)
	checker.MediaResolver = func(ctx context.Context, videoID string) (netcheck.YouTubeMedia, error) {
		// Innertube runs outside the test port range; only media GETs enter probe.
		return netcheck.ResolveYouTubeMedia(ctx, videoID, netcheck.DefaultOptions())
	}
	if base, ok := s.checker.(*netcheck.Checker); ok {
		checker.RedirectorURL = base.RedirectorURL
		checker.DNS = base.DNS
	}

	cfg := sweep.Config{
		Dir:        s.sweepDir(),
		ArchiveDir: filepath.Join(os.TempDir(), "zator"),
		Store:      s.store,
		ProbeStore: probeStore,
		Probe:      probe,
		Checker:    checker,
		ResolveMedia: func(ctx context.Context) (netcheck.YouTubeMedia, error) {
			return checker.MediaResolver(ctx, netcheck.DefaultYouTubeVideoID)
		},
		MaxStrategy: s.maxStrategy,
		GVURL:       func(ctx context.Context) string { return "https://" + checker.ClusterDomain(ctx) },
		OnComplete: func(rep sweep.Report, err error) {
			if err == nil && rep.State == sweep.StateDone && len(rep.Applied) > 0 {
				s.notifyTelemetry()
			}
		},
	}
	coord, err := sweep.New(cfg)
	if err != nil {
		return nil, err
	}
	s.sweeper = coord
	return coord, nil
}

// writeSupersweep — GET settings.cgi?setting=supersweep (read-only).
func (s *Server) writeSupersweep(w http.ResponseWriter) {
	sw, err := s.sweeperOrDefault()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Суперавтопрогон недоступен: "+err.Error())
		return
	}
	rep, err := sw.Report()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Не удалось прочитать отчёт: "+err.Error())
		return
	}
	data, err := rep.JSON()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(data, '\n'))
}

// postSupersweep — POST setting=supersweep&value=start|status|cancel.
func (s *Server) postSupersweep(_ *http.Request, p params) (map[string]any, error) {
	action := strings.ToLower(p.Get("value"))
	if action == "" {
		action = strings.ToLower(p.Get("action"))
	}
	// status — read-only, без runtimeMu (не блокировать Overview).
	if action == "status" {
		sw, err := s.sweeperOrDefault()
		if err != nil {
			return nil, errf(http.StatusServiceUnavailable, "Суперавтопрогон недоступен: "+err.Error())
		}
		rep, err := sw.Report()
		if err != nil {
			return nil, errf(http.StatusInternalServerError, "Не удалось прочитать отчёт: "+err.Error())
		}
		return map[string]any{"ok": true, "report": rep, "running": sw.Running()}, nil
	}

	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()

	sw, err := s.sweeperOrDefault()
	if err != nil {
		return nil, errf(http.StatusServiceUnavailable, "Суперавтопрогон недоступен: "+err.Error())
	}
	switch action {
	case "start":
		scope, err := scopeParam(p)
		if err != nil {
			return nil, err
		}
		sp := sweep.DefaultParams()
		sp.Scope = scope
		if raw := p.Get("quick_profile"); raw != "" {
			profile, convErr := strconv.Atoi(raw)
			if convErr != nil || (profile != 1 && profile != 2 && profile != 3 && profile != 4) {
				return nil, errf(http.StatusBadRequest, "Некорректный профиль быстрого подбора")
			}
			sp.QuickProfile = profile
			sp.QuickAddress = p.Raw("address")
		}
		if v := p.Get("tls_pref"); v != "" {
			sp.TLSPref = netcheck.ParseTLSPref(v)
		}
		if raw := p.Raw("domain"); strings.TrimSpace(raw) != "" {
			sp.Domains = strings.FieldsFunc(raw, func(r rune) bool {
				return r == '\n' || r == '\r' || r == ',' || r == ';' || r == ' ' || r == '\t'
			})
		}
		// Прогон живёт дольше HTTP-запроса — контекст запроса не используем.
		if err := sw.Start(context.Background(), sp); err != nil {
			switch {
			case errors.Is(err, sweep.ErrBusy):
				return nil, errf(http.StatusConflict, "Суперавтопрогон уже выполняется")
			case errors.Is(err, sweep.ErrNoDomains):
				return nil, errf(http.StatusBadRequest, "Нет корректных доменов РКН")
			case errors.Is(err, sweep.ErrDNSSpoofed):
				return nil, errf(http.StatusConflict, "Системный DNS подменён или сломан: прогон не начат")
			case errors.Is(err, orchestra.ErrInvalid):
				return nil, errf(http.StatusBadRequest, err.Error())
			}
			return nil, errf(http.StatusInternalServerError, "Не удалось запустить суперавтопрогон: "+err.Error())
		}
		return map[string]any{"ok": true, "started": true}, nil
	case "cancel":
		if err := sw.Cancel(); err != nil {
			if errors.Is(err, sweep.ErrNotRunning) {
				return nil, errf(http.StatusConflict, "Суперавтопрогон не запущен")
			}
			return nil, errf(http.StatusInternalServerError, "Не удалось отменить суперавтопрогон: "+err.Error())
		}
		return map[string]any{"ok": true, "cancelled": true}, nil
	}
	return nil, errf(http.StatusBadRequest, "Некорректное действие: "+action)
}

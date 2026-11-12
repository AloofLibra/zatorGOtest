package webapi

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/AloofLibra/zator/internal/netcheck"
	"github.com/AloofLibra/zator/internal/orchestra"
	"github.com/AloofLibra/zator/internal/sweep"
)

// sweepDir — каталог прогресса суперавтопрогона.
func (s *Server) sweepDir() string { return filepath.Join(s.paths.StateDir, "supersweep") }

// sweeperOrDefault возвращает Sweeper; по умолчанию лениво создаёт
// *sweep.Coordinator поверх хранилища orchestra и сетевого Checker.
// Изолированный nfqws2 (Probe) не подключается: используется NoopProbe.
func (s *Server) sweeperOrDefault() (Sweeper, error) {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	if s.sweeper != nil {
		return s.sweeper, nil
	}
	cfg := sweep.Config{
		Dir:         s.sweepDir(),
		ArchiveDir:  s.paths.StateDir,
		Store:       s.store,
		Checker:     s.checker,
		MaxStrategy: s.maxStrategy,
	}
	if c, ok := s.checker.(*netcheck.Checker); ok {
		cfg.GVURL = func(ctx context.Context) string { return "https://" + c.ClusterDomain(ctx) }
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
	case "status":
		rep, err := sw.Report()
		if err != nil {
			return nil, errf(http.StatusInternalServerError, "Не удалось прочитать отчёт: "+err.Error())
		}
		return map[string]any{"ok": true, "report": rep, "running": sw.Running()}, nil
	}
	return nil, errf(http.StatusBadRequest, "Некорректное действие: "+action)
}

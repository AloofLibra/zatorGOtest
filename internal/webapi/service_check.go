package webapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/netcheck"
	"github.com/AloofLibra/zator/internal/service"
)

// luaLockCacheTTL — запас над cache_ttl=2 в assets/lua/locked.lua.
const luaLockCacheTTL = 2300 * time.Millisecond

// POST /cgi-bin/service.cgi — start|stop|restart службы zapret2.
func (s *Server) handleService(w http.ResponseWriter, r *http.Request, p params) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	action := p.Get("action")
	if action != "start" && action != "stop" && action != "restart" {
		writeError(w, http.StatusBadRequest, "Некорректное действие")
		return
	}
	if s.svc == nil {
		msg := "Не удалось выполнить команду zapret2"
		detail := "служба недоступна"
		if s.svcErr != nil {
			detail = s.svcErr.Error()
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg, "detail": detail})
		return
	}

	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()

	if action != "stop" && s.sweepRunningLocked() {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":  "Служба занята суперавтопрогоном",
			"detail": "дождитесь окончания или отмените supersweep",
		})
		return
	}

	var err error
	switch action {
	case "start":
		if s.app != nil {
			if _, e := s.app.ApplyZapretRuntime(); e != nil {
				s.app.WriteServiceWarnings([]string{"runtime apply: " + e.Error()})
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"error": "Не удалось применить конфиг zapret2", "detail": e.Error(),
					"runtime_apply_error": e.Error(),
				})
				return
			}
			s.app.SetNfqwsWantRunning(true)
		}
		err = s.svc.Start(r.Context(), service.UnitZapret2)
		if err == nil && s.app != nil {
			var warns []string
			if e := s.app.EnsureFirewallApplied(r.Context()); e != nil {
				warns = append(warns, "fw ensure: "+e.Error())
			}
			s.app.StartNfqwsWatchdog()
			s.app.StartWebUIWatchdog()
			if len(warns) > 0 {
				s.app.WriteServiceWarnings(warns)
			} else {
				s.app.ClearServiceWarnings()
			}
		}
	case "stop":
		if s.app != nil {
			s.app.PersistAutoLocked()
			s.app.SetNfqwsWantRunning(false)
			s.app.StopNfqwsWatchdog()
		}
		err = s.svc.Stop(r.Context(), service.UnitZapret2)
	default:
		if s.app != nil {
			err = s.app.RestartZapretTransactional(r.Context(), func(ctx context.Context) error {
				return s.svc.Restart(ctx, service.UnitZapret2)
			})
			if err != nil {
				s.app.WriteServiceWarnings([]string{err.Error()})
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"error": "Не удалось выполнить команду zapret2", "detail": err.Error(),
				})
				return
			}
			var warns []string
			if st, d := s.app.FwEnsureStatus(); st == "fail" && d != "" {
				warns = append(warns, "fw ensure: "+d)
			}
			s.app.StartNfqwsWatchdog()
			s.app.StartWebUIWatchdog()
			if len(warns) > 0 {
				s.app.WriteServiceWarnings(warns)
			} else {
				s.app.ClearServiceWarnings()
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		err = s.svc.Restart(r.Context(), service.UnitZapret2)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Не удалось выполнить команду zapret2", "detail": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// checkCtx возвращает контекст проверки с таймаутом.
func (s *Server) checkCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), s.timeout)
}

// GET|POST /cgi-bin/check.cgi — полная проверка либо одного профиля (?profile=N).
// Для profile: возвращает lock/scope/zapret2_running; wait_apply=1 ждёт TTL Lua.
func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request, p params) {
	if !requireMethod(w, r, http.MethodGet, http.MethodPost) {
		return
	}
	scope, err := scopeParam(p)
	if err != nil {
		writeErr(w, err)
		return
	}
	ctx, cancel := s.checkCtx(r)
	defer cancel()
	profile := p.Get("profile")
	var resp netcheck.CheckResponse
	if profile == "" {
		resp = s.checker.Check(ctx)
	} else {
		if !profileRe.MatchString(profile) {
			writeError(w, http.StatusBadRequest, "Некорректный профиль")
			return
		}
		id, _ := strconv.Atoi(profile)
		if !config.ValidProfile(id) {
			writeError(w, http.StatusBadRequest, "Некорректный профиль")
			return
		}
		waitApply := p.Get("wait_apply") == "1" || strings.EqualFold(p.Get("wait_apply"), "true")
		if waitApply {
			timer := time.NewTimer(luaLockCacheTTL)
			select {
			case <-ctx.Done():
				timer.Stop()
				writeError(w, http.StatusGatewayTimeout, "Таймаут ожидания применения лока")
				return
			case <-timer.C:
			}
			resp.WaitedApply = true
		}
		opts := netcheck.ProfileCheckOptions{}
		if id == 2 {
			opts.MediaURL = strings.TrimSpace(p.Get("media_url"))
			opts.VideoID = strings.TrimSpace(p.Get("video_id"))
		}
		checkResp := s.checker.CheckProfileOpts(ctx, id, opts)
		checkResp.WaitedApply = resp.WaitedApply
		resp = checkResp
		s.annotateProfileCheck(&resp, scope, id, p.Get("expect_lock"))
		s.annotateMediaHints(&resp)
	}
	if resp.Results == nil {
		resp.Results = []netcheck.TargetResult{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// annotateProfileCheck добавляет факты о локе и состоянии zapret2.
func (s *Server) annotateProfileCheck(resp *netcheck.CheckResponse, scope string, profile int, expectLock string) {
	resp.Profile = profile
	resp.Scope = scope
	resp.Lock = s.profileLockLabel(scope, profile)
	running := service.IsNfqws2Running()
	resp.Zapret2Running = &running

	var bits []string
	if resp.Message != "" {
		bits = append(bits, resp.Message)
	}
	if !running {
		bits = append(bits, "zapret2 не запущен — TLS-тест идёт без обхода DPI")
	}
	switch resp.Lock {
	case "auto":
		bits = append(bits, fmt.Sprintf("лок профиля %d: auto (проверяется текущий авто-режим)", profile))
	case "0":
		bits = append(bits, fmt.Sprintf("лок профиля %d: выключен (0)", profile))
	default:
		bits = append(bits, fmt.Sprintf("лок профиля %d: стратегия %s (scope=%s)", profile, resp.Lock, scope))
	}
	if expect := strings.TrimSpace(expectLock); expect != "" {
		ok := resp.Lock == expect
		resp.Applied = &ok
		if ok {
			bits = append(bits, "лок в store совпал с ожидаемым "+expect)
		} else {
			bits = append(bits, fmt.Sprintf("лок в store %s ≠ ожидаемый %s", resp.Lock, expect))
		}
	}
	if resp.Retried {
		bits = append(bits, "TLS-проба с повтором")
	}
	resp.Message = strings.Join(bits, ". ")
}

func (s *Server) profileLockLabel(scope string, profile int) string {
	if s.store == nil {
		return "auto"
	}
	id := strconv.Itoa(profile)
	for _, proto := range config.ProfileProtos(profile) {
		st, err := s.store.GetLock(scope, id, proto)
		if err != nil || st.IsAuto() {
			continue
		}
		return st.String()
	}
	return "auto"
}

// annotateMediaHints добавляет подсказки WSSIZE/ipv4 при mid-stream media-классе.
func (s *Server) annotateMediaHints(resp *netcheck.CheckResponse) {
	if resp == nil || s.app == nil {
		return
	}
	for _, r := range resp.Results {
		if r.Media == nil {
			continue
		}
		switch r.Media.Class {
		case netcheck.MediaMidstream:
			ips := 0
			if cl := s.app.GooglevideoCentralList(); cl != nil {
				ips = cl.Count()
			}
			hint := fmt.Sprintf("WSSIZE: googlevideo-ipv4=%d адресов", ips)
			if ips == 0 {
				hint += " (список пуст — Domains→googlevideo refresh_ips или Apply)"
			}
			if resp.Message != "" {
				resp.Message += ". " + hint
			} else {
				resp.Message = hint
			}
			return
		}
	}
}

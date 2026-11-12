package webapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/AloofLibra/zator/internal/netcheck"
	"github.com/AloofLibra/zator/internal/service"
)

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
	var err error
	switch action {
	case "start":
		err = s.svc.Start(r.Context(), service.UnitZapret2)
	case "stop":
		err = s.svc.Stop(r.Context(), service.UnitZapret2)
	default:
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
func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request, p params) {
	if !requireMethod(w, r, http.MethodGet, http.MethodPost) {
		return
	}
	if _, err := scopeParam(p); err != nil {
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
		resp = s.checker.CheckProfile(ctx, id)
	}
	if resp.Results == nil {
		resp.Results = []netcheck.TargetResult{}
	}
	writeJSON(w, http.StatusOK, resp)
}

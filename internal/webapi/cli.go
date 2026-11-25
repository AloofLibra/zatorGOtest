package webapi

// This file exposes the existing Web UI business operations to the local CLI.
// Both entry points use the same validators, locks and config edit paths; the
// CLI calls these Go methods directly and never starts or calls an HTTP server.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/orchestra"
)

// CLISetting reads one supported setting using the same getter as settings.cgi.
func (s *Server) CLISetting(ctx context.Context, name string) (any, error) {
	cfg, _ := s.readConfig()
	return s.settingGet(ctx, cfg, name)
}

// CLISetSetting applies a setting through the existing settings operation.
func (s *Server) CLISetSetting(ctx context.Context, name string, values map[string]string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	form := make(url.Values, len(values)+1)
	form.Set("setting", name)
	for k, v := range values {
		form.Set(k, v)
	}
	r := (&http.Request{}).WithContext(ctx)
	return s.settingsPost(r, params(form))
}

// CLIDomains invokes the same domain list and mutation logic used by domains.cgi.
func (s *Server) CLIDomains(ctx context.Context, name, scope, action string, values map[string]string) (map[string]any, error) {
	d, ok := domainLists[name]
	if !ok {
		return nil, fmt.Errorf("неизвестный список %q", name)
	}
	if scope == "" {
		scope = "default"
	}
	form := make(url.Values, len(values)+3)
	form.Set("list", name)
	form.Set("scope", scope)
	form.Set("action", action)
	for k, v := range values {
		form.Set(k, v)
	}
	scope, err := scopeParam(params(form))
	if err != nil {
		return nil, err
	}
	s.listMu.Lock()
	defer s.listMu.Unlock()
	if action == "list" || action == "" {
		return s.domainsList(d, scope)
	}
	return s.domainsAction((&http.Request{}).WithContext(ctx), d, params(form), scope)
}

// CLISupersweep uses the same coordinator and guards as the settings endpoint.
func (s *Server) CLISupersweep(ctx context.Context, action string, values map[string]string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	form := make(url.Values, len(values)+2)
	form.Set("setting", "supersweep")
	form.Set("value", action)
	for k, v := range values {
		form.Set(k, v)
	}
	return s.supersweepAction(params(form))
}

// CLISetProfileLock preserves profile guards, strategy range validation and
// multi-protocol rollback implemented by the UI path.
func (s *Server) CLISetProfileLock(ctx context.Context, scope string, profile int, strategy *int) (map[string]any, error) {
	if scope == "" {
		scope = "default"
	}
	validatedScope, err := scopeParam(params(url.Values{"scope": {scope}}))
	if err != nil {
		return nil, err
	}
	scope = validatedScope
	cfg, _ := s.readConfig()
	id := profile
	st := orchestra.StrategyAuto
	if strategy != nil {
		max := cfg.ProfileMaxStrategyOrDefault(id)
		if *strategy < 0 || (*strategy != 0 && *strategy > max) {
			return nil, errf(http.StatusBadRequest, "Стратегия вне диапазона")
		}
		st = orchestra.Strategy(*strategy)
	}
	if err := autoConflict(cfg, id); err != nil {
		return nil, err
	}
	if err := profileModeGuard(cfg, id); err != nil {
		return nil, err
	}
	if len(config.ProfileProtos(id)) == 0 {
		return nil, errf(http.StatusBadRequest, "Не удалось определить протокол профиля")
	}
	prev := s.profileLockLabel(scope, id)
	if err := s.applyProfileLock(scope, id, st); err != nil {
		return nil, err
	}
	lock := st.String()
	out := map[string]any{"ok": true, "previous": prev, "lock": lock, "changed": prev != lock, "scope": scope, "profile": id}
	if id == 6 {
		if warning := s.voicePorts(ctx, strategy == nil || *strategy != 0); warning != "" {
			out["warning"] = warning
		}
	}
	if prev != lock {
		s.notifyTelemetry()
	}
	return out, nil
}

// CLIProfileLockLabel reports the effective strategy and its lock source for a scope.
func (s *Server) CLIProfileLockLabel(scope string, profile int) (string, string, error) {
	if !config.ValidProfile(profile) {
		return "", "", fmt.Errorf("некорректный профиль")
	}
	if scope == "" {
		scope = orchestra.ScopeDefault
	}
	validatedScope, err := scopeParam(params(url.Values{"scope": {scope}}))
	if err != nil {
		return "", "", err
	}
	lock, source := s.effectiveLock(validatedScope, profile)
	return lock, string(source), nil
}

// CLIProfileCheck performs the selected profile's ordinary reachability check.
func (s *Server) CLIProfileCheck(ctx context.Context, profile int) (any, error) {
	if !config.ValidProfile(profile) {
		return nil, fmt.Errorf("некорректный профиль")
	}
	cctx, cancel := s.checkCtx((&http.Request{}).WithContext(ctx))
	defer cancel()
	return s.checker.CheckProfile(cctx, profile), nil
}

// CLIReachability runs the ordinary diagnostics check on explicit request.
func (s *Server) CLIReachability(ctx context.Context) (any, error) {
	return s.checker.Check(ctx), nil
}

// CLIStartQuickSweep is a convenience helper for validated quick-search params.
func (s *Server) CLIStartQuickSweep(ctx context.Context, profile int, address, scope, tlsPref string) (map[string]any, error) {
	values := map[string]string{"quick_profile": fmt.Sprint(profile), "address": address, "scope": scope, "tls_pref": tlsPref}
	return s.CLISupersweep(ctx, "start", values)
}

// CLICustom invokes GET/POST /api/custom directly for CLI and interactive menus.
func (s *Server) CLICustom(ctx context.Context, action string, values map[string]string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if action == "" || action == "list" {
		return s.customListPayload(values["block"])
	}
	form := make(url.Values, len(values)+1)
	form.Set("action", action)
	for k, v := range values {
		form.Set(k, v)
	}
	return s.customPost((&http.Request{}).WithContext(ctx), params(form))
}

// CLIBlobs invokes GET/POST /api/blobs directly for CLI and interactive menus.
func (s *Server) CLIBlobs(ctx context.Context, action string, values map[string]string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if action == "" || action == "list" {
		return s.blobsListPayload()
	}
	form := make(url.Values, len(values)+1)
	form.Set("action", action)
	for k, v := range values {
		form.Set(k, v)
	}
	return s.blobsAction((&http.Request{}).WithContext(ctx), params(form))
}

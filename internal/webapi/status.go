package webapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator"
	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/firewall"
	"github.com/AloofLibra/zator/internal/orchestra"
	"github.com/AloofLibra/zator/internal/zapret"
)

const (
	providerFile    = "provider.txt"
	providerUnknown = "Не определён"
	autoRotationMsg = "управляется авторотацией TCP/HTTP. Сначала выключите авторотацию."
)

var (
	profileRe  = regexp.MustCompile(`^[1-9][0-9]*$`)
	strategyRe = regexp.MustCompile(`^[0-9]+$`)
)

// statusProfile — элемент profiles[] ответа status.cgi.
type statusProfile struct {
	Profile     int    `json:"profile"`
	Label       string `json:"label"`
	Description string `json:"description"`
	CurrentLock string `json:"current_lock"`
	MaxStrategy int    `json:"max_strategy"`
	Scope       string `json:"scope"`
	LockSource  string `json:"lock_source"`

	IsUDPGames      bool  `json:"is_udp_games,omitempty"`
	UDPGamesEnabled *bool `json:"udp_games_enabled,omitempty"`
	IsFallback      bool  `json:"is_fallback,omitempty"`
	FallbackEnabled *bool `json:"fallback_enabled,omitempty"`
	IsDNSDesync     bool  `json:"is_dns_desync,omitempty"`
	DNSEnabled      *bool `json:"dns_desync_enabled,omitempty"`
}

// scopeParam возвращает scope запроса ("" -> default) либо 400.
func scopeParam(p params) (string, error) {
	sc := strings.ToLower(p.Get("scope"))
	if sc == "" {
		return orchestra.ScopeDefault, nil
	}
	if !orchestra.ValidScope(sc) {
		return "", errf(http.StatusBadRequest, "Некорректный scope: "+p.Get("scope"))
	}
	return sc, nil
}

func (s *Server) providerText() string {
	data, err := os.ReadFile(filepath.Join(s.paths.StateDir, providerFile))
	if err != nil {
		return providerUnknown
	}
	if v := strings.TrimSpace(string(data)); v != "" {
		return v
	}
	return providerUnknown
}

func boolPtr(b bool) *bool { return &b }

// effectiveLock возвращает отображаемую фиксацию профиля в scope и её источник.
func (s *Server) effectiveLock(scope string, id int) (display, source string) {
	protos := config.ProfileProtos(id)
	if len(protos) == 0 {
		return "auto", orchestra.SourceAuto
	}
	profile := strconv.Itoa(id)
	source, err := s.store.LockSource(scope, profile, protos[0])
	if err != nil {
		return "auto", orchestra.SourceAuto
	}
	if source == orchestra.SourceConflict {
		return "auto", source
	}
	st, err := s.store.GetLock(scope, profile, protos[0])
	if err != nil {
		return "auto", orchestra.SourceConflict
	}
	if st.IsAuto() && scope != orchestra.ScopeDefault {
		if d, err := s.store.GetLock(orchestra.ScopeDefault, profile, protos[0]); err == nil {
			st = d
		}
	}
	return st.String(), source
}

func (s *Server) statusPayload(ctx context.Context, scope string) map[string]any {
	cfg, _ := s.readConfig()
	profiles := make([]statusProfile, 0, config.MaxProfile)
	for _, pr := range cfg.Profiles() {
		lock, src := s.effectiveLock(scope, pr.ID)
		sp := statusProfile{
			Profile: pr.ID, Label: pr.Label, Description: pr.Description,
			CurrentLock: lock, MaxStrategy: pr.MaxStrategy,
			Scope: src, LockSource: src,
		}
		switch pr.ID {
		case config.ProfileUDPGames:
			sp.IsUDPGames = true
			sp.UDPGamesEnabled = boolPtr(cfg.UDPGamesText() == "Включен")
		case config.ProfileFallbackT, config.ProfileFallbackH:
			sp.IsFallback = true
			sp.FallbackEnabled = boolPtr(cfg.FallbackText() == "включен")
		case config.ProfileDNS:
			sp.IsDNSDesync = true
			sp.DNSEnabled = boolPtr(cfg.DNSDesyncText() == "Включен")
		}
		profiles = append(profiles, sp)
	}
	return map[string]any{
		"zapret2_running":       s.zapretRunning(ctx),
		"strategy_locks_status": s.store.StatusText(),
		"hostlist_mode":         cfg.HostlistText(),
		"fwtype":                cfg.FWType(),
		"flowoffload":           cfg.FlowOffload(),
		"tls_blob_mode":         cfg.TLSBlobMenuValue(),
		"wireguard":             cfg.WireGuardText(),
		"auto_mode":             cfg.AutoModeText(),
		"rst_guard":             cfg.RSTGuardText(),
		"reasm":                 cfg.ReasmText(),
		"quic443":               cfg.QUIC443Text(),
		"provider":              s.providerText(),
		"profiles":              profiles,
	}
}

// GET /cgi-bin/status.cgi
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request, p params) {
	if !requireMethod(w, r, http.MethodGet, http.MethodPost) {
		return
	}
	scope, err := scopeParam(p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.statusPayload(r.Context(), scope))
}

func fileDate(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return st.ModTime().UTC().Format("2006-01-02 15:04:05 UTC")
}

func (s *Server) versionPayload() map[string]any {
	zv := ""
	if in, err := zapret.ReadInstalled(s.paths.ZapretRoot); err == nil {
		zv = in.Version
	}
	ver := zator.Version()
	date := fileDate(filepath.Join(s.paths.ConfigDir, "zator.json"))
	cfgDate := fileDate(s.paths.ZapretConfig)
	defDate := fileDate(s.defaultConfigPath())
	pending := false
	if a, err := os.Stat(s.paths.ZapretConfig); err == nil {
		if b, err := os.Stat(s.defaultConfigPath()); err == nil && b.ModTime().After(a.ModTime()) {
			pending = true
		}
	}
	return map[string]any{
		"zapret2_version":       zv,
		"zator_version":         ver,
		"zator_date":            date,
		"webui_version":         ver,
		"webui_date":            date,
		"tracking":              "latest",
		"update_available":      false,
		"latest_release":        "",
		"latest_zator_date":     "",
		"latest_webui_date":     "",
		"config_date":           cfgDate,
		"config_default_date":   defDate,
		"config_update_pending": pending,
	}
}

// GET /cgi-bin/state.cgi — агрегат начальной инициализации.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request, p params) {
	if !requireMethod(w, r, http.MethodGet, http.MethodPost) {
		return
	}
	scope, err := scopeParam(p)
	if err != nil {
		writeErr(w, err)
		return
	}
	cfg, _ := s.readConfig()
	section := func(name string) any {
		v, err := s.settingGet(r.Context(), cfg, name)
		if err != nil {
			return map[string]string{"_error": err.Error()}
		}
		return v
	}
	var backups any
	if v, err := s.backupsList(); err != nil {
		backups = map[string]string{"_error": err.Error()}
	} else {
		backups = v
	}
	out := map[string]any{
		"status":      s.statusPayload(r.Context(), scope),
		"version":     s.versionPayload(),
		"scopes":      s.scopesPayload(cfg),
		"tls_blob":    section(""),
		"wg_blob":     section("wg_blob"),
		"wg_state":    section("wg_state"),
		"fallback":    section("fallback"),
		"udp_games":   section("udp-games"),
		"wssize_ramp": section("wssize_ramp"),
		"auto_mode":   section("auto_mode"),
		"hostlist":    section("hostlist"),
		"rst_guard":   section("rst_guard"),
		"reasm":       section("reasm"),
		"quic443":     section("quic443"),
		"dns_desync":  section("dns_desync"),
		"ports":         section("ports"),
		"provider":      section("provider"),
		"fwtype":        section("fwtype"),
		"flowoffload":   section("flowoffload"),
		"client_scope":  section("client_scope"),
		"nfqws2_errors": section("nfqws2_errors"),
		"backups":       backups,
	}
	writeJSON(w, http.StatusOK, out)
}

// --- scopes.cgi ---

func (s *Server) scopesPayload(cfg *config.Config) map[string]any {
	enabled := cfg.ClientScopeText() == "включен"
	warning := ""
	if enabled {
		cc, err := firewall.ClientScopeConfigFromGetter(cfg.Get, s.paths.ZatorRoot)
		if err == nil {
			err = cc.Validate()
		}
		if err != nil {
			warning = err.Error()
		}
	}
	scopes, err := s.store.ListScopes()
	if err != nil || len(scopes) == 0 {
		scopes = []string{orchestra.ScopeDefault}
	}
	return map[string]any{"enabled": enabled, "warning": warning, "scopes": scopes}
}

// GET /cgi-bin/scopes.cgi
func (s *Server) handleScopes(w http.ResponseWriter, r *http.Request, _ params) {
	if !requireMethod(w, r, http.MethodGet, http.MethodPost) {
		return
	}
	cfg, _ := s.readConfig()
	writeJSON(w, http.StatusOK, s.scopesPayload(cfg))
}

// --- set-lock / clear-lock ---

// lockPrecheck проверяет профиль и конфликт с авторотацией; возвращает номер профиля.
func (s *Server) lockPrecheck(cfg *config.Config, profile string) (int, error) {
	if !profileRe.MatchString(profile) {
		return 0, errf(http.StatusBadRequest, "Некорректный профиль")
	}
	id, err := strconv.Atoi(profile)
	if err != nil {
		return 0, errf(http.StatusBadRequest, "Некорректный профиль")
	}
	return id, nil
}

func autoConflict(cfg *config.Config, id int) error {
	if id >= 1 && id <= 4 && cfg.AutoModeText() == "включен" {
		return errf(http.StatusConflict, "Профиль "+strconv.Itoa(id)+" "+autoRotationMsg)
	}
	return nil
}

// applyProfileLock пишет фиксацию во все протоколы профиля.
func (s *Server) applyProfileLock(scope string, id int, st orchestra.Strategy) error {
	for _, proto := range config.ProfileProtos(id) {
		var err error
		if st.IsAuto() {
			err = s.store.ClearLock(scope, strconv.Itoa(id), proto)
		} else {
			err = s.store.SetLock(scope, strconv.Itoa(id), proto, st)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// voicePorts правит голосовые порты профиля 6 и перезапускает zapret2.
func (s *Server) voicePorts(ctx context.Context, on bool) {
	err := s.editConfigs(func(c *config.Config) error {
		return c.SetPortToken("udp", config.VoicePorts, on)
	})
	if err == nil {
		s.restartIfRunning(ctx)
	}
}

// POST /cgi-bin/set-lock.cgi
func (s *Server) handleSetLock(w http.ResponseWriter, r *http.Request, p params) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	cfg, _ := s.readConfig()
	id, err := s.lockPrecheck(cfg, p.Get("profile"))
	if err != nil {
		writeErr(w, err)
		return
	}
	strategy := p.Get("strategy")
	if !strategyRe.MatchString(strategy) {
		writeError(w, http.StatusBadRequest, "Некорректная стратегия")
		return
	}
	scope, err := scopeParam(p)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := autoConflict(cfg, id); err != nil {
		writeErr(w, err)
		return
	}
	n, convErr := strconv.Atoi(strategy)
	max := cfg.ProfileMaxStrategyOrDefault(id)
	if convErr != nil || (n != 0 && (n < 1 || n > max)) {
		writeError(w, http.StatusBadRequest, "Стратегия вне диапазона")
		return
	}
	if len(config.ProfileProtos(id)) == 0 {
		writeError(w, http.StatusBadRequest, "Не удалось определить протокол профиля")
		return
	}
	if err := s.applyProfileLock(scope, id, orchestra.Strategy(n)); err != nil {
		writeLockFailure(w, err, "Не удалось сохранить состояние профиля")
		return
	}
	if id == 6 {
		s.voicePorts(r.Context(), n != 0)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /cgi-bin/clear-lock.cgi
func (s *Server) handleClearLock(w http.ResponseWriter, r *http.Request, p params) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	cfg, _ := s.readConfig()
	id, err := s.lockPrecheck(cfg, p.Get("profile"))
	if err != nil {
		writeErr(w, err)
		return
	}
	scope, err := scopeParam(p)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := autoConflict(cfg, id); err != nil {
		writeErr(w, err)
		return
	}
	if len(config.ProfileProtos(id)) == 0 {
		writeError(w, http.StatusBadRequest, "Не удалось определить протокол профиля")
		return
	}
	if err := s.applyProfileLock(scope, id, orchestra.StrategyAuto); err != nil {
		writeLockFailure(w, err, "Не удалось сбросить состояние профиля")
		return
	}
	if id == 6 {
		s.voicePorts(r.Context(), true)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeLockFailure(w http.ResponseWriter, err error, msg string) {
	if errors.Is(err, orchestra.ErrConflict) {
		writeError(w, http.StatusConflict, "Конфликт дубликатов в файле фиксаций: "+err.Error())
		return
	}
	if errors.Is(err, orchestra.ErrInvalid) {
		writeError(w, http.StatusBadRequest, "Стратегия вне диапазона")
		return
	}
	writeError(w, http.StatusInternalServerError, msg)
}

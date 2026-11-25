package webapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/firewall"
	"github.com/AloofLibra/zator/internal/netcheck"
	"github.com/AloofLibra/zator/internal/orchestra"
	"github.com/AloofLibra/zator/internal/provider"
	"github.com/AloofLibra/zator/internal/telemetry"
)

// postFn — обработчик POST settings.cgi; возвращает тело успешного ответа.
type postFn func(s *Server, r *http.Request, p params) (map[string]any, error)

var postSettingsTable map[string]postFn

func init() {
	postSettingsTable = map[string]postFn{
		"tls_blob":          (*Server).postTLSBlob,
		"tls_blob_profile":  (*Server).postTLSBlobProfile,
		"fake_mode":         (*Server).postFakeMode,
		"sni_override":      (*Server).postSNIOverride,
		"clone_size":        (*Server).postCloneSize,
		"clone_size_global": (*Server).postGlobalCloneSize,
		"wg_blob":           (*Server).postWGBlob,
		"wg_repeats":        (*Server).postWGRepeats,
		"ports_add":         (*Server).postPortsAdd,
		"ports_remove":      (*Server).postPortsRemove,
		"provider_set":      (*Server).postProviderSet,
		"provider_redetect": (*Server).postProviderRedetect,
		"telemetry_consent": (*Server).postTelemetryConsent,
		"client_scope":      (*Server).postClientScope,
		"wssize_ramp":       (*Server).postWssizeRamp,
		"supersweep":        (*Server).postSupersweep,
		"fwtype":            varSetting("FWTYPE", "Некорректное значение: ", "iptables", "nftables"),
		"flowoffload":       varSetting("FLOWOFFLOAD", "Некорректное значение: ", "donttouch", "none", "software", "hardware"),
		"ifaces":            (*Server).postIfaces,
		"iface":             (*Server).postIfaces,
		"interfaces":        (*Server).postIfaces,
		"config_raw":        (*Server).postConfigRaw,
		"config":            (*Server).postConfigRaw,
		"update_apply":      (*Server).postUpdateApply,
		"update_preflight":  (*Server).postUpdatePreflight,
		"strategy_register": (*Server).postStrategyRegister,
		"h3_curl":           (*Server).postH3Curl,
	}
	for _, t := range toggles {
		t := t
		fn := func(s *Server, r *http.Request, p params) (map[string]any, error) {
			return s.postToggle(r, p, t)
		}
		for _, n := range t.names {
			postSettingsTable[n] = fn
		}
	}
}

func (s *Server) postTelemetryConsent(r *http.Request, p params) (map[string]any, error) {
	enabled, err := parseOnOff(p)
	if err != nil {
		return nil, err
	}
	c := telemetry.New(s.paths)
	settings, err := c.SetEnabled(enabled)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"ok": true, "decided": settings.Decided, "enabled": settings.Enabled,
		"configured": strings.TrimSpace(c.Token) != "" && strings.TrimSpace(c.Endpoint) != "",
		"sent":       false,
	}
	if enabled && out["configured"] == true {
		ctx, cancel := context.WithTimeout(r.Context(), telemetry.SendTimeout)
		defer cancel()
		if err := c.SendStats(ctx); err == nil {
			out["sent"] = true
		} else {
			out["send_error"] = true
		}
	}
	return out, nil
}

// settingsPost разбирает setting и вызывает обработчик.
func (s *Server) settingsPost(r *http.Request, p params) (map[string]any, error) {
	name := normSetting(p.Get("setting"))
	fn, ok := postSettingsTable[name]
	if !ok {
		return nil, errf(http.StatusBadRequest, "Неизвестная настройка")
	}
	return fn(s, r, p)
}

// --- переключатели 0|1 ---

type toggle struct {
	names    []string
	apply    func(c *config.Config, on bool) error
	state    func(c *config.Config) string // nil — поле state не возвращается
	guard    func(s *Server, c *config.Config, on bool) error
	notFound string
}

func parseOnOff(p params) (bool, error) {
	switch v := p.Get("value"); v {
	case "1":
		return true, nil
	case "0":
		return false, nil
	default:
		return false, errf(http.StatusBadRequest, "Некорректное значение: "+v)
	}
}

var toggles = []toggle{
	{
		names:    []string{"wg_state", "wg"},
		apply:    (*config.Config).SetWireGuard,
		notFound: "Стратегия WireGuard не найдена в конфиге (нет блока #Z2R_WG_*)",
	},
	{
		names: []string{"fallback_state", "fallback"},
		apply: (*config.Config).SetFallback,
		guard: func(_ *Server, c *config.Config, on bool) error {
			if on && c.AutoModeText() == "включен" {
				return errf(http.StatusConflict, "Безразборный режим недоступен при включённой авторотации TCP/HTTP. Сначала выключите авторотацию.")
			}
			return nil
		},
		notFound: "Блоки безразборного режима не найдены в конфиге." + cliHint,
	},
	{
		names:    []string{"udp_games_state", "udp_games"},
		apply:    (*config.Config).SetUDPGames,
		notFound: "Блок игрового UDP не найден в конфиге." + cliHint,
	},
	{
		names: []string{"auto_mode_state", "auto_mode"},
		apply: (*config.Config).SetAutoMode,
		state: (*config.Config).AutoModeText,
		guard: func(_ *Server, c *config.Config, on bool) error {
			if on && c.FallbackText() == "включен" {
				return errf(http.StatusConflict, "Авторотация недоступна при включённом безразборном режиме. Сначала выключите безразборный режим.")
			}
			return nil
		},
		notFound: "В конфиге не найдены маркеры авторежима." + cliHint,
	},
	{
		names: []string{"hostlist_state", "hostlist"},
		apply: func(c *config.Config, on bool) error { return c.SetHostlistAuto(on) },
		state: (*config.Config).HostlistText,
	},
	{
		names: []string{"rst_guard_state", "rst_guard"},
		apply: (*config.Config).SetRSTGuard,
		state: (*config.Config).RSTGuardText,
		guard: func(s *Server, _ *config.Config, on bool) error {
			if !on {
				return nil
			}
			if _, err := os.Stat(filepath.Join(s.paths.LuaDir, "rst-guard.lua")); err != nil {
				return errf(http.StatusInternalServerError, "Файл rst-guard.lua отсутствует на устройстве. Включите защиту один раз через CLI (пункт 18).")
			}
			return nil
		},
		notFound: "Строки rst_guard не найдены в конфиге." + cliHint,
	},
	{
		names:    []string{"reasm_state", "reasm"},
		apply:    (*config.Config).SetReasm,
		state:    (*config.Config).ReasmText,
		notFound: "Не найден блок NFQWS2_OPT в конфиге." + cliHint,
	},
	{
		names:    []string{"dns_desync_state", "dns_desync"},
		apply:    (*config.Config).SetDNSDesync,
		state:    (*config.Config).DNSDesyncText,
		notFound: "Блок DNS (UDP:53) не найден в конфиге." + cliHint,
	},
}

func (s *Server) postToggle(r *http.Request, p params, t toggle) (map[string]any, error) {
	on, err := parseOnOff(p)
	if err != nil {
		return nil, err
	}
	cur, installed := s.readConfig()
	if !installed {
		return nil, s.notInstalledErr()
	}
	if t.guard != nil {
		if err := t.guard(s, cur, on); err != nil {
			return nil, err
		}
	}
	if err := s.editConfigs(func(c *config.Config) error { return t.apply(c, on) }); err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, errf(http.StatusInternalServerError, t.notFound)
		}
		return nil, err
	}
	out := map[string]any{"ok": true, "restarted": s.maybeRestart(r.Context(), p)}
	if t.state != nil {
		c, _ := s.readConfig()
		out["state"] = t.state(c)
	}
	return out, nil
}

// varSetting создаёт обработчик простой переменной конфига с белым списком значений.
func varSetting(name, errPrefix string, allowed ...string) postFn {
	return func(s *Server, r *http.Request, p params) (map[string]any, error) {
		v := p.Get("value")
		okVal := false
		for _, a := range allowed {
			okVal = okVal || a == v
		}
		if !okVal {
			return nil, errf(http.StatusBadRequest, errPrefix+v)
		}
		if err := s.editConfigs(func(c *config.Config) error { return c.Set(name, v) }); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "restarted": s.maybeRestart(r.Context(), p)}, nil
	}
}

// --- блобы ---

func (s *Server) blobExists(name string) bool {
	st, err := os.Stat(filepath.Join(s.paths.FakeDir, name))
	return err == nil && !st.IsDir()
}

var (
	maxruDeclRe = regexp.MustCompile(`(?m)^(\s*--blob=maxru:@\S*/)[^/\s]+`)
	wgDeclRe    = regexp.MustCompile(`(--blob=fakewgblob:@\S*/)[^/\s]+`)
	wgRepeatsRe = regexp.MustCompile(`blob=fakewgblob:repeats=[0-9]+`)
)

// editOpt применяет fn к тексту NFQWS2_OPT.
func editOpt(c *config.Config, fn func(opt string) (string, error)) error {
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return fmt.Errorf("%w: %s", config.ErrNotFound, config.OptVar)
	}
	next, err := fn(opt)
	if err != nil {
		return err
	}
	return c.SetNFQWS2Opt(next)
}

// switchTLSBlob переключает ссылки стратегий maxru <-> fake_default_tls и путь блоба.
func switchTLSBlob(c *config.Config, value string) error {
	return editOpt(c, func(opt string) (string, error) {
		lines := strings.Split(opt, "\n")
		for i, ln := range lines {
			if !strings.Contains(ln, "--lua-desync=") || strings.Contains(ln, "strategy=26") {
				continue
			}
			if value == blobDefault {
				lines[i] = strings.ReplaceAll(ln, "blob=maxru", "blob="+blobDefault)
			} else {
				lines[i] = strings.ReplaceAll(ln, "blob="+blobDefault, "blob=maxru")
			}
		}
		out := strings.Join(lines, "\n")
		if value != blobDefault {
			if !maxruDeclRe.MatchString(out) {
				return "", fmt.Errorf("%w: --blob=maxru", config.ErrNotFound)
			}
			out = maxruDeclRe.ReplaceAllString(out, "${1}"+value)
		}
		return out, nil
	})
}

func (s *Server) postTLSBlob(r *http.Request, p params) (map[string]any, error) {
	v := p.Get("value")
	if v != blobDefault && !tlsBlobFileRe.MatchString(v) {
		return nil, errf(http.StatusBadRequest, "Некорректное значение блоба: "+v)
	}
	if v != blobDefault && !s.blobExists(v) {
		return nil, errf(http.StatusBadRequest, "Файл блоба не существует: "+v)
	}
	if err := s.editConfigs(func(c *config.Config) error { return switchTLSBlob(c, v) }); err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, errf(http.StatusInternalServerError, "TLS-блоб не найден в конфиге (нет --blob=maxru:@...)")
		}
		return nil, err
	}
	_ = s.store.SetBlobFile("maxru", v)
	s.notifyTelemetry()
	return map[string]any{"ok": true, "restarted": s.maybeRestart(r.Context(), p)}, nil
}

// overrideProfile проверяет profile из набора профилей с переопределениями (1,2,3,4,8).
func overrideProfile(p params) (string, error) {
	pr := p.Get("profile")
	for _, id := range orchestra.OverrideProfiles {
		if strconv.Itoa(id) == pr {
			return pr, nil
		}
	}
	return "", errf(http.StatusBadRequest, "Некорректный профиль")
}

func (s *Server) postTLSBlobProfile(r *http.Request, p params) (map[string]any, error) {
	profile := p.Get("profile")
	if !orchestra.IsBlobOverrideProfile(profile) {
		return nil, errf(http.StatusBadRequest, "Некорректный профиль: "+profile)
	}
	profNum, _ := strconv.Atoi(profile)
	v := p.Get("value")
	slotName := orchestra.BlobSlotName(profile)
	switch {
	case v == "" || v == "global":
		if err := s.store.ClearOverride(orchestra.OverrideBlob, profile); err != nil {
			return nil, err
		}
		s.notifyTelemetry()
		return map[string]any{"ok": true, "restarted": false, "restart_required": false}, nil
	case v == blobDefault:
		if profNum == 5 {
			return nil, errf(http.StatusBadRequest, "fake_default_tls неприменим к QUIC-профилю (только TLS-профили)")
		}
		if err := s.store.SetOverride(orchestra.OverrideBlob, profile, blobDefault); err != nil {
			return nil, err
		}
		s.notifyTelemetry()
		return map[string]any{"ok": true, "restarted": false, "restart_required": false}, nil
	case !orchestra.ValidBlobFileForProfile(v, profile):
		return nil, errf(http.StatusBadRequest, "Некорректное значение блоба: "+v)
	case !s.blobExists(v):
		return nil, errf(http.StatusBadRequest, "Файл блоба не существует: "+v)
	}

	var slotChanged bool
	if live, lErr := config.Load(s.paths.ZapretConfig); lErr == nil {
		prev, ok := live.SlotBlobFile(slotName)
		slotChanged = !ok || prev != v
	} else {
		slotChanged = true
	}

	err := s.editConfigs(func(c *config.Config) error {
		if _, e := c.EnsureSlotBlob(slotName, v); e != nil {
			return e
		}
		return c.SetSlotBlobFile(slotName, v)
	})
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, errf(http.StatusInternalServerError, "Слот блоба "+slotName+" не найден в конфиге")
		}
		return nil, err
	}
	restarted := false
	if slotChanged {
		restarted = s.maybeRestart(r.Context(), p)
	}
	out := map[string]any{"ok": true, "restarted": restarted, "restart_required": slotChanged}
	if err := s.store.SetBlobFile(slotName, v); err != nil {
		out["warning"] = "Конфиг обновлён, но blob_files не сохранён: " + err.Error()
	}
	if err := s.store.SetOverride(orchestra.OverrideBlob, profile, slotName); err != nil {
		out["warning"] = "Конфиг обновлён, но override не сохранён: " + err.Error()
	}
	s.notifyTelemetry()
	return out, nil
}

func (s *Server) postFakeMode(_ *http.Request, p params) (map[string]any, error) {
	profile, err := overrideProfile(p)
	if err != nil {
		return nil, err
	}
	switch v := p.Get("value"); v {
	case orchestra.ModeClassic:
		err = s.store.ClearOverride(orchestra.OverrideMode, profile)
	case orchestra.ModeClone:
		err = s.store.SetOverride(orchestra.OverrideMode, profile, v)
	default:
		return nil, errf(http.StatusBadRequest, "Некорректное значение: "+v)
	}
	if err != nil {
		return nil, err
	}
	s.notifyTelemetry()
	return map[string]any{"ok": true, "restarted": false, "restart_required": false}, nil
}

func (s *Server) postSNIOverride(_ *http.Request, p params) (map[string]any, error) {
	profile, err := overrideProfile(p)
	if err != nil {
		return nil, err
	}
	value := strings.TrimSpace(p.Get("value"))
	if value == "" {
		err = s.store.ClearOverride(orchestra.OverrideSNI, profile)
	} else {
		if err := orchestra.OverrideSNI.ValidateValue(value); err != nil {
			return nil, errf(http.StatusBadRequest, "Некорректный SNI: "+value)
		}
		err = s.store.SetOverride(orchestra.OverrideSNI, profile, value)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "restarted": false, "restart_required": false}, nil
}

func (s *Server) postCloneSize(_ *http.Request, p params) (map[string]any, error) {
	profile, err := overrideProfile(p)
	if err != nil {
		return nil, err
	}
	v := p.Get("value")
	if v == "" || v == "global" {
		err = s.store.ClearOverride(orchestra.OverrideCloneSize, profile)
	} else {
		if verr := orchestra.OverrideCloneSize.ValidateValue(v); verr != nil {
			return nil, errf(http.StatusBadRequest, fmt.Sprintf("Некорректное значение: %s (допустимо %d..%d)", v, orchestra.CloneSizeMin, orchestra.CloneSizeMax))
		}
		err = s.store.SetOverride(orchestra.OverrideCloneSize, profile, v)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "restarted": false, "restart_required": false}, nil
}

func (s *Server) postGlobalCloneSize(_ *http.Request, p params) (map[string]any, error) {
	v := strings.TrimSpace(p.Get("value"))
	if v == "" || v == "global" {
		if err := s.store.ClearOverride(orchestra.OverrideCloneSize, "0"); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	}
	if err := orchestra.OverrideCloneSize.ValidateValue(v); err != nil {
		return nil, errf(http.StatusBadRequest, fmt.Sprintf("Некорректное значение: %s (допустимо %d..%d)", v, orchestra.CloneSizeMin, orchestra.CloneSizeMax))
	}
	if err := s.store.SetOverride(orchestra.OverrideCloneSize, "0", v); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (s *Server) postWGBlob(r *http.Request, p params) (map[string]any, error) {
	v := p.Get("value")
	if !wgBlobFileRe.MatchString(v) {
		return nil, errf(http.StatusBadRequest, "Некорректное значение блоба: "+v)
	}
	if !s.blobExists(v) {
		return nil, errf(http.StatusBadRequest, "Файл блоба не существует: "+v)
	}
	err := s.editConfigs(func(c *config.Config) error {
		return editOpt(c, func(opt string) (string, error) {
			if !wgDeclRe.MatchString(opt) {
				return "", fmt.Errorf("%w: fakewgblob", config.ErrNotFound)
			}
			return wgDeclRe.ReplaceAllString(opt, "${1}"+v), nil
		})
	})
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, errf(http.StatusInternalServerError, "Стратегия WireGuard не найдена в конфиге (нет --blob=fakewgblob:@...)")
		}
		return nil, err
	}
	return map[string]any{"ok": true, "restarted": s.maybeRestart(r.Context(), p)}, nil
}

func (s *Server) postWGRepeats(r *http.Request, p params) (map[string]any, error) {
	v := p.Get("value")
	if !strategyRe.MatchString(v) {
		return nil, errf(http.StatusBadRequest, "Некорректное значение repeats")
	}
	n, _ := strconv.Atoi(v)
	if n < 2 || n > 99 {
		return nil, errf(http.StatusBadRequest, "Значение repeats должно быть от 2 до 99")
	}
	err := s.editConfigs(func(c *config.Config) error {
		return editOpt(c, func(opt string) (string, error) {
			if !wgRepeatsRe.MatchString(opt) {
				return "", fmt.Errorf("%w: repeats", config.ErrNotFound)
			}
			return wgRepeatsRe.ReplaceAllString(opt, "blob=fakewgblob:repeats="+strconv.Itoa(n)), nil
		})
	})
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, errf(http.StatusInternalServerError, "Стратегия WireGuard не найдена в конфиге (нет blob=fakewgblob:repeats=)")
		}
		return nil, err
	}
	return map[string]any{"ok": true, "restarted": s.maybeRestart(r.Context(), p)}, nil
}

// --- порты ---

var portTokenRe = regexp.MustCompile(`^([0-9]{1,5})(-([0-9]{1,5}))?$`)

func validPortToken(t string) bool {
	m := portTokenRe.FindStringSubmatch(t)
	if m == nil {
		return false
	}
	lo, _ := strconv.Atoi(m[1])
	if lo < 1 || lo > 65535 {
		return false
	}
	if m[3] != "" {
		hi, _ := strconv.Atoi(m[3])
		return hi >= lo && hi <= 65535
	}
	return true
}

func portProto(p params) (string, error) {
	proto := strings.ToLower(p.Get("proto"))
	if proto != "tcp" && proto != "udp" {
		return "", errf(http.StatusBadRequest, "Некорректный протокол: "+p.Get("proto"))
	}
	return proto, nil
}

func (s *Server) postPortsAdd(r *http.Request, p params) (map[string]any, error) {
	proto, err := portProto(p)
	if err != nil {
		return nil, err
	}
	raw := p.Get("value")
	var valid, invalid []string
	for _, t := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' || r == '\t' }) {
		if validPortToken(t) {
			valid = append(valid, t)
		} else {
			invalid = append(invalid, t)
		}
	}
	var added, skipped []string
	err = s.editConfigs(func(c *config.Config) error {
		var e error
		added, skipped, e = c.AddUserPorts(proto, valid)
		return e
	})
	if err != nil {
		return nil, err
	}
	skipped = append(skipped, invalid...)
	if len(added) == 0 {
		return nil, errf(http.StatusBadRequest, "Ничего не добавлено (некорректные значения или дубликаты): "+strings.Join(skipped, ","))
	}
	return map[string]any{
		"ok": true, "added": strings.Join(added, ","), "skipped": strings.Join(skipped, ","),
		"restarted": s.maybeRestart(r.Context(), p),
	}, nil
}

func (s *Server) postPortsRemove(r *http.Request, p params) (map[string]any, error) {
	proto, err := portProto(p)
	if err != nil {
		return nil, err
	}
	v := p.Get("value")
	if proto == "udp" && v == config.UDPGamesPorts {
		return nil, errf(http.StatusBadRequest, "Диапазон 1026-65531 управляется переключателем игрового UDP на вкладке Настройки")
	}
	found := false
	err = s.editConfigs(func(c *config.Config) error {
		f, e := c.RemoveUserPort(proto, v)
		if e == nil && f {
			found = true
		}
		return e
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errf(http.StatusBadRequest, "Порт не найден среди добавленных: "+v)
	}
	return map[string]any{"ok": true, "restarted": s.maybeRestart(r.Context(), p)}, nil
}

// --- провайдер ---

func (s *Server) postProviderSet(_ *http.Request, p params) (map[string]any, error) {
	name := p.Get("name")
	city := p.Get("city")
	full, err := s.provider.Set(name, city)
	if err != nil {
		if errors.Is(err, provider.ErrEmpty) {
			return nil, errf(http.StatusBadRequest, "Укажите название провайдера")
		}
		return nil, err
	}
	return map[string]any{"ok": true, "provider": full}, nil
}

func (s *Server) postProviderRedetect(r *http.Request, _ params) (map[string]any, error) {
	res, err := s.provider.Redetect(r.Context())
	if err != nil {
		if errors.Is(err, provider.ErrNotDetected) {
			return map[string]any{
				"ok":       true,
				"provider": provider.Unknown,
				"warning":  "Не удалось определить провайдера",
			}, nil
		}
		return nil, errf(http.StatusBadGateway, "Не удалось определить провайдера: "+err.Error())
	}
	return map[string]any{
		"ok":       true,
		"provider": res.Display,
		"asn":      res.ASN,
		"city":     res.City,
		"brand":    res.Brand,
		"source":   res.Source,
	}, nil
}

// --- client scope ---

var markMaskRe = regexp.MustCompile(`^0[xX][0-9a-fA-F]{1,8}$`)

func (s *Server) postClientScope(r *http.Request, p params) (map[string]any, error) {
	enable := p.Get("client_scope_enable")
	if enable != "0" && enable != "1" {
		return nil, errf(http.StatusBadRequest, "Некорректное значение: "+enable)
	}
	mask := p.Get("client_scope_mark_mask")
	if mask != "" && !markMaskRe.MatchString(mask) {
		return nil, errf(http.StatusBadRequest, "Некорректная маска: "+mask)
	}
	shift, max := p.Get("client_scope_mark_shift"), p.Get("client_scope_mark_max")
	if shift == "" {
		shift = "8"
	}
	if max == "" {
		max = "255"
	}
	if n, err := strconv.Atoi(shift); err != nil || n < 0 || n > 31 {
		return nil, errf(http.StatusBadRequest, "Некорректный сдвиг: "+shift)
	}
	if n, err := strconv.Atoi(max); err != nil || n < 0 || n > 255 {
		return nil, errf(http.StatusBadRequest, "Некорректный максимум: "+max)
	}
	warning := ""
	enabled := enable == "1"
	err := s.editConfigs(func(c *config.Config) error {
		for k, v := range map[string]string{
			"CLIENT_SCOPE_MARK_MASK": mask, "CLIENT_SCOPE_MARK_SHIFT": shift, "CLIENT_SCOPE_MARK_MAX": max,
		} {
			if err := c.Set(k, v); err != nil {
				return err
			}
		}
		on := enabled
		if on {
			if mask == "" {
				on, warning = false, "Маска метки пуста: client scope безопасно выключен"
			} else if err := s.validScope(c); err != nil {
				on, warning = false, err.Error()
			}
		}
		if on {
			return c.Set("CLIENT_SCOPE_ENABLE", "1")
		}
		return c.Set("CLIENT_SCOPE_ENABLE", "0")
	})
	if err != nil {
		return nil, err
	}
	c, _ := s.readConfig()
	_ = s.app.SyncCustomDHooks(c)
	return map[string]any{
		"ok": true, "restarted": s.maybeRestart(r.Context(), p),
		"enabled": c.ClientScopeText() == "включен", "warning": warning,
	}, nil
}

func (s *Server) validScope(c *config.Config) error {
	cc, err := firewall.ClientScopeConfigFromGetter(c.Get, s.paths.ZatorRoot)
	if err != nil {
		return err
	}
	if err := cc.Validate(); err != nil {
		return err
	}
	mapPath, err := clientScopeMapPath(cc.MapFile, s.paths.ZatorRoot)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(mapPath)
	if err != nil {
		return fmt.Errorf("не удалось прочитать client_scope.tsv: %w", err)
	}
	entries, _, err := cc.ParseTSV(strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("добавьте хотя бы один IP-маппинг перед включением client scopes")
	}
	return nil
}

// --- wssize_ramp ---

func (s *Server) postWssizeRamp(r *http.Request, p params) (map[string]any, error) {
	vars := map[string]string{}
	if v := p.Get("value"); v != "" {
		if v != "0" && v != "1" {
			return nil, errf(http.StatusBadRequest, "Некорректное значение: "+v)
		}
		vars["Z2R_WSSIZE_RAMP_ENABLE"] = v
	}
	if m := strings.ToLower(p.Get("mode")); m != "" {
		if m != "full" && m != "lite" {
			return nil, errf(http.StatusBadRequest, "Некорректный режим: "+m)
		}
		vars["Z2R_WSSIZE_RAMP_MODE"] = m
	}
	if sc := p.Get("scale"); sc != "" {
		n, err := strconv.Atoi(sc)
		if err != nil || n < 0 || n > 14 {
			return nil, errf(http.StatusBadRequest, "Некорректный scale: "+sc)
		}
		vars["Z2R_WSSIZE_RAMP_SCALE"] = strconv.Itoa(n)
	}
	if len(vars) == 0 {
		return nil, errf(http.StatusBadRequest, "Некорректное значение: ")
	}
	if vars["Z2R_WSSIZE_RAMP_ENABLE"] == "1" {
		if _, err := os.Stat(filepath.Join(s.paths.LuaDir, "wssize-ramp.lua")); err != nil {
			return nil, errf(http.StatusInternalServerError, "Файл wssize-ramp.lua отсутствует на устройстве.")
		}
	}
	err := s.editConfigs(func(c *config.Config) error {
		for k, v := range vars {
			if err := c.Set(k, v); err != nil {
				return err
			}
		}
		if _, err := c.SyncWssizeRampVars(); err != nil {
			return err
		}
		c.StripWssizeShellIf()
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"ok": true, "restarted": s.maybeRestart(r.Context(), p)}
	// При включении WSSIZE подтянуть ipv4 (force при явном refresh_ips=1).
	if vars["Z2R_WSSIZE_RAMP_ENABLE"] == "1" || p.Get("refresh_ips") == "1" {
		force := p.Get("refresh_ips") == "1"
		if res, e := s.app.RefreshGooglevideoIPv4(r.Context(), force); e == nil {
			out["ipv4_status"] = res.Status
			out["ipv4_count"] = res.Count
		} else {
			out["ipv4_error"] = e.Error()
		}
	}
	return out, nil
}

func (s *Server) h3Env() netcheck.H3Env {
	if base, ok := s.checker.(*netcheck.Checker); ok {
		return base.H3Env
	}
	return netcheck.H3Env{}
}

func (s *Server) postH3Curl(r *http.Request, p params) (map[string]any, error) {
	action := strings.ToLower(p.Get("action"))
	if action == "" {
		action = strings.ToLower(p.Get("value"))
	}
	env := s.h3Env()
	switch action {
	case "install", "update":
		if _, err := env.Install(r.Context(), s.paths, action == "update"); err != nil {
			switch {
			case errors.Is(err, netcheck.ErrH3Offline), errors.Is(err, netcheck.ErrH3ArchUnsupported):
				return nil, errf(http.StatusBadRequest, err.Error())
			default:
				return nil, errf(http.StatusBadGateway, "Не удалось установить h3-curl: "+err.Error())
			}
		}
	case "remove":
		if err := netcheck.RemoveH3Curl(s.paths); err != nil {
			return nil, errf(http.StatusInternalServerError, err.Error())
		}
	default:
		return nil, errf(http.StatusBadRequest, "Некорректное действие: "+action)
	}
	st := env.Detect(s.paths)
	return map[string]any{
		"ok":      true,
		"found":   st.Found,
		"path":    st.Path,
		"arch":    st.Arch,
		"managed": st.Managed,
	}, nil
}

package webapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/provider"
)

func settingGetMap(t *testing.T, e *env, setting string) map[string]any {
	t.Helper()
	path := "/cgi-bin/settings.cgi"
	if setting != "" {
		path += "?setting=" + setting
	}
	resp, out := e.get(path)
	wantStatus(t, resp, http.StatusOK)
	return out
}

func liveConfig(t *testing.T, e *env) *config.Config {
	t.Helper()
	c, err := config.Load(e.app.Paths.ZapretConfig)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSettingsGetDefaults(t *testing.T) {
	t.Setenv("Z2R_OFFLINE", "1")
	e := newEnv(t, envOpts{})
	tests := []struct {
		setting string
		key     string
		want    any
	}{
		{"auto_mode", "state", "выключен"},
		{"auto_mode", "enabled", false},
		{"hostlist", "state", "по листам"},
		{"hostlist", "auto", false},
		{"rst_guard", "enabled", true},
		{"rst_guard", "lua_available", true},
		{"reasm", "enabled", false},
		{"quic443", "state", "выключены"},
		{"fallback", "state", "выключен"},
		{"fallback", "enabled", false},
		{"udp-games", "enabled", false},
		{"dns_desync", "enabled", false},
		{"wg_state", "state", "0"},
		{"wg_state", "enabled", false},
		{"provider", "provider", "Не определён"},
		{"fwtype", "value", "iptables"},
		{"wssize_ramp", "enabled", false},
		{"wssize_ramp", "mode", "full"},
		{"wssize_ramp", "googlevideo_local_collection", "disabled"},
		{"supersweep", "state", "idle"},
		{"update_check", "update_available", false},
		{"recommendations", "status", "unknown_provider"},
	}
	for _, tc := range tests {
		t.Run(tc.setting+"/"+tc.key, func(t *testing.T) {
			out := settingGetMap(t, e, tc.setting)
			if out[tc.key] != tc.want {
				t.Errorf("%s = %v, want %v (%v)", tc.key, out[tc.key], tc.want, out)
			}
		})
	}

	ports := settingGetMap(t, e, "ports")
	tcp := ports["tcp"].(map[string]any)
	if tcp["base"] != "80,443,2053,2083,2087,2096,8443" || len(tcp["user"].([]any)) != 0 {
		t.Errorf("ports tcp = %v", tcp)
	}

	wg := settingGetMap(t, e, "wg_blob")
	if wg["current_blob"] != "wg_initial_fake_2.bin" || wg["current_repeats"] != "10" {
		t.Errorf("wg_blob = %v", wg)
	}
	if bl := wg["available_blobs"].([]any); len(bl) < 2 {
		t.Errorf("wg available = %v", bl)
	}

	tls := settingGetMap(t, e, "")
	for _, k := range []string{"current_mode", "current_blob", "available_blobs", "profile_blobs", "profile_modes", "profile_snis", "profile_sizes"} {
		if _, ok := tls[k]; !ok {
			t.Errorf("tls_blob: нет %s", k)
		}
	}
	if modes := tls["profile_modes"].(map[string]any); modes["1"] != "classic" {
		t.Errorf("profile_modes = %v", modes)
	}
	// Неизвестный setting на GET = TLS-блоб.
	if out := settingGetMap(t, e, "whatever"); out["current_mode"] == nil {
		t.Errorf("unknown GET = %v", out)
	}
}

func TestSettingsToggles(t *testing.T) {
	e := newEnv(t, envOpts{})

	// hostlist
	resp, out := e.post("/cgi-bin/settings.cgi", "setting", "hostlist_state", "value", "1")
	wantOK(t, resp, out)
	if out["state"] != "авто" || out["restarted"] != false {
		t.Errorf("hostlist = %v", out)
	}
	if !strings.Contains(e.zapretConfig(), "MODE_FILTER=autohostlist") {
		t.Error("MODE_FILTER не обновлён")
	}
	// config.default синхронизируется.
	def, _ := os.ReadFile(filepath.Join(e.app.Paths.ConfigDir, "config.default"))
	if !strings.Contains(string(def), "MODE_FILTER=autohostlist") {
		t.Error("config.default не обновлён")
	}

	// quic443, wg, reasm, dns, udp_games
	cases := []struct {
		setting string
		check   func(c *config.Config) bool
	}{
		{"quic443_state", func(c *config.Config) bool { return c.QUIC443Text() == "включены" }},
		{"wg_state", func(c *config.Config) bool { return c.WireGuardText() == "включено" }},
		{"reasm_state", func(c *config.Config) bool { return c.ReasmText() == "включено" }},
		{"udp_games_state", func(c *config.Config) bool { return c.UDPGamesText() == "Включен" }},
		{"fallback_state", func(c *config.Config) bool { return c.FallbackText() == "включен" }},
	}
	for _, tc := range cases {
		resp, out := e.post("/cgi-bin/settings.cgi", "setting", tc.setting, "value", "1")
		wantOK(t, resp, out)
		if !tc.check(liveConfig(t, e)) {
			t.Errorf("%s=1: состояние не применилось", tc.setting)
		}
		resp, out = e.post("/cgi-bin/settings.cgi", "setting", tc.setting, "value", "0")
		wantOK(t, resp, out)
		if tc.check(liveConfig(t, e)) {
			t.Errorf("%s=0: состояние не снялось", tc.setting)
		}
	}

	// dns_desync: по умолчанию выключен (иначе allow_nohost ломает весь DNS).
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "dns_desync_state", "value", "1")
	wantOK(t, resp, out)
	if liveConfig(t, e).DNSDesyncText() != "Включен" {
		t.Error("dns on не применился")
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "dns_desync_state", "value", "0")
	wantOK(t, resp, out)
	if out["state"] != "Выключен" || liveConfig(t, e).DNSDesyncText() != "Выключен" {
		t.Errorf("dns off: %v", out)
	}

	// rst_guard
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "rst_guard_state", "value", "0")
	wantOK(t, resp, out)
	if out["state"] != "выключен" {
		t.Errorf("rst off: %v", out)
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "rst_guard_state", "value", "1")
	wantOK(t, resp, out)
	if out["state"] != "включен" {
		t.Errorf("rst on: %v", out)
	}
}

func TestSettingsValidationErrors(t *testing.T) {
	e := newEnv(t, envOpts{})
	resp, out := e.post("/cgi-bin/settings.cgi", "setting", "hostlist_state", "value", "2")
	wantError(t, resp, out, 400, "Некорректное значение: 2")
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "nonsense", "value", "1")
	wantError(t, resp, out, 400, "Неизвестная настройка")
	resp, out = e.post("/cgi-bin/settings.cgi", "value", "1")
	wantError(t, resp, out, 400, "Неизвестная настройка")

	// Взаимная блокировка авторотации и безразбора.
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "auto_mode_state", "value", "1")
	wantOK(t, resp, out)
	if out["state"] != "включен" {
		t.Errorf("auto state = %v", out["state"])
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "fallback_state", "value", "1")
	wantError(t, resp, out, 409, "Безразборный режим недоступен при включённой авторотации TCP/HTTP. Сначала выключите авторотацию.")
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "auto_mode_state", "value", "0")
	wantOK(t, resp, out)
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "fallback_state", "value", "1")
	wantOK(t, resp, out)
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "auto_mode_state", "value", "1")
	wantError(t, resp, out, 409, "Авторотация недоступна при включённом безразборном режиме. Сначала выключите безразборный режим.")

	// rst_guard без Lua-файла.
	_ = os.Remove(filepath.Join(e.app.Paths.LuaDir, "rst-guard.lua"))
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "rst_guard_state", "value", "1")
	if resp.StatusCode != 500 || !strings.Contains(out["error"].(string), "rst-guard.lua") {
		t.Errorf("rst_guard без lua: %d %v", resp.StatusCode, out)
	}
}

func TestSettingsAutoModeSwitchesBlocks(t *testing.T) {
	e := newEnv(t, envOpts{})
	before := e.zapretConfig()
	resp, out := e.post("/cgi-bin/settings.cgi", "setting", "auto_mode_state", "value", "1")
	wantOK(t, resp, out)
	on := e.zapretConfig()
	if on == before || !strings.Contains(on, "#Z2R_AUTO_MODE=1") {
		t.Fatal("конфиг не изменился")
	}
	c := liveConfig(t, e)
	std, _ := c.FindBlock(config.BlockAutoStandard)
	if strings.Contains(strings.Join(std.Lines, "\n"), "\n--filter-tcp=443,2053") {
		t.Error("стандартные блоки остались активными")
	}
	blk, _ := c.AutoBlock("3")
	if !strings.Contains(blk.Text(), "--filter-tcp=443") || strings.Contains(blk.Text(), "--skip --filter-tcp") {
		t.Errorf("блок AUTO_3 не включён:\n%s", blk.Text())
	}
	// Возврат даёт исходный конфиг.
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "auto_mode_state", "value", "0")
	wantOK(t, resp, out)
	if e.zapretConfig() != before {
		t.Error("выключение авторотации не вернуло исходный config")
	}
}

func TestSettingsRestartWhenRunning(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.svc.running = true
	resp, out := e.post("/cgi-bin/settings.cgi", "setting", "quic443_state", "value", "1")
	wantOK(t, resp, out)
	if out["restarted"] != true {
		t.Errorf("restarted = %v", out["restarted"])
	}
	// restart=0 откладывает рестарт.
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "quic443_state", "value", "0", "restart", "0")
	wantOK(t, resp, out)
	if out["restarted"] != false {
		t.Errorf("restarted = %v", out["restarted"])
	}
	if got := strings.Join(e.svc.called(), ","); got != "restart" {
		t.Errorf("calls = %s", got)
	}
}

func TestSettingsPorts(t *testing.T) {
	e := newEnv(t, envOpts{})
	resp, out := e.post("/cgi-bin/settings.cgi", "setting", "ports_add", "proto", "tcp", "value", "8080,9000-9100,bad,443")
	wantOK(t, resp, out)
	if out["added"] != "8080,9000-9100" || out["skipped"] != "443,bad" {
		t.Errorf("ports_add = %v", out)
	}
	ports := settingGetMap(t, e, "ports")["tcp"].(map[string]any)
	if ports["full"] != "8080,9000-9100,80,443,2053,2083,2087,2096,8443" {
		t.Errorf("full = %v", ports["full"])
	}
	if u := ports["user"].([]any); len(u) != 2 || u[0] != "8080" {
		t.Errorf("user = %v", u)
	}

	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "ports_remove", "proto", "tcp", "value", "8080")
	wantOK(t, resp, out)
	if got := liveConfig(t, e).PortsFor("tcp").Full; got != "9000-9100,80,443,2053,2083,2087,2096,8443" {
		t.Errorf("после remove = %s", got)
	}

	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "ports_remove", "proto", "tcp", "value", "1234")
	wantError(t, resp, out, 400, "Порт не найден среди добавленных: 1234")
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "ports_remove", "proto", "tcp", "value", "443")
	wantError(t, resp, out, 400, "Порт не найден среди добавленных: 443")
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "ports_remove", "proto", "udp", "value", "1026-65531")
	wantError(t, resp, out, 400, "Диапазон 1026-65531 управляется переключателем игрового UDP на вкладке Настройки")
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "ports_add", "proto", "icmp", "value", "1")
	wantError(t, resp, out, 400, "Некорректный протокол: icmp")
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "ports_add", "proto", "udp", "value", "443")
	wantError(t, resp, out, 400, "Ничего не добавлено (некорректные значения или дубликаты): 443")
}

func TestSettingsProvider(t *testing.T) {
	e := newEnv(t, envOpts{})
	resp, out := e.post("/cgi-bin/settings.cgi", "setting", "provider_set", "name", "MTS", "city", "Moscow")
	wantOK(t, resp, out)
	if out["provider"] != "MTS - Moscow" {
		t.Errorf("provider = %v", out)
	}
	if got := settingGetMap(t, e, "provider")["provider"]; got != "MTS - Moscow" {
		t.Errorf("GET provider = %v", got)
	}
	_, st := e.get("/cgi-bin/status.cgi")
	if st["provider"] != "MTS - Moscow" {
		t.Errorf("status provider = %v", st["provider"])
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "provider_set", "name", "  ")
	wantError(t, resp, out, 400, "Укажите название провайдера")
}

func TestSettingsProviderRedetect(t *testing.T) {
	const ipwhoOK = `{"success":true,"city":"Moscow","connection":{"asn":8359,"isp":"PJSC MTS"}}`
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		switch r.URL.Path {
		case "/ipwho":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(ipwhoOK))
		default:
			http.Error(w, "no", 500)
		}
	}))
	t.Cleanup(srv.Close)

	e := newEnv(t, envOpts{})
	d := provider.New(e.app.Paths)
	d.HTTP = srv.Client()
	d.IPWhoURL = srv.URL + "/ipwho"
	d.IPInfoURL = srv.URL + "/ipinfo"
	d.IPAPIURL = srv.URL + "/ipapi"
	d.ASNRemoteURL = ""
	e.srv.provider = d

	resp, out := e.post("/cgi-bin/settings.cgi", "setting", "provider_redetect")
	wantOK(t, resp, out)
	if out["provider"] != "MTS - Moscow" {
		t.Errorf("redetect = %v", out)
	}
	if out["source"] != "ipwho.is" || out["asn"] != float64(8359) {
		t.Errorf("meta = %v", out)
	}
	if got := settingGetMap(t, e, "provider")["provider"]; got != "MTS - Moscow" {
		t.Errorf("GET provider = %v", got)
	}
	_, st := e.get("/cgi-bin/status.cgi")
	if st["provider"] != "MTS - Moscow" {
		t.Errorf("status provider = %v", st["provider"])
	}
	if hits["/ipwho"] == 0 {
		t.Error("ipwho.is не вызывался")
	}
}

func TestSettingsBlobsAndWG(t *testing.T) {
	e := newEnv(t, envOpts{})

	// TLS-блоб: файл, затем возврат на встроенный.
	resp, out := e.post("/cgi-bin/settings.cgi", "setting", "tls_blob", "value", "tls_clienthello_5.bin")
	wantOK(t, resp, out)
	if liveConfig(t, e).TLSBlobFile() != "tls_clienthello_5.bin" {
		t.Errorf("blob file = %s", liveConfig(t, e).TLSBlobFile())
	}
	if m := liveConfig(t, e).TLSBlobModeValue(); m != "maxru" {
		t.Errorf("mode = %s", m)
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "tls_blob", "value", "fake_default_tls")
	wantOK(t, resp, out)
	if m := liveConfig(t, e).TLSBlobModeValue(); m != "fake_default_tls" {
		t.Errorf("mode = %s", m)
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "tls_blob", "value", "../../etc/passwd")
	wantError(t, resp, out, 400, "Некорректное значение блоба: ../../etc/passwd")
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "tls_blob", "value", "tls_missing.bin")
	wantError(t, resp, out, 400, "Файл блоба не существует: tls_missing.bin")

	// WG.
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "wg_blob", "value", "wg_initial_fake_1.bin")
	wantOK(t, resp, out)
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "wg_repeats", "value", "7")
	wantOK(t, resp, out)
	file, rep := liveConfig(t, e).WireGuardBlob()
	if file != "wg_initial_fake_1.bin" || rep != "7" {
		t.Errorf("WG = %s %s", file, rep)
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "wg_repeats", "value", "1")
	wantError(t, resp, out, 400, "Значение repeats должно быть от 2 до 99")
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "wg_repeats", "value", "x")
	wantError(t, resp, out, 400, "Некорректное значение repeats")
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "wg_blob", "value", "evil.bin")
	wantError(t, resp, out, 400, "Некорректное значение блоба: evil.bin")
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "wg_blob", "value", "wg_initial_fake_9.bin")
	wantError(t, resp, out, 400, "Файл блоба не существует: wg_initial_fake_9.bin")
}

func TestSettingsProfileOverrides(t *testing.T) {
	e := newEnv(t, envOpts{})

	resp, out := e.post("/cgi-bin/settings.cgi", "setting", "fake_mode", "profile", "1", "value", "clone")
	wantOK(t, resp, out)
	if out["restarted"] != false || out["restart_required"] != false {
		t.Errorf("fake_mode = %v", out)
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "clone_size", "profile", "1", "value", "964")
	wantOK(t, resp, out)
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "tls_blob_profile", "profile", "2", "value", "tls_clienthello_5.bin")
	wantOK(t, resp, out)
	if out["restart_required"] != true {
		t.Errorf("tls_blob_profile = %v", out)
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "tls_blob_profile", "profile", "8", "value", "fake_default_tls")
	wantOK(t, resp, out)
	if out["restart_required"] != false {
		t.Errorf("tls_blob_profile builtin = %v", out)
	}

	tls := settingGetMap(t, e, "")
	if tls["profile_modes"].(map[string]any)["1"] != "clone" ||
		tls["profile_sizes"].(map[string]any)["1"] != "964" ||
		tls["profile_blobs"].(map[string]any)["2"] != "tls_clienthello_5.bin" ||
		tls["profile_blobs"].(map[string]any)["8"] != "fake_default_tls" {
		t.Errorf("overrides = %v", tls)
	}
	if !strings.Contains(e.zapretConfig(), "--blob=z2r_prof_2:@/opt/zator/files/fake/tls_clienthello_5.bin") {
		t.Error("слот z2r_prof_2 не обновлён")
	}

	// Сброс.
	e.post("/cgi-bin/settings.cgi", "setting", "fake_mode", "profile", "1", "value", "classic")
	e.post("/cgi-bin/settings.cgi", "setting", "clone_size", "profile", "1", "value", "global")
	e.post("/cgi-bin/settings.cgi", "setting", "tls_blob_profile", "profile", "2", "value", "")
	tls = settingGetMap(t, e, "")
	if tls["profile_modes"].(map[string]any)["1"] != "classic" || tls["profile_sizes"].(map[string]any)["1"] != "" ||
		tls["profile_blobs"].(map[string]any)["2"] != "" {
		t.Errorf("после сброса: %v", tls)
	}

	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "fake_mode", "profile", "5", "value", "clone")
	wantError(t, resp, out, 400, "Некорректный профиль")
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "clone_size", "profile", "1", "value", "10")
	wantStatus(t, resp, 400)
}

func TestSettingsWssizeFwtypeClientScope(t *testing.T) {
	e := newEnv(t, envOpts{})
	resp, out := e.post("/cgi-bin/settings.cgi", "setting", "wssize_ramp", "value", "1", "mode", "lite", "scale", "3")
	wantOK(t, resp, out)
	w := settingGetMap(t, e, "wssize_ramp")
	if w["enabled"] != true || w["mode"] != "lite" || w["scale"] != float64(3) {
		t.Errorf("wssize = %v", w)
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "wssize_ramp", "value", "1", "scale", "99")
	wantStatus(t, resp, 400)

	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "fwtype", "value", "nftables")
	wantOK(t, resp, out)
	if liveConfig(t, e).FWType() != "nftables" {
		t.Error("fwtype не применён")
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "fwtype", "value", "pf")
	wantError(t, resp, out, 400, "Некорректное значение: pf")

	// client_scope с пустой маской безопасно выключается.
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "client_scope", "client_scope_enable", "1", "client_scope_mark_mask", "")
	wantOK(t, resp, out)
	if out["enabled"] != false || out["warning"] == "" {
		t.Errorf("client_scope = %v", out)
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "client_scope", "client_scope_enable", "1", "client_scope_mark_mask", "zz")
	wantStatus(t, resp, 400)
}

func TestSupersweepEndpoints(t *testing.T) {
	e := newEnv(t, envOpts{})
	resp, out := e.post("/cgi-bin/settings.cgi", "setting", "supersweep", "value", "start", "domain", "example.com\nfoo.org")
	wantOK(t, resp, out)
	if !e.sweeper.started || len(e.sweeper.params.Domains) != 2 {
		t.Errorf("sweeper = %+v", e.sweeper)
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "supersweep", "value", "start")
	wantError(t, resp, out, 409, "Суперавтопрогон уже выполняется")
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "supersweep", "value", "status")
	wantOK(t, resp, out)
	if out["running"] != true {
		t.Errorf("status = %v", out)
	}
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "supersweep", "value", "cancel")
	wantOK(t, resp, out)
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "supersweep", "value", "cancel")
	wantError(t, resp, out, 409, "Суперавтопрогон не запущен")
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "supersweep", "value", "explode")
	wantStatus(t, resp, 400)
}

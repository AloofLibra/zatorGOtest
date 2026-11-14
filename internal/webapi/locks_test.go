package webapi

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/orchestra"
)

func profileOf(t *testing.T, st map[string]any, id int) map[string]any {
	t.Helper()
	for _, v := range st["profiles"].([]any) {
		m := v.(map[string]any)
		if int(m["profile"].(float64)) == id {
			return m
		}
	}
	t.Fatalf("профиль %d не найден", id)
	return nil
}

func TestStatusShape(t *testing.T) {
	e := newEnv(t, envOpts{})
	resp, st := e.get("/cgi-bin/status.cgi")
	wantStatus(t, resp, http.StatusOK)
	for _, k := range []string{"zapret2_running", "strategy_locks_status", "hostlist_mode", "fwtype", "flowoffload",
		"tls_blob_mode", "wireguard", "auto_mode", "rst_guard", "reasm", "quic443", "provider", "client_scope", "profiles", "health"} {
		if _, ok := st[k]; !ok {
			t.Errorf("нет поля %s", k)
		}
	}
	if h, ok := st["health"].(map[string]any); !ok || h["overall"] == nil {
		t.Errorf("health = %v", st["health"])
	}
	if cs, ok := st["client_scope"].(map[string]any); !ok || cs["mode"] == nil {
		t.Errorf("client_scope = %v", st["client_scope"])
	}
	if st["strategy_locks_status"] != "Нет" || st["fwtype"] != "iptables" || st["auto_mode"] != "выключен" {
		t.Errorf("status = %v", st)
	}
	// Максимумы считаются по встроенному config.default (44 TLS-стратегии).
	want := map[int]int{1: 44, 2: 44, 3: 44, 4: 28, 5: 32, 6: 32, 7: 20, 8: 44, 9: 8, 10: 20}
	for id, max := range want {
		p := profileOf(t, st, id)
		if int(p["max_strategy"].(float64)) != max {
			t.Errorf("profile %d max_strategy = %v, want %d", id, p["max_strategy"], max)
		}
		if p["current_lock"] != "auto" {
			t.Errorf("profile %d lock = %v", id, p["current_lock"])
		}
	}
	if p := profileOf(t, st, 7); p["is_udp_games"] != true || p["udp_games_enabled"] != false {
		t.Errorf("profile 7 = %v", p)
	}
	if p := profileOf(t, st, 8); p["is_fallback"] != true || p["fallback_enabled"] != false {
		t.Errorf("profile 8 = %v", p)
	}
	if p := profileOf(t, st, 10); p["is_dns_desync"] != true || p["dns_desync_enabled"] != false {
		t.Errorf("profile 10 = %v", p)
	}
}

func TestSetLockAndClearLock(t *testing.T) {
	e := newEnv(t, envOpts{})

	resp, out := e.post("/cgi-bin/set-lock.cgi", "profile", "1", "strategy", "7")
	wantOK(t, resp, out)
	if out["lock"] != "7" || out["previous"] != "auto" || out["changed"] != true {
		t.Errorf("set-lock meta = %v", out)
	}
	// Профиль 1 = tls + http.
	for _, proto := range []string{"tls", "http"} {
		st, err := e.srv.Store().GetLock("default", "1", proto)
		if err != nil || st != 7 {
			t.Errorf("lock 1/%s = %v err=%v", proto, st, err)
		}
	}
	data, err := os.ReadFile(e.app.Paths.LockedTSV)
	if err != nil || !strings.Contains(string(data), "1\ttls\t7") {
		t.Errorf("locked.tsv = %q err=%v", data, err)
	}
	_, st := e.get("/cgi-bin/status.cgi")
	if p := profileOf(t, st, 1); p["current_lock"] != "7" || p["lock_source"] != "default" {
		t.Errorf("profile 1 = %v", p)
	}
	if st["strategy_locks_status"] != "Есть" {
		t.Errorf("strategy_locks_status = %v", st["strategy_locks_status"])
	}

	// Выключение профиля: 0.
	resp, out = e.post("/cgi-bin/set-lock.cgi", "profile", "2", "strategy", "0")
	wantOK(t, resp, out)
	_, st = e.get("/cgi-bin/status.cgi")
	if p := profileOf(t, st, 2); p["current_lock"] != "0" {
		t.Errorf("profile 2 = %v", p)
	}

	// Fallback-профили пишутся в locked.manual.tsv (сначала включаем режим).
	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "fallback_state", "value", "1")
	wantOK(t, resp, out)
	resp, out = e.post("/cgi-bin/set-lock.cgi", "profile", "8", "strategy", "5")
	wantOK(t, resp, out)
	if data, _ := os.ReadFile(e.app.Paths.LockedManualTSV); !strings.Contains(string(data), "8\ttls\t5") {
		t.Errorf("locked.manual.tsv = %q", data)
	}

	// Сброс.
	resp, out = e.post("/cgi-bin/clear-lock.cgi", "profile", "1")
	wantOK(t, resp, out)
	_, st = e.get("/cgi-bin/status.cgi")
	if p := profileOf(t, st, 1); p["current_lock"] != "auto" {
		t.Errorf("после clear: %v", p)
	}
	if got, _ := e.srv.Store().GetLock("default", "1", "http"); !got.IsAuto() {
		t.Errorf("http lock = %v", got)
	}
}

func TestSetLockValidation(t *testing.T) {
	e := newEnv(t, envOpts{})
	tests := []struct {
		name   string
		kv     []string
		status int
		msg    string
	}{
		{"bad profile", []string{"profile", "x", "strategy", "1"}, 400, "Некорректный профиль"},
		{"profile zero", []string{"profile", "0", "strategy", "1"}, 400, "Некорректный профиль"},
		{"missing profile", []string{"strategy", "1"}, 400, "Некорректный профиль"},
		{"bad strategy", []string{"profile", "1", "strategy", "-1"}, 400, "Некорректная стратегия"},
		{"missing strategy", []string{"profile", "1"}, 400, "Некорректная стратегия"},
		{"out of range", []string{"profile", "4", "strategy", "29"}, 400, "Стратегия вне диапазона"},
		{"out of range 9", []string{"profile", "9", "strategy", "9"}, 400, "Стратегия вне диапазона"},
		{"fallback off", []string{"profile", "8", "strategy", "1"}, 409, "Сначала включите безразборный режим"},
		{"udp games off", []string{"profile", "7", "strategy", "1"}, 409, "Сначала включите игровой UDP"},
		{"unknown profile", []string{"profile", "11", "strategy", "0"}, 400, "Не удалось определить протокол профиля"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := e.post("/cgi-bin/set-lock.cgi", tc.kv...)
			wantError(t, resp, out, tc.status, tc.msg)
		})
	}
	resp, out := e.post("/cgi-bin/clear-lock.cgi", "profile", "zz")
	wantError(t, resp, out, 400, "Некорректный профиль")
	resp, out = e.post("/cgi-bin/clear-lock.cgi", "profile", "11")
	wantError(t, resp, out, 400, "Не удалось определить протокол профиля")
	// clear-lock тоже гейтится по режиму профиля.
	resp, out = e.post("/cgi-bin/clear-lock.cgi", "profile", "8")
	wantError(t, resp, out, 409, "Сначала включите безразборный режим")
	resp, out = e.post("/cgi-bin/clear-lock.cgi", "profile", "7")
	wantError(t, resp, out, 409, "Сначала включите игровой UDP")
	// Ничего не должно быть записано.
	if e.srv.Store().HasLocks() {
		t.Error("после ошибок записаны локи")
	}
}

func TestSetLockAutoRotationConflict(t *testing.T) {
	e := newEnv(t, envOpts{})
	resp, out := e.post("/cgi-bin/settings.cgi", "setting", "auto_mode_state", "value", "1")
	wantOK(t, resp, out)

	want := "Профиль 3 управляется авторотацией TCP/HTTP. Сначала выключите авторотацию."
	resp, out = e.post("/cgi-bin/set-lock.cgi", "profile", "3", "strategy", "1")
	wantError(t, resp, out, http.StatusConflict, want)
	resp, out = e.post("/cgi-bin/clear-lock.cgi", "profile", "3")
	wantError(t, resp, out, http.StatusConflict, want)
	// Профили вне 1–4 не затронуты.
	resp, out = e.post("/cgi-bin/set-lock.cgi", "profile", "5", "strategy", "1")
	wantOK(t, resp, out)
}

func TestSetLockVoiceProfileRestartsAndEditsPorts(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.svc.running = true

	resp, out := e.post("/cgi-bin/set-lock.cgi", "profile", "6", "strategy", "0")
	wantOK(t, resp, out)
	if line := lineWith(e.zapretConfig(), "NFQWS2_PORTS_UDP="); strings.Contains(line, "50000-50099") {
		t.Errorf("голосовые порты не убраны: %s", line)
	}
	if calls := e.svc.called(); len(calls) != 1 || calls[0] != "restart" {
		t.Errorf("service calls = %v", calls)
	}

	resp, out = e.post("/cgi-bin/set-lock.cgi", "profile", "6", "strategy", "3")
	wantOK(t, resp, out)
	if !strings.Contains(lineWith(e.zapretConfig(), "NFQWS2_PORTS_UDP="), "50000-50099") {
		t.Errorf("голосовые порты не возвращены: %s", lineWith(e.zapretConfig(), "NFQWS2_PORTS_UDP="))
	}
	if st, _ := e.srv.Store().GetLock("default", "6", "udp"); st != orchestra.Strategy(3) {
		t.Errorf("lock 6/udp = %v", st)
	}
}

func lineWith(text, prefix string) string {
	for _, ln := range strings.Split(text, "\n") {
		if strings.HasPrefix(ln, prefix) {
			return ln
		}
	}
	return ""
}

func TestServiceEndpoint(t *testing.T) {
	e := newEnv(t, envOpts{})
	for _, action := range []string{"start", "stop", "restart"} {
		resp, out := e.post("/cgi-bin/service.cgi", "action", action)
		wantOK(t, resp, out)
	}
	if got := strings.Join(e.svc.called(), ","); got != "start,stop,restart" {
		t.Errorf("calls = %s", got)
	}

	resp, out := e.post("/cgi-bin/service.cgi", "action", "reload")
	wantError(t, resp, out, 400, "Некорректное действие")
	resp, out = e.post("/cgi-bin/service.cgi")
	wantError(t, resp, out, 400, "Некорректное действие")

	e.svc.failOn = "restart"
	resp, out = e.post("/cgi-bin/service.cgi", "action", "restart")
	wantError(t, resp, out, 500, "Не удалось выполнить команду zapret2")
	if out["detail"] == nil {
		t.Error("нет detail")
	}
	// GET не допускается.
	resp, out = e.get("/cgi-bin/service.cgi?action=start")
	wantError(t, resp, out, 405, "Метод не поддерживается")
}

func TestServiceStartBlockedOnApplyFail(t *testing.T) {
	e := newEnv(t, envOpts{})
	// Битый OPT: shell-metachar → Apply/Validate fail → start блокируется.
	bad := "QNUM=200\nNFQWS2_OPT=\"\n--new\n--filter-tcp=443;evil\n\"\n"
	if err := os.WriteFile(e.app.Paths.ZapretConfig, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	before := e.svc.called()
	resp, out := e.post("/cgi-bin/service.cgi", "action", "start")
	wantError(t, resp, out, 500, "Не удалось применить конфиг zapret2")
	if out["runtime_apply_error"] == nil {
		t.Error("expected runtime_apply_error")
	}
	if got := e.svc.called(); len(got) != len(before) {
		t.Fatalf("Start must not run after apply fail: %v", got)
	}
}

func TestStatusReflectsServiceState(t *testing.T) {
	e := newEnv(t, envOpts{})
	_, st := e.get("/cgi-bin/status.cgi")
	if st["zapret2_running"] != false {
		t.Fatalf("running = %v", st["zapret2_running"])
	}
	e.post("/cgi-bin/service.cgi", "action", "start")
	_, st = e.get("/cgi-bin/status.cgi")
	if st["zapret2_running"] != true {
		t.Fatalf("running = %v", st["zapret2_running"])
	}
}

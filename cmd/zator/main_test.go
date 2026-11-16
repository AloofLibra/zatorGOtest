package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/app"
	"github.com/AloofLibra/zator/internal/orchestra"
	"github.com/AloofLibra/zator/internal/platform"
)

func runCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func setupEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ZATOR_ROOT", filepath.Join(t.TempDir(), "zator"))
	t.Setenv("ZAPRET2_ROOT", filepath.Join(t.TempDir(), "zapret2"))
	if bundle := os.Getenv("ZATOR_TEST_BUNDLE_DIR"); bundle != "" {
		t.Setenv("Z2R_OFFLINE", "1")
		t.Setenv("ZATOR_BUNDLE_DIR", bundle)
	}
}

func TestVersion(t *testing.T) {
	code, out, _ := runCmd(t, "version")
	if code != 0 || !strings.HasPrefix(out, "zator ") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestWebUIHostDefaultsByPlatformAndHonorsOverride(t *testing.T) {
	tests := []struct {
		name, override string
		platform       platform.Platform
		want           string
	}{
		{name: "VPS loops back", platform: platform.Platform{Kind: platform.KindVPS}, want: "127.0.0.1"},
		{name: "Alpine listens on all interfaces", platform: platform.Platform{Kind: platform.KindVPS, OSID: "alpine"}, want: ""},
		{name: "OpenWrt uses firewall", platform: platform.Platform{Kind: platform.KindOpenWRT}, want: ""},
		{name: "Entware uses firewall", platform: platform.Platform{Kind: platform.KindEntware}, want: ""},
		{name: "explicit public bind", override: "0.0.0.0", platform: platform.Platform{Kind: platform.KindVPS}, want: "0.0.0.0"},
		{name: "explicit loopback", override: "127.0.0.1", platform: platform.Platform{Kind: platform.KindOpenWRT}, want: "127.0.0.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := webUIHost(tc.override, tc.platform); got != tc.want {
				t.Fatalf("webUIHost(%q, %+v) = %q, want %q", tc.override, tc.platform, got, tc.want)
			}
		})
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errOut := runCmd(t, "bogus")
	if code != 2 || !strings.Contains(errOut, "неизвестная команда") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestServiceWarningsAndValidation(t *testing.T) {
	setupEnv(t)
	if code, out, _ := runCmd(t, "service", "warnings"); code != 0 || !strings.Contains(out, "ошибок служб нет") {
		t.Fatalf("warnings: code=%d out=%q", code, out)
	}
	if code, _, _ := runCmd(t, "service", "restart", "unknown"); code != 2 {
		t.Fatalf("invalid service code=%d", code)
	}
	if code, _, _ := runCmd(t, "wssize-hook", "bogus"); code != 2 {
		t.Fatalf("invalid WSSIZE hook code=%d", code)
	}
	if code, _, _ := runCmd(t, "rollback"); code != 1 {
		t.Fatalf("rollback without snapshot code=%d", code)
	}
}

func TestErrorsCommandShowsServiceWarningsAndValidatesArgs(t *testing.T) {
	setupEnv(t)
	a := app.New()
	a.WriteServiceWarnings([]string{"nfqws2 не запущен"})
	if code, out, _ := runCmd(t, "errors"); code != 0 || !strings.Contains(out, "nfqws2 не запущен") || !strings.Contains(out, "Журнал ошибок nfqws2 недоступен") {
		t.Fatalf("errors: code=%d out=%q", code, out)
	}
	if code, _, errOut := runCmd(t, "errors", "unexpected"); code != 2 || !strings.Contains(errOut, "использование: zator errors") {
		t.Fatalf("invalid errors args: code=%d err=%q", code, errOut)
	}
}

func TestWriteErrorLogBoundsOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nfqws2_1.err")
	content := strings.Repeat("x", maxCLIErrorLogBytes+7)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if !writeErrorLog([]string{path}, &out) {
		t.Fatal("expected error log to be found")
	}
	if got := len(out.Bytes()); got != len("Журнал ошибок nfqws2 ("+path+"):\n")+maxCLIErrorLogBytes+1 {
		t.Fatalf("output size=%d; want header + %d bytes", got, maxCLIErrorLogBytes)
	}
	if !strings.HasSuffix(out.String(), strings.Repeat("x", maxCLIErrorLogBytes)+"\n") {
		t.Fatal("output did not retain the tail of the log")
	}
}

func TestLocksClearCreatesBackup(t *testing.T) {
	setupEnv(t)
	if code, _, errOut := runCmd(t, "install", "-zapret=false", "-skip-service", "-no-auth"); code != 0 {
		t.Fatalf("install: code=%d err=%s", code, errOut)
	}
	a := app.New()
	if err := os.MkdirAll(a.Paths.ZapretRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.Paths.ZapretConfig, []byte("NFQWS2_ENABLE=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := orchestra.NewStore(a.Paths.StateOrchestraDir())
	if err := store.SetLock(orchestra.ScopeDefault, "1", "tls", 5); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runCmd(t, "locks", "clear"); code != 1 {
		t.Fatalf("clear without confirmation code=%d", code)
	}
	if !store.HasLocks() {
		t.Fatal("missing -y unexpectedly cleared locks")
	}
	if code, out, errOut := runCmd(t, "locks", "clear", "-y"); code != 0 || !strings.Contains(out, "backup:") {
		t.Fatalf("clear: code=%d out=%q err=%q", code, out, errOut)
	} else {
		if store.HasLocks() {
			t.Fatal("locks remain after clear")
		}
		name := strings.TrimSpace(strings.SplitN(out, "backup:", 2)[1])
		if code, restoreOut, restoreErr := runCmd(t, "restore", name); code != 0 {
			t.Fatalf("restore %s: code=%d out=%q err=%q", name, code, restoreOut, restoreErr)
		}
	}
	if !store.HasLocks() {
		t.Fatal("smart restore did not restore strategy locks")
	}
}

func TestDefaultNotInstalled(t *testing.T) {
	setupEnv(t)
	code, out, _ := runCmd(t)
	if code != 0 || !strings.Contains(out, "zator install") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestInstallStatusUninstall(t *testing.T) {
	setupEnv(t)
	if code, _, errOut := runCmd(t, "install", "-port", "18080", "-zapret=false", "-skip-service", "-password", "test-pass"); code != 0 {
		t.Fatalf("install: %d %s", code, errOut)
	}
	code, out, _ := runCmd(t)
	if code != 0 || !strings.Contains(out, ":18080/") {
		t.Fatalf("default after install: %d %q", code, out)
	}
	if code, out, _ := runCmd(t, "status"); code != 0 || !strings.Contains(out, "Установлен:  true") || !strings.Contains(out, "password=true") {
		t.Fatalf("status: %d %q", code, out)
	}
	if code, _, _ := runCmd(t, "uninstall"); code != 1 {
		t.Fatalf("uninstall без -y должен вернуть 1, получен %d", code)
	}
	if code, _, errOut := runCmd(t, "uninstall", "-y"); code != 0 {
		t.Fatalf("uninstall: %d %s", code, errOut)
	}
	cfgPath := filepath.Join(os.Getenv("ZATOR_ROOT"), "config", "zator.json")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config должен остаться после uninstall: %v", err)
	}
	if code, _, errOut := runCmd(t, "uninstall", "-y", "-purge"); code != 0 {
		t.Fatalf("purge: %d %s", code, errOut)
	}
	if _, err := os.Stat(os.Getenv("ZATOR_ROOT")); !os.IsNotExist(err) {
		t.Fatalf("purge должен удалить корень: %v", err)
	}
}

func TestInstallInvalidSource(t *testing.T) {
	setupEnv(t)
	if code, _, _ := runCmd(t, "install", "-source", "nope", "-password", "x"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

func TestInstallNoAuth(t *testing.T) {
	setupEnv(t)
	if code, _, errOut := runCmd(t, "install", "-zapret=false", "-skip-service", "-no-auth"); code != 0 {
		t.Fatalf("install: %d %s", code, errOut)
	}
	if code, out, _ := runCmd(t, "auth", "status"); code != 0 || !strings.Contains(out, "auth_enabled:  false") {
		t.Fatalf("auth status: %d %q", code, out)
	}
}

func TestWebHandlerServesAPIAndStatic(t *testing.T) {
	setupEnv(t)
	if code, _, errOut := runCmd(t, "install", "-port", "18081", "-zapret=false", "-skip-service", "-no-auth"); code != 0 {
		t.Fatalf("install: %d %s", code, errOut)
	}
	a, err := app.Load()
	if err != nil {
		t.Fatal(err)
	}
	if a.Config.AuthEnabled || a.Config.HasPassword() {
		t.Fatalf("no-auth install: %+v", a.Config)
	}
	var warn bytes.Buffer
	h, err := newWebHandler(a, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn.String(), "без проверки доступа") {
		t.Errorf("ожидалось сообщение об открытом доступе: %q", warn.String())
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	get := func(path string) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}

	resp, body := get("/cgi-bin/status.cgi")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("status.cgi: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var st map[string]any
	if err := json.Unmarshal(body, &st); err != nil || st["profiles"] == nil {
		t.Errorf("status body: %v %s", err, body)
	}
	if resp, body := get("/"); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<html") {
		t.Errorf("статика: %d", resp.StatusCode)
	}
	if resp, _ := get("/api/scopes"); resp.StatusCode != http.StatusOK {
		t.Errorf("/api/scopes: %d", resp.StatusCode)
	}
}

func TestWebHandlerWarnsWithoutAuth(t *testing.T) {
	setupEnv(t)
	a, err := app.Load()
	if err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	if _, err := newWebHandler(a, &warn); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn.String(), "без проверки доступа") {
		t.Errorf("нет сообщения об открытом доступе: %q", warn.String())
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/app"
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
}

func TestVersion(t *testing.T) {
	code, out, _ := runCmd(t, "version")
	if code != 0 || !strings.HasPrefix(out, "zator ") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errOut := runCmd(t, "bogus")
	if code != 2 || !strings.Contains(errOut, "неизвестная команда") {
		t.Fatalf("code=%d err=%q", code, errOut)
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
	if code, _, errOut := runCmd(t, "install", "-port", "18080", "-zapret=false"); code != 0 {
		t.Fatalf("install: %d %s", code, errOut)
	}
	code, out, _ := runCmd(t)
	if code != 0 || !strings.Contains(out, ":18080/") {
		t.Fatalf("default after install: %d %q", code, out)
	}
	if code, out, _ := runCmd(t, "status"); code != 0 || !strings.Contains(out, "Установлен:  true") {
		t.Fatalf("status: %d %q", code, out)
	}
	if code, _, _ := runCmd(t, "uninstall"); code != 1 {
		t.Fatalf("uninstall без -y должен вернуть 1, получен %d", code)
	}
	if code, _, errOut := runCmd(t, "uninstall", "-y"); code != 0 {
		t.Fatalf("uninstall: %d %s", code, errOut)
	}
}

func TestInstallInvalidSource(t *testing.T) {
	setupEnv(t)
	if code, _, _ := runCmd(t, "install", "-source", "nope"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

func TestWebHandlerServesAPIAndStatic(t *testing.T) {
	setupEnv(t)
	if code, _, errOut := runCmd(t, "install", "-port", "18081", "-zapret=false"); code != 0 {
		t.Fatalf("install: %d %s", code, errOut)
	}
	a, err := app.Load()
	if err != nil {
		t.Fatal(err)
	}
	if a.Config.Token == "" {
		t.Fatal("install должен сгенерировать токен")
	}
	var warn bytes.Buffer
	h, err := newWebHandler(a, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if warn.Len() != 0 {
		t.Errorf("предупреждение при заданном токене: %q", warn.String())
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	get := func(path, token string) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}

	if resp, _ := get("/cgi-bin/status.cgi", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("без токена: %d", resp.StatusCode)
	}
	resp, body := get("/cgi-bin/status.cgi", a.Config.Token)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("status.cgi: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var st map[string]any
	if err := json.Unmarshal(body, &st); err != nil || st["profiles"] == nil {
		t.Errorf("status body: %v %s", err, body)
	}
	if resp, body := get("/", a.Config.Token); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<html") {
		t.Errorf("статика: %d", resp.StatusCode)
	}
	if resp, _ := get("/api/scopes", a.Config.Token); resp.StatusCode != http.StatusOK {
		t.Errorf("/api/scopes: %d", resp.StatusCode)
	}
}

func TestWebHandlerWarnsWithoutToken(t *testing.T) {
	setupEnv(t)
	a, err := app.Load()
	if err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	if _, err := newWebHandler(a, &warn); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn.String(), "токен не задан") {
		t.Errorf("нет предупреждения: %q", warn.String())
	}
}

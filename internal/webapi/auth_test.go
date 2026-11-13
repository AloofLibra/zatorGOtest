package webapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestLoginLogoutAndMiddleware(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.app.Config.AuthEnabled = false
	resp, out := e.get("/api/status")
	wantStatus(t, resp, http.StatusOK)
	if out["profiles"] == nil {
		t.Fatalf("open status: %v", out)
	}

	if err := e.app.SetPassword("s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Save(); err != nil {
		t.Fatal(err)
	}

	resp, out = e.get("/api/status")
	wantError(t, resp, out, http.StatusUnauthorized, "Требуется авторизация")

	resp, body := e.do(http.MethodGet, "/", nil, map[string]string{"Accept": "text/html"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<html") {
		t.Fatalf("статика без auth: %d %s", resp.StatusCode, body[:min(80, len(body))])
	}

	resp, out = e.post("/api/login", "password", "wrong")
	wantError(t, resp, out, http.StatusUnauthorized, "Неверный пароль")

	form := url.Values{"password": {"s3cret"}}
	resp, raw := e.do(http.MethodPost, "/api/login", form, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d %s", resp.StatusCode, raw)
	}
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == CookieName {
			cookie = c.Value
		}
	}
	if cookie == "" || cookie != e.app.Config.SessionToken {
		t.Fatalf("cookie=%q session=%q", cookie, e.app.Config.SessionToken)
	}

	resp, out = doJSONAuth(e, http.MethodGet, "/api/status", nil, map[string]string{
		"Cookie": CookieName + "=" + cookie,
	})
	wantStatus(t, resp, http.StatusOK)
	if out["profiles"] == nil {
		t.Fatalf("authed status: %v", out)
	}

	resp, _ = e.do(http.MethodPost, "/api/logout", url.Values{}, map[string]string{
		"Cookie": CookieName + "=" + cookie,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	resp, out = doJSONAuth(e, http.MethodGet, "/api/status", nil, map[string]string{
		"Cookie": CookieName + "=" + cookie,
	})
	wantError(t, resp, out, http.StatusUnauthorized, "Требуется авторизация")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func doJSONAuth(e *env, method, path string, form url.Values, hdr map[string]string) (*http.Response, map[string]any) {
	e.t.Helper()
	resp, body := e.do(method, path, form, hdr)
	out := map[string]any{}
	if err := json.Unmarshal(body, &out); err != nil {
		e.t.Fatalf("%s %s: не JSON: %v\n%s", method, path, err, body)
	}
	return resp, out
}

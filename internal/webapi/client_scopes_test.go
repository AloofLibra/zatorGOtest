package webapi

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestClientScopeMappingsCRUD(t *testing.T) {
	e := newEnv(t, envOpts{})
	path := filepath.Join(e.app.Paths.ExtraStratsDir, "cache", "client_scope.tsv")
	if err := os.WriteFile(path, []byte("# keep\nmark:4\t192.0.2.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, out := e.get("/api/client-scopes")
	wantStatus(t, resp, http.StatusOK)
	rows := out["mappings"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["scope"] != "mark:4" {
		t.Fatalf("initial mappings = %v", rows)
	}

	resp, out = e.post("/api/client-scopes", "action", "add", "ip", "192.0.2.10", "scope", "mark:5")
	wantOK(t, resp, out)
	resp, out = e.post("/api/client-scopes", "action", "add", "ip", "192.0.2.10", "scope", "mark:6")
	wantOK(t, resp, out)
	resp, out = e.get("/api/client-scopes")
	wantStatus(t, resp, http.StatusOK)
	rows = out["mappings"].([]any)
	if len(rows) != 2 || rows[1].(map[string]any)["scope"] != "mark:6" {
		t.Fatalf("updated mappings = %v", rows)
	}

	resp, out = e.post("/api/client-scopes", "action", "add", "ip", "2001:db8::10", "scope", "mark:7")
	wantOK(t, resp, out)
	resp, out = e.post("/api/client-scopes", "action", "remove", "ip", "192.0.2.10")
	wantOK(t, resp, out)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "# keep\nmark:4\t192.0.2.4\nmark:7\t2001:db8::10\n" {
		t.Fatalf("client_scope.tsv = %q", raw)
	}
}

func TestClientScopeMappingsValidationAndEnable(t *testing.T) {
	e := newEnv(t, envOpts{})
	resp, out := e.post("/api/client-scopes", "action", "add", "ip", "not-an-ip", "scope", "mark:1")
	wantError(t, resp, out, http.StatusBadRequest, "Некорректный IP-адрес")
	resp, out = e.post("/api/settings", "setting", "client_scope", "client_scope_enable", "1", "client_scope_mark_mask", "0xff00", "client_scope_mark_shift", "8", "client_scope_mark_max", "255")
	wantOK(t, resp, out)
	if out["enabled"] != false || out["warning"] == "" {
		t.Fatalf("enabled without map = %v", out)
	}
	resp, out = e.post("/api/client-scopes", "action", "add", "ip", "192.0.2.10", "scope", "mark:1")
	wantOK(t, resp, out)
	resp, out = e.post("/api/settings", "setting", "client_scope", "client_scope_enable", "1", "client_scope_mark_mask", "0xff00", "client_scope_mark_shift", "8", "client_scope_mark_max", "255")
	wantOK(t, resp, out)
	if out["enabled"] != true {
		t.Fatalf("enabled with map = %v", out)
	}
}

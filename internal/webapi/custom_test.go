package webapi

import (
	"os"
	"strings"
	"testing"
)

func TestCustomStratsEndpoints(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.svc.running = true

	// Initial GET returns all 5 blocks and empty strategies slice.
	resp, out := e.get("/api/custom")
	wantStatus(t, resp, 200)
	blocks := out["blocks"].([]any)
	if len(blocks) != 5 {
		t.Fatalf("blocks len = %d, want 5", len(blocks))
	}
	if strats := out["strategies"].([]any); len(strats) != 0 {
		t.Fatalf("expected 0 initial strategies, got %v", strats)
	}

	// Add invalid strategy -> 400.
	resp, out = e.post("/cgi-bin/custom.cgi", "action", "add", "block", "tpl", "value", "fake:blob=unknown_blob")
	wantStatus(t, resp, 400)

	// Add valid strategy via single command input (nfqws2 ... :strategy=N stripped, strict blob check kept).
	resp, out = e.post("/api/custom", "action", "add", "block", "tpl", "value", "--lua-desync=fake:blob=maxru:repeats=2:strategy=1", "comment", "my test")
	wantOK(t, resp, out)
	if out["apply_required"] != true || out["id"] != float64(1) {
		t.Fatalf("add out = %v", out)
	}

	// Import blockcheck2 summary + zapret1 rejection.
	impText := "curl_test_https_tls12 ipv4 example.org : nfqws2 --blob=tlsgoogle:@/tmp/t.bin --lua-desync=fake:blob=tlsgoogle:repeats=3:strategy=9\n" +
		"curl_test_https_tls12 ipv4 old.org : nfqws --dpi-desync=fake\n"
	resp, out = e.post("/api/custom", "action", "import", "block", "tpl", "text", impText)
	wantOK(t, resp, out)
	if out["added"] != float64(1) || out["failed"] != float64(1) {
		t.Fatalf("import out = %v", out)
	}

	// Toggle enabled -> disable id 1.
	resp, out = e.post("/api/custom", "action", "enable", "id", "1", "value", "0")
	wantOK(t, resp, out)

	// Edit id 2.
	resp, out = e.post("/api/custom", "action", "edit", "id", "2", "block", "tpl", "value", "fake:blob=maxru:repeats=4", "comment", "updated")
	wantOK(t, resp, out)

	// Apply injects enabled strategy into live zapret config and restarts running zapret2.
	resp, out = e.post("/api/custom", "action", "apply")
	wantOK(t, resp, out)
	if out["restarted"] != true || out["apply_required"] != false {
		t.Fatalf("apply out = %v", out)
	}
	rawCfg, _ := os.ReadFile(e.app.Paths.ZapretConfig)
	if !strings.Contains(string(rawCfg), "--lua-desync=fake:blob=maxru:repeats=4:strategy=45") && !strings.Contains(string(rawCfg), "--lua-desync=fake:blob=maxru:repeats=4:strategy=") {
		t.Fatalf("custom strategy not injected into config:\n%s", rawCfg)
	}

	// Verify assigned field updated on GET.
	resp, out = e.get("/api/custom?block=tpl")
	wantStatus(t, resp, 200)
	items := out["strategies"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %v", items)
	}
	second := items[1].(map[string]any)
	if second["assigned"] == "" {
		t.Errorf("expected assigned strategy number on item 2, got %v", second)
	}

	// Delete id 1 and re-apply.
	resp, out = e.post("/api/custom", "action", "delete", "id", "1")
	wantOK(t, resp, out)
	resp, out = e.post("/api/custom", "action", "apply", "restart", "0")
	wantOK(t, resp, out)
}

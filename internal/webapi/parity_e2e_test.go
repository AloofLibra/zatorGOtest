package webapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/orchestra"
	"github.com/AloofLibra/zator/internal/uiembed"
)

func TestParity160EndToEndWorkflow(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.svc.running = true

	// Seed a valid QUIC blob candidate and a project SNI set.
	if err := os.WriteFile(filepath.Join(e.app.Paths.FakeDir, "quic_custom.bin"), []byte("QUICBLOB"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(e.app.Paths.SNIZatorTxt()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.app.Paths.SNIZatorTxt(), []byte("# set: google\nwww.google.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Upload custom blob binary via raw POST /api/blobs?action=upload.
	resp, out := e.uploadBinary("/api/blobs?action=upload&name=e2e-fake.bin", []byte("E2EFAKEBLOB"))
	wantOK(t, resp, out)
	if out["created"] != float64(1) {
		t.Fatalf("expected created=1, got %v", out)
	}

	// 2. Add single custom strategy + batch import from blockcheck2, then apply.
	resp, out = e.post("/api/custom", "action", "add", "block", "tpl", "value", "fake:blob=e2e_fake:repeats=2", "comment", "first")
	wantOK(t, resp, out)
	impText := strings.Join([]string{
		"curl_test_https_tls12 ipv4 example.org : nfqws2 --blob=tlsgoogle:@/tmp/t.bin --lua-desync=fake:blob=tlsgoogle:repeats=3:strategy=99",
		"curl_test_https_tls12 ipv4 old.org : nfqws --dpi-desync=fake",
	}, "\n")
	resp, out = e.post("/api/custom", "action", "import", "block", "tpl", "text", impText)
	wantOK(t, resp, out)
	if out["added"] != float64(1) || out["failed"] != float64(1) {
		t.Fatalf("import mismatch: %v", out)
	}
	if notes, _ := out["notes"].([]any); len(notes) == 0 {
		t.Fatalf("expected blob remap note in import output: %v", out)
	}

	resp, out = e.post("/api/custom", "action", "apply")
	wantOK(t, resp, out)
	if out["restarted"] != true || out["apply_required"] != false {
		t.Fatalf("apply output = %v", out)
	}
	cfgText := e.zapretConfig()
	if !strings.Contains(cfgText, "--blob=e2e_fake:@") || !strings.Contains(cfgText, "--lua-desync=fake:blob=e2e_fake:repeats=2:strategy=") {
		t.Fatalf("applied custom blob/strategy missing in config:\n%s", cfgText)
	}

	// 3. Lock profile 1 to the 2nd custom strategy assigned number, delete 1st, re-apply -> lock must remap.
	resp, listOut := e.get("/api/custom?block=tpl")
	wantStatus(t, resp, http.StatusOK)
	items := listOut["strategies"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 custom strategies, got %v", items)
	}
	assigned2Str, _ := items[1].(map[string]any)["assigned"].(string)
	assigned2, err := strconv.Atoi(assigned2Str)
	if err != nil || assigned2 <= 40 {
		t.Fatalf("unexpected assigned2=%q", assigned2Str)
	}

	resp, out = e.post("/api/set-lock", "profile", "1", "strategy", assigned2Str)
	wantOK(t, resp, out)
	resp, out = e.post("/api/custom", "action", "delete", "id", "1")
	wantOK(t, resp, out)
	resp, out = e.post("/api/custom", "action", "apply")
	wantOK(t, resp, out)
	if out["moved"] == nil || int(out["moved"].(float64)) < 1 {
		t.Fatalf("expected moved >= 1 lock remap, got %v", out)
	}
	wantNew := orchestra.Strategy(assigned2 - 1)
	if got, gErr := e.srv.Store().GetLock(orchestra.ScopeDefault, "1", "tls"); gErr != nil || got != wantNew {
		t.Fatalf("remapped profile 1 lock = %v (%v), want %v", got, gErr, wantNew)
	}

	// 4. Profile 5 QUIC blob validation + persistence into blob_override & blob_files.
	resp, _ = e.post("/api/settings", "setting", "tls_blob_profile", "profile", "5", "value", "fake_default_tls")
	wantStatus(t, resp, http.StatusBadRequest)
	resp, out = e.post("/api/settings", "setting", "tls_blob_profile", "profile", "5", "value", "quic_custom.bin")
	wantOK(t, resp, out)

	// 5. Simulate config upgrade from config.default and re-apply 1.6.0 overlays.
	defCfg, err := uiembed.ConfigDefault()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.app.Paths.ZapretConfig, defCfg, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(e.app.Paths.ZapretConfig)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := e.app.ReapplyConfigOverlays(cfg)
	if err != nil || !changed {
		t.Fatalf("ReapplyConfigOverlays changed=%v err=%v", changed, err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	upgraded := e.zapretConfig()
	for _, wantSub := range []string{
		"sni-pool.lua",
		"--blob=z2r_prof_5:@",
		"quic_custom.bin",
		"--blob=e2e_fake:@",
		"#Z2R_CUSTOM_TPL_BEGIN",
	} {
		if !strings.Contains(upgraded, wantSub) {
			t.Errorf("upgraded config missing %q:\n%s", wantSub, upgraded)
		}
	}

	// 6. SNI set write, assign, in-use delete protection, clear, and delete.
	resp, out = e.post("/api/blobs", "action", "sni_set_write", "name", "mye2e", "list", "cdn1.example.org\ncdn2.example.org")
	wantOK(t, resp, out)
	resp, out = e.post("/api/blobs", "action", "sni_pool_assign", "profile", "1", "value", "mye2e")
	wantOK(t, resp, out)
	resp, _ = e.post("/api/blobs", "action", "sni_set_delete", "name", "mye2e")
	wantStatus(t, resp, http.StatusBadRequest)
	resp, out = e.post("/api/blobs", "action", "sni_pool_clear", "profile", "1")
	wantOK(t, resp, out)
	resp, out = e.post("/api/blobs", "action", "sni_set_delete", "name", "mye2e")
	wantOK(t, resp, out)

	// 7. Verify nfqws2_errors suppressed_conntrack field & clean config_dirty=false.
	resp, errOut := e.get("/api/settings?setting=nfqws2_errors")
	wantStatus(t, resp, http.StatusOK)
	if _, ok := errOut["suppressed_conntrack"]; !ok {
		t.Errorf("expected suppressed_conntrack field in nfqws2_errors payload: %v", errOut)
	}
	resp, blobsOut := e.get("/api/blobs")
	wantStatus(t, resp, http.StatusOK)
	if blobsOut["config_dirty"] != false {
		t.Errorf("expected config_dirty=false after overlays reapply, got %v", blobsOut["config_dirty"])
	}
}

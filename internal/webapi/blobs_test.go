package webapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/customblobs"
)

func (e *env) uploadBinary(path string, data []byte) (*http.Response, map[string]any) {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.ts.URL+path, bytes.NewReader(data))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := e.ts.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		e.t.Fatalf("decode response: %v", err)
	}
	return resp, out
}

func TestCustomBlobsAndSNIEndpoints(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.svc.running = true

	// Path traversal & >256 KiB upload rejected cleanly.
	resp, out := e.uploadBinary("/api/blobs?action=upload&name=../evil.bin", []byte("x"))
	wantStatus(t, resp, 400)
	resp, out = e.uploadBinary("/api/blobs?action=upload&name=big.bin", make([]byte, customblobs.MaxBlobSize+1))
	wantStatus(t, resp, http.StatusRequestEntityTooLarge)

	// Valid upload creates file and declaration.
	resp, out = e.uploadBinary("/api/blobs?action=upload&name=my-custom.bin", []byte("CUSTOMBLOB"))
	wantOK(t, resp, out)
	if out["apply_required"] != true || out["created"] != float64(1) {
		t.Fatalf("upload out = %v", out)
	}

	// Add a custom strategy referencing my_custom, verify config_dirty and apply.
	resp, out = e.post("/api/custom", "action", "add", "block", "tpl", "value", "fake:blob=my_custom:repeats=2")
	wantOK(t, resp, out)

	resp, out = e.get("/api/blobs")
	wantStatus(t, resp, 200)
	if out["config_dirty"] != true {
		t.Errorf("expected config_dirty=true before apply, got %v", out["config_dirty"])
	}
	blobs := out["blobs"].([]any)
	if len(blobs) != 1 || blobs[0].(map[string]any)["refs"] != float64(1) {
		t.Fatalf("unexpected blobs list: %v", blobs)
	}

	// Delete blocked while referenced by custom strategy.
	resp, out = e.post("/cgi-bin/blobs.cgi", "action", "delete", "id", "1")
	wantStatus(t, resp, 400)

	// Rename cascades into custom_strategies.tsv; Apply updates both sections and restarts zapret2.
	resp, out = e.post("/api/blobs", "action", "edit", "id", "1", "value", "renamed_blob", "file", "my-custom.bin")
	wantOK(t, resp, out)
	resp, out = e.post("/api/blobs", "action", "apply")
	wantOK(t, resp, out)
	if out["restarted"] != true {
		t.Fatalf("expected restart on blobs apply, got %v", out)
	}
	cfgRaw := e.zapretConfig()
	if !strings.Contains(cfgRaw, "--blob=renamed_blob:@") || !strings.Contains(cfgRaw, "blob=renamed_blob") {
		t.Fatalf("renamed blob/strategy missing in config:\n%s", cfgRaw)
	}

	// SNI actions: pin, pool set/custom/policy, user sets CRUD & guards.
	_ = os.WriteFile(e.app.Paths.SNIZatorTxt(), []byte("# set: google\nwww.google.com\nmail.google.com\n"), 0o644)

	resp, out = e.post("/api/blobs", "action", "sni_pin", "profile", "1", "value", "cdn.example.org")
	wantOK(t, resp, out)
	if out["restart_required"] != false {
		t.Errorf("sni_pin must not require restart: %v", out)
	}
	resp, out = e.post("/api/blobs", "action", "sni_set_write", "name", "custom_set", "list", "one.org\ntwo.org")
	wantOK(t, resp, out)
	resp, out = e.post("/api/blobs", "action", "sni_pool_assign", "profile", "2", "value", "@custom_set")
	wantOK(t, resp, out)
	resp, out = e.post("/api/blobs", "action", "sni_pool_policy", "profile", "2", "value", "rand")
	wantOK(t, resp, out)

	// Protected set (zator) & in-use user set cannot be deleted.
	resp, out = e.post("/api/blobs", "action", "sni_set_delete", "name", "google")
	wantStatus(t, resp, 400)
	resp, out = e.post("/api/blobs", "action", "sni_set_delete", "name", "custom_set")
	wantStatus(t, resp, 400)

	// Clear pool, then deleting custom_set succeeds cleanly.
	resp, out = e.post("/api/blobs", "action", "sni_pool_clear", "profile", "2")
	wantOK(t, resp, out)
	resp, out = e.post("/api/blobs", "action", "sni_set_delete", "name", "custom_set")
	wantOK(t, resp, out)

	// Clean up strategy then delete blob & verify file deleted.
	_, _ = e.post("/api/custom", "action", "delete", "id", "1")
	resp, out = e.post("/api/blobs", "action", "delete", "id", "1")
	wantOK(t, resp, out)
	if _, err := os.Stat(filepath.Join(e.app.Paths.CustomBlobDir(), "my-custom.bin")); !os.IsNotExist(err) {
		t.Fatalf("expected physical blob file removed on delete, err=%v", err)
	}
}

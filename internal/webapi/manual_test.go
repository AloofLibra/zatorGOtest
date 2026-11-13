package webapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestStrategyRegister(t *testing.T) {
	e := newEnv(t, envOpts{})
	resp, out := e.post("/cgi-bin/settings.cgi",
		"setting", "strategy_register",
		"profile", "4",
		"desync", "hostfakesplit:tcp_md5",
		"restart", "0",
	)
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d out=%v", resp.StatusCode, out)
	}
	if out["ok"] != true {
		t.Fatalf("out=%v", out)
	}
	n, _ := out["strategy"].(float64)
	if n < 1 {
		t.Fatalf("strategy=%v", out["strategy"])
	}
}

func TestBlobUploadTLS(t *testing.T) {
	e := newEnv(t, envOpts{})
	name := "tls_manual_test.bin"
	body := []byte{0x16, 0x03, 0x01, 0x00, 0x01, 0x01}
	req, err := http.NewRequest(http.MethodPost, e.ts.URL+"/cgi-bin/blob-upload.cgi?kind=tls&name="+name, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d out=%v", resp.StatusCode, out)
	}
	if out["ok"] != true || out["name"] != name {
		t.Fatalf("out=%v", out)
	}
	raw, err := os.ReadFile(filepath.Join(e.srv.paths.FakeDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, body) {
		t.Fatalf("file mismatch")
	}

	req2, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/cgi-bin/blob-upload.cgi?kind=tls&name=evil.exe", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/octet-stream")
	resp2, err := e.ts.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad name, got %d", resp2.StatusCode)
	}
}

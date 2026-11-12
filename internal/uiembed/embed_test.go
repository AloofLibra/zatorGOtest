package uiembed

import (
	"io/fs"
	"strings"
	"testing"
)

func TestWebUIHasIndex(t *testing.T) {
	data, err := fs.ReadFile(WebUI(), "index.html")
	if err != nil || len(data) == 0 {
		t.Fatalf("index.html: %v", err)
	}
	if !HasWebUI() {
		t.Error("ожидался встроенный Web UI")
	}
}

func TestStubFS(t *testing.T) {
	data, err := fs.ReadFile(StubFS(), "index.html")
	if err != nil || !strings.Contains(string(data), "zator") {
		t.Fatalf("stub: %v", err)
	}
}

func TestConfigDefault(t *testing.T) {
	data, err := ConfigDefault()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "FWTYPE") {
		t.Error("config.default не содержит FWTYPE")
	}
}

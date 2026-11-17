package uiembed

import (
	"errors"
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
	if err != nil || string(data) != `<!doctype html><html><head><meta charset="utf-8"><title>zator</title></head><body><h1>zator</h1><p>Web UI не установлен.</p></body></html>` {
		t.Fatalf("stub: %v", err)
	}
}

func TestMissingDirFS(t *testing.T) {
	if _, err := fs.ReadFile(dirFS(t.TempDir()+"/missing"), "index.html"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file error = %v, want fs.ErrNotExist", err)
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

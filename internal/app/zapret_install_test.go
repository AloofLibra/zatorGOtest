package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyZapretInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zapret2")
	if legacyZapretInit(path) {
		t.Fatal("missing init must not be treated as the Zator override")
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n# z2r: оверрайды апстримных\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !legacyZapretInit(path) {
		t.Fatal("Zator-owned init override was not detected")
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n# upstream zapret2 init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if legacyZapretInit(path) {
		t.Fatal("upstream init was classified as the Zator override")
	}
}

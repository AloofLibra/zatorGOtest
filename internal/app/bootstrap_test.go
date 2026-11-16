package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/AloofLibra/zator/internal/platform"
)

func TestPreserveOpenWrtZapretInitRequiresRegisteredScript(t *testing.T) {
	root := t.TempDir()
	upstream := filepath.Join(root, "zapret2")
	registered := filepath.Join(root, "etc", "init.d", "zapret2")
	if err := os.MkdirAll(filepath.Dir(registered), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(upstream, []byte("upstream"), 0o755); err != nil {
		t.Fatal(err)
	}
	pl := platform.Platform{Kind: platform.KindOpenWRT, InitSystem: platform.InitProcd}

	if preserveOpenWrtZapretInit(pl, upstream, registered) {
		t.Fatal("must not preserve an upstream script that is not registered")
	}
	if err := os.WriteFile(registered, []byte("registered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !preserveOpenWrtZapretInit(pl, upstream, registered) {
		t.Fatal("must preserve an installed upstream procd script")
	}
	pl.InitSystem = platform.InitSysV
	if preserveOpenWrtZapretInit(pl, upstream, registered) {
		t.Fatal("must not preserve the OpenWrt script for another init system")
	}
}

func TestRegisterOpenWrtZapretInitPreservesLocalScript(t *testing.T) {
	root := t.TempDir()
	upstream := filepath.Join(root, "upstream")
	registered := filepath.Join(root, "registered")
	if err := os.WriteFile(upstream, []byte("upstream"), 0o755); err != nil {
		t.Fatal(err)
	}
	local := []byte("#!/bin/sh\n# locally managed init\n")
	if err := os.WriteFile(registered, local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := registerOpenWrtZapretInit(upstream, registered); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(registered)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(local) {
		t.Fatalf("local init overwritten: %q", got)
	}
}

func TestRegisterOpenWrtZapretInitLinksUpstream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink creation needs elevated privileges")
	}
	root := t.TempDir()
	upstream := filepath.Join(root, "upstream")
	registered := filepath.Join(root, "etc", "init.d", "zapret2")
	if err := os.MkdirAll(filepath.Dir(registered), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(upstream, []byte("upstream"), 0o755); err != nil {
		t.Fatal(err)
	}
	generated := []byte("#!/bin/sh\n# zapret2 (создано zator)\n")
	if err := os.WriteFile(registered, generated, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := registerOpenWrtZapretInit(upstream, registered); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(registered)
	if err != nil {
		t.Fatal(err)
	}
	if got != upstream {
		t.Fatalf("registered init link = %q, want %q", got, upstream)
	}
}

func TestCleanupOpenWrtInitLinksKeepsOnlyCurrentPriorities(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink creation needs elevated privileges")
	}
	root := t.TempDir()
	initDir := filepath.Join(root, "etc", "init.d")
	rcDir := filepath.Join(root, "etc", "rc.d")
	if err := os.MkdirAll(initDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	system := filepath.Join(initDir, "zapret2")
	if err := os.WriteFile(system, []byte("START=21\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"S90zapret2", "K89zapret2", "S21zapret2", "K10zapret2", "S90other"} {
		if err := os.Symlink("../init.d/zapret2", filepath.Join(rcDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupOpenWrtInitLinks(system, rcDir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"S90zapret2", "K89zapret2"} {
		if _, err := os.Lstat(filepath.Join(rcDir, name)); !os.IsNotExist(err) {
			t.Errorf("stale link %s remains: %v", name, err)
		}
	}
	for _, name := range []string{"S21zapret2", "K10zapret2", "S90other"} {
		if _, err := os.Lstat(filepath.Join(rcDir, name)); err != nil {
			t.Errorf("expected link %s was removed: %v", name, err)
		}
	}
}

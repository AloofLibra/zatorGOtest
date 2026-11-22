package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AloofLibra/zator/internal/platform"
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

func TestNeedsZapretReinstallOpenRCRepair(t *testing.T) {
	zr := t.TempDir()
	for rel, body := range map[string]string{
		"init.d/sysv/functions":  "# sysv\n",
		"init.d/sysv/zapret2":    "#!/bin/sh\n",
		"init.d/openrc/zapret2":  "#!/sbin/openrc-run\n. \"$EXEDIR/functions\"\n",
		"init.d/openrc/functions": "# fn\n",
	} {
		p := filepath.Join(zr, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	plat := platform.Platform{Kind: platform.KindVPS, InitSystem: platform.InitOpenRC, OSID: "alpine"}
	if needsZapretReinstall(plat, zr) {
		t.Fatal("valid OpenRC layout should not require reinstall")
	}

	// Поломка от v0.1.43-test15: openrc/zapret2 перезаписан SysV-скриптом.
	if err := os.WriteFile(filepath.Join(zr, "init.d", "openrc", "zapret2"), []byte("#!/bin/sh\n. \"$EXEDIR/functions\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !needsZapretReinstall(plat, zr) {
		t.Fatal("corrupted openrc/zapret2 must trigger reinstall")
	}

	// Восстанавливаем openrc/zapret2, но удаляем openrc/functions.
	if err := os.WriteFile(filepath.Join(zr, "init.d", "openrc", "zapret2"), []byte("#!/sbin/openrc-run\n. \"$EXEDIR/functions\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(zr, "init.d", "openrc", "functions")); err != nil {
		t.Fatal(err)
	}
	if !needsZapretReinstall(plat, zr) {
		t.Fatal("missing openrc/functions must trigger reinstall")
	}
}

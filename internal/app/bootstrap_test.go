package app

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/firewall"
	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/platform"
)

func TestSyncCustomDHooksRespectsSysProfile(t *testing.T) {
	cfg := config.Parse("Z2R_WSSIZE_RAMP_ENABLE=0\n")

	t.Run("openwrt", func(t *testing.T) {
		dir := t.TempDir()
		p := paths.New("/opt/zator", filepath.Join(dir, "zapret2"))
		a := NewWith(p, platform.Platform{Kind: platform.KindOpenWRT, InitSystem: platform.InitProcd})
		if err := a.SyncCustomDHooks(cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(firewall.CustomDPath(p.ZapretRoot, firewall.CustomDOpenWrt, firewall.CustomDWssizeName)); err != nil {
			t.Fatalf("missing openwrt custom.d hook: %v", err)
		}
		if _, err := os.Stat(firewall.CustomDPath(p.ZapretRoot, firewall.CustomDSysV, firewall.CustomDWssizeName)); !os.IsNotExist(err) {
			t.Fatalf("openwrt should not write sysv custom.d: %v", err)
		}
	})

	t.Run("openrc skips symlinked custom.d", func(t *testing.T) {
		dir := t.TempDir()
		p := paths.New("/opt/zator", filepath.Join(dir, "zapret2"))
		sysvDir := filepath.Join(p.ZapretRoot, "init.d", "sysv", "custom.d")
		openrcParent := filepath.Join(p.ZapretRoot, "init.d", "openrc")
		if err := os.MkdirAll(sysvDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(openrcParent, 0o755); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Symlink("../sysv/custom.d", filepath.Join(openrcParent, "custom.d")); err != nil {
				t.Fatal(err)
			}
		}
		a := NewWith(p, platform.Platform{Kind: platform.KindVPS, InitSystem: platform.InitOpenRC, OSID: "alpine"})
		if err := a.SyncCustomDHooks(cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(firewall.CustomDPath(p.ZapretRoot, firewall.CustomDSysV, firewall.CustomDWssizeName)); err != nil {
			t.Fatalf("missing sysv custom.d hook: %v", err)
		}
		if runtime.GOOS != "windows" {
			if link, err := os.Readlink(filepath.Join(openrcParent, "custom.d")); err != nil || filepath.ToSlash(link) != "../sysv/custom.d" {
				t.Fatalf("openrc/custom.d symlink replaced: %q, %v", link, err)
			}
		}
	})
}

func TestFirewallReloadCmdSelection(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	p := paths.New(filepath.Join(dir, "zator"), filepath.Join(dir, "zapret2"))
	for _, f := range []string{
		filepath.Join(root, "run", "openrc", "softlevel"),
		filepath.Join(root, "etc", "init.d", "zapret2"),
		filepath.Join(p.ZapretRoot, "init.d", "sysv", "zapret2"),
		filepath.Join(p.ZapretRoot, "init.d", "openwrt", "zapret2"),
	} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	openrcApp := NewWith(p, platform.Platform{Kind: platform.KindVPS, InitSystem: platform.InitOpenRC, OSID: "alpine"})
	cmd, ok := openrcApp.firewallReloadCmd(root)
	want := filepath.Join(p.ZapretRoot, "init.d", "sysv", "zapret2")
	if !ok || cmd.Name != want || !reflect.DeepEqual(cmd.Args, []string{"restart-fw"}) {
		t.Fatalf("openrc firewallReloadCmd = %+v (ok=%t), want %s", cmd, ok, want)
	}

	procdApp := NewWith(p, platform.Platform{Kind: platform.KindOpenWRT, InitSystem: platform.InitProcd})
	cmd, ok = procdApp.firewallReloadCmd(root)
	wantProcd := filepath.Join(root, "etc", "init.d", "zapret2")
	if !ok || cmd.Name != wantProcd || !reflect.DeepEqual(cmd.Args, []string{"restart-fw"}) {
		t.Fatalf("procd firewallReloadCmd = %+v (ok=%t), want %s", cmd, ok, wantProcd)
	}
}

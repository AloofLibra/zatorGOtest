package deps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/platform"
)

func TestCheckWindowsSkip(t *testing.T) {
	rep := Check(platform.Platform{Kind: platform.KindEntware}, Options{})
	if runtimeGOOSWindows() {
		if len(rep.Items) == 0 || rep.Items[0].Status != StatusSkip {
			t.Fatalf("expected skip on windows: %+v", rep)
		}
	}
}

func runtimeGOOSWindows() bool {
	return filepath.Separator == '\\'
}

func TestFindBinaryInRoot(t *testing.T) {
	root := t.TempDir()
	sbin := filepath.Join(root, "opt", "sbin")
	if err := os.MkdirAll(sbin, 0o755); err != nil {
		t.Fatal(err)
	}
	ipt := filepath.Join(sbin, "iptables")
	if err := os.WriteFile(ipt, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := findBinary("iptables", Options{Root: root})
	if got != ipt {
		t.Fatalf("got %q want %q", got, ipt)
	}
}

func TestModuleLoaded(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	if err := os.MkdirAll(proc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "modules"), []byte("xt_NFQUEUE 123 0 - Live 0x0\nip_set 1 0 - Live 0x0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := Options{Root: root}
	if !moduleLoaded(opts, "xt_NFQUEUE") {
		t.Fatal("expected xt_NFQUEUE loaded")
	}
	if moduleLoaded(opts, "missing_mod") {
		t.Fatal("missing should be false")
	}
}

func TestEnsureOpenWRTNftQueueModules(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"nft", "iptables", "ipset"} {
		path := filepath.Join(root, "usr", "sbin", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	proc := filepath.Join(root, "proc")
	if err := os.MkdirAll(proc, 0o755); err != nil {
		t.Fatal(err)
	}
	mods := "nfnetlink_queue 1 0 - Live 0x0\nnft_queue 1 0 - Live 0x0\nxt_connbytes 1 0 - Live 0x0\nip_set_bitmap_port 1 0 - Live 0x0\n"
	if err := os.WriteFile(filepath.Join(proc, "modules"), []byte(mods), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := Check(platform.Platform{Kind: platform.KindOpenWRT, FWType: platform.FWNftables}, Options{Root: root})
	if !rep.OK() {
		t.Fatalf("nftables queue modules should satisfy OpenWrt requirements: %s", rep.Summary())
	}
	items := map[string]bool{}
	for _, item := range rep.Items {
		if item.Kind == "module" && item.Status == StatusOK {
			items[item.Name] = true
		}
	}
	for _, want := range []string{"nfnetlink_queue", "nft_queue", "xt_connbytes"} {
		if !items[want] {
			t.Errorf("expected %s module check, got %+v", want, items)
		}
	}
	for _, item := range rep.Items {
		if item.Name == "xt_NFQUEUE" {
			t.Fatal("nftables backend must not require legacy xt_NFQUEUE")
		}
	}
}

func TestReportSummary(t *testing.T) {
	r := Report{
		Platform: platform.Platform{Kind: platform.KindEntware, Hardware: platform.HardwareKeenetic},
		Items: []Item{
			{Name: "iptables", Kind: "binary", Status: StatusOK, Detail: "/opt/sbin/iptables"},
			{Name: "ipset", Kind: "binary", Status: StatusMissing},
		},
		CriticalMissing: []string{"ipset"},
	}
	if r.OK() {
		t.Fatal("expected not OK")
	}
	s := r.Summary()
	for _, part := range []string{"ipset", "iptables", "критично"} {
		if !strings.Contains(s, part) {
			t.Fatalf("summary missing %q: %q", part, s)
		}
	}
}

func TestEnsureEntwareFakeRoot(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "opt", "sbin"),
		filepath.Join(root, "proc"),
		filepath.Join(root, "proc", "sys", "kernel"),
		filepath.Join(root, "lib", "modules", "5.0"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range []string{"opkg", "iptables", "ipset", "insmod"} {
		p := filepath.Join(root, "opt", "sbin", b)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mods := "xt_NFQUEUE 1 0 - Live 0x0\nxt_connbytes 1 0 - Live 0x0\nip_set_bitmap_port 1 0 - Live 0x0\n"
	if err := os.WriteFile(filepath.Join(root, "proc", "modules"), []byte(mods), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "proc", "sys", "kernel", "osrelease"), []byte("5.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := Check(platform.Platform{Kind: platform.KindEntware, Hardware: platform.HardwareKeenetic}, Options{Root: root})
	if !rep.OK() {
		t.Fatalf("expected OK, got: %s", rep.Summary())
	}
	names := map[string]bool{}
	for _, it := range rep.Items {
		if it.Status == StatusOK {
			names[it.Name] = true
		}
	}
	for _, want := range []string{"opkg", "iptables", "ipset", "xt_NFQUEUE", "ip_set_bitmap_port"} {
		if !names[want] {
			t.Fatalf("missing ok item %s in %s", want, rep.Summary())
		}
	}
}

func TestEnsureEntwareMissingCritical(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "proc"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "proc", "modules"), []byte(""), 0o644)
	rep := Check(platform.Platform{Kind: platform.KindEntware}, Options{Root: root})
	if rep.OK() {
		t.Fatal("expected critical missing")
	}
	joined := strings.Join(rep.CriticalMissing, ",")
	for _, want := range []string{"opkg", "iptables", "ipset", "xt_NFQUEUE"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("critical should include %s: %v", want, rep.CriticalMissing)
		}
	}
}

func TestOpkgInstallArgsNoDashY(t *testing.T) {
	args := opkgInstallArgs("iptables", "ipset")
	if len(args) < 3 || args[0] != "install" {
		t.Fatalf("args=%v", args)
	}
	for _, a := range args {
		if a == "-y" || a == "--yes" {
			t.Fatalf("Entware opkg must not get -y: %v", args)
		}
	}
	if args[1] != "iptables" || args[2] != "ipset" {
		t.Fatalf("args=%v", args)
	}
}

func TestModuleLoadedSysfs(t *testing.T) {
	root := t.TempDir()
	mod := filepath.Join(root, "sys", "module", "xt_NFQUEUE")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if !moduleLoaded(Options{Root: root}, "xt_NFQUEUE") {
		t.Fatal("expected sysfs module")
	}
}

func TestModuleLoadedBuiltin(t *testing.T) {
	root := t.TempDir()
	for path, body := range map[string]string{
		filepath.Join(root, "proc", "sys", "kernel", "osrelease"):         "6.6.58-0-lts\n",
		filepath.Join(root, "lib", "modules", "6.6.58-0-lts", "modules.builtin"): "kernel/net/netfilter/nfnetlink_queue.ko\nkernel/net/netfilter/nft_queue.ko.xz\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{Root: root}
	if !moduleLoaded(opts, "nfnetlink_queue") {
		t.Fatal("expected nfnetlink_queue from modules.builtin")
	}
	if !moduleLoaded(opts, "nft_queue") {
		t.Fatal("expected nft_queue.ko.xz from modules.builtin")
	}
	if moduleLoaded(opts, "xt_NFQUEUE") {
		t.Fatal("xt_NFQUEUE should not be reported loaded")
	}
}

func TestEnsureAlpineNftablesAndIptables(t *testing.T) {
	t.Run("nftables", func(t *testing.T) {
		root := t.TempDir()
		nft := filepath.Join(root, "usr", "sbin", "nft")
		if err := os.MkdirAll(filepath.Dir(nft), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(nft, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		proc := filepath.Join(root, "proc")
		if err := os.MkdirAll(proc, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proc, "modules"), []byte("nfnetlink_queue 1 0 - Live 0x0\nnft_queue 1 0 - Live 0x0\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		rep := Check(platform.Platform{
			Kind: platform.KindVPS, OSID: "alpine", InitSystem: platform.InitOpenRC, FWType: platform.FWNftables,
		}, Options{Root: root})
		if !rep.OK() {
			t.Fatalf("expected Alpine nftables deps OK without iptables/ipset/xt_NFQUEUE: %s", rep.Summary())
		}
		for _, it := range rep.Items {
			if it.Name == "iptables" || it.Name == "ipset" || it.Name == "xt_NFQUEUE" {
				t.Fatalf("Alpine nftables should not require %s: %+v", it.Name, rep.Items)
			}
		}
	})

	t.Run("iptables", func(t *testing.T) {
		root := t.TempDir()
		for _, bin := range []string{"iptables", "ipset"} {
			p := filepath.Join(root, "sbin", bin)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		proc := filepath.Join(root, "proc")
		if err := os.MkdirAll(proc, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proc, "modules"), []byte("xt_NFQUEUE 1 0 - Live 0x0\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		rep := Check(platform.Platform{
			Kind: platform.KindVPS, OSID: "alpine", InitSystem: platform.InitOpenRC, FWType: platform.FWIptables,
		}, Options{Root: root})
		if !rep.OK() {
			t.Fatalf("expected Alpine iptables deps OK: %s", rep.Summary())
		}
	})
}

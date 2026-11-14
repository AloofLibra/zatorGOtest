package netiface

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidName(t *testing.T) {
	ok := []string{"eth3", "br0", "eth2.1", "br-lan", "wg0"}
	for _, n := range ok {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false", n)
		}
	}
	bad := []string{"", "eth 3", "eth;3", "../x", "a/b"}
	for _, n := range bad {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true", n)
		}
	}
}

func TestValidList(t *testing.T) {
	if !ValidList("eth3") || !ValidList("eth3 nwg0") {
		t.Fatal("expected valid lists")
	}
	if ValidList("") || ValidList("eth3;rm") {
		t.Fatal("expected invalid lists")
	}
}

func TestDefaultRouteDev(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc", "net")
	if err := os.MkdirAll(proc, 0o755); err != nil {
		t.Fatal(err)
	}
	// Keenetic-like: default via eth3 metric 1000
	route := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth3\t00000000\t01002C2E\t0003\t0\t0\t1000\t00000000\t0\t0\t0\n" +
		"br0\t0001A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n"
	if err := os.WriteFile(filepath.Join(proc, "route"), []byte(route), 0o644); err != nil {
		t.Fatal(err)
	}
	d := Detector{Root: root}
	got, err := d.DefaultRouteDev()
	if err != nil {
		t.Fatal(err)
	}
	if got != "eth3" {
		t.Fatalf("DefaultRouteDev = %q, want eth3", got)
	}
}

func TestDetectWANLANPrefersBr0(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc", "net")
	sys := filepath.Join(root, "sys", "class", "net")
	if err := os.MkdirAll(proc, 0o755); err != nil {
		t.Fatal(err)
	}
	route := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth3\t00000000\t01002C2E\t0003\t0\t0\t1000\t00000000\t0\t0\t0\n"
	if err := os.WriteFile(filepath.Join(proc, "route"), []byte(route), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"eth3", "br0", "ra0", "lo"} {
		dir := filepath.Join(sys, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, "operstate"), []byte("up\n"), 0o644)
	}
	d := Detector{Root: root}
	wan, lan, src, err := d.DetectWANLAN()
	if err != nil {
		t.Fatal(err)
	}
	if wan != "eth3" {
		t.Fatalf("wan=%q", wan)
	}
	if lan != "br0" {
		t.Fatalf("lan=%q src=%s", lan, src)
	}
	if src != "bridge" {
		t.Fatalf("source=%q", src)
	}
}

func TestListFiltersNoise(t *testing.T) {
	root := t.TempDir()
	sys := filepath.Join(root, "sys", "class", "net")
	proc := filepath.Join(root, "proc", "net")
	_ = os.MkdirAll(proc, 0o755)
	_ = os.WriteFile(filepath.Join(proc, "route"), []byte(
		"Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n"+
			"eth3\t00000000\t01002C2E\t0003\t0\t0\t1000\t00000000\t0\t0\t0\n"), 0o644)
	for _, name := range []string{"eth3", "br0", "ra0", "apcli0", "dummy0", "lo"} {
		dir := filepath.Join(sys, name)
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "operstate"), []byte("up\n"), 0o644)
	}
	list, err := (Detector{Root: root}).List()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, i := range list {
		names[i.Name] = true
	}
	if !names["eth3"] || !names["br0"] {
		t.Fatalf("missing eth3/br0: %v", names)
	}
	if names["ra0"] || names["apcli0"] || names["dummy0"] || names["lo"] {
		t.Fatalf("noise not filtered: %v", names)
	}
}

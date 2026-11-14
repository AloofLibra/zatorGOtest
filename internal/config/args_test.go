package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildNfqws2Args(t *testing.T) {
	c := Parse(`QNUM=210
FOO=bar
NFQWS2_OPT="
--new
--filter-tcp=443
--lua-init=@/opt/zator/lua/${FOO}.lua
--skip --filter-udp=53
--lua-desync=fake:strategy=1 --lua-desync=multisplit:strategy=1
"
`)
	args := c.BuildNfqws2Args()
	if len(args) < 4 || args[0] != "--qnum=210" {
		t.Fatalf("%v", args)
	}
	joined := strings.Join(args, "\n")
	if !strings.Contains(joined, "--lua-init=@/opt/zator/lua/bar.lua") {
		t.Fatalf("expand failed: %v", args)
	}
	// --skip и filter должны быть разными argv (иначе @file не отключает профиль).
	var sawSkip, sawDNS bool
	for i, a := range args {
		if a == "--skip" {
			sawSkip = true
			if i+1 >= len(args) || args[i+1] != "--filter-udp=53" {
				t.Fatalf("skip not followed by dns filter: %v", args[i:])
			}
		}
		if a == "--filter-udp=53" {
			sawDNS = true
		}
		if strings.Contains(a, " ") {
			t.Fatalf("token with space (must split): %q", a)
		}
	}
	if !sawSkip || !sawDNS {
		t.Fatalf("missing skip/dns tokens: %v", args)
	}
}

func TestWriteNfqws2Args(t *testing.T) {
	c := Parse(`NFQWS2_OPT="
--new
--filter-tcp=80
"
`)
	p := filepath.Join(t.TempDir(), "nfqws2.args")
	if err := c.WriteNfqws2Args(p); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.HasPrefix(got, "--qnum=200\n") || !strings.Contains(got, "--filter-tcp=80\n") {
		t.Fatalf("%q", got)
	}
}

func TestValidateNfqws2Args(t *testing.T) {
	if err := ValidateNfqws2Args(nil); err == nil {
		t.Fatal("empty")
	}
	if err := ValidateNfqws2Args([]string{"--qnum=200", "--new"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNfqws2Args([]string{"--lua-desync=fake:blob=x;rm"}); err == nil {
		t.Fatal("metachar")
	}
	if err := ValidateNfqws2Args([]string{"not-a-flag"}); err == nil {
		t.Fatal("flag required")
	}
	c := Parse(`NFQWS2_OPT="--new --filter-tcp=80;evil"`)
	if err := c.WriteNfqws2Args(filepath.Join(t.TempDir(), "a")); err == nil {
		t.Fatal("write must reject metachar")
	}
}

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

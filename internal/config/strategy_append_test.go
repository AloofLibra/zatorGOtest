package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestNormalizeDesyncLine(t *testing.T) {
	got, err := NormalizeDesyncLine("fake:blob=fake_default_tls:repeats=2", 45)
	if err != nil {
		t.Fatal(err)
	}
	want := "--lua-desync=fake:blob=fake_default_tls:repeats=2:strategy=45"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	got, err = NormalizeDesyncLine("--lua-desync=multisplit:pos=1:strategy=9 --lua-desync=fake:blob=x:strategy=9", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, ":strategy=50") || strings.Contains(got, ":strategy=9") {
		t.Fatalf("strategy rewrite failed: %q", got)
	}
}

func TestAppendProfileStrategyCommon(t *testing.T) {
	src := filepath.Join("..", "..", "assets", "config.default")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Skip(err)
	}
	c := Parse(string(raw))
	before := c.ProfileMaxStrategyOrDefault(1)
	n, err := c.AppendProfileStrategy(1, "fake:blob=fake_default_tls:repeats=2 --lua-desync=multisplit:pos=1,midsld")
	if err != nil {
		t.Fatal(err)
	}
	if n != before+1 {
		t.Fatalf("n=%d want %d", n, before+1)
	}
	if c.ProfileMaxStrategyOrDefault(1) != n {
		t.Fatalf("max after=%d want %d", c.ProfileMaxStrategyOrDefault(1), n)
	}
	opt, _ := c.NFQWS2Opt()
	if !strings.Contains(opt, ":strategy="+strconv.Itoa(n)) {
		t.Fatalf("opt missing strategy line")
	}
}

func TestAppendProfileStrategyDiscord(t *testing.T) {
	src := filepath.Join("..", "..", "assets", "config.default")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Skip(err)
	}
	c := Parse(string(raw))
	before := c.ProfileMaxStrategyOrDefault(4)
	n, err := c.AppendProfileStrategy(4, "hostfakesplit:tcp_md5")
	if err != nil {
		t.Fatal(err)
	}
	if n != before+1 {
		t.Fatalf("n=%d want %d", n, before+1)
	}
	if c.ProfileMaxStrategyOrDefault(4) != n {
		t.Fatalf("discord max not updated")
	}
}

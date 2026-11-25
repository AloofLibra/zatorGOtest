package config

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestBuildNfqws2RuntimeArgsAddsInitDefaults(t *testing.T) {
	c := Parse(`QNUM=210
DESYNC_MARK=0x40000001
NFQWS2_OPT="
--qnum=210
--user=obsolete
--fwmark=obsolete
--lua-init=@/custom/zapret-lib.lua
--lua-init=@/opt/zator/lua/fake-adapt.lua
--filter-tcp=443
"
`)
	args := c.BuildNfqws2RuntimeArgs("/opt/zapret2/lua", "nobody", "")
	for _, want := range []string{
		"--user=nobody",
		"--fwmark=0x40000001",
		"--lua-init=@/opt/zapret2/lua/zapret-lib.lua",
		"--lua-init=@/opt/zapret2/lua/zapret-antidpi.lua",
		"--lua-init=@/opt/zapret2/lua/zapret-auto.lua",
		"--qnum=210",
		"--lua-init=@/opt/zator/lua/fake-adapt.lua",
	} {
		found := false
		for _, arg := range args {
			if arg == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %q: %v", want, args)
		}
	}
	for _, arg := range args {
		if arg == "--user=obsolete" || arg == "--fwmark=obsolete" || arg == "--lua-init=@/custom/zapret-lib.lua" {
			t.Fatalf("duplicate base setting: %v", args)
		}
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
	if !strings.HasPrefix(got, "--qnum=300\n") || !strings.Contains(got, "--filter-tcp=80\n") {
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
	if err := ValidateNfqws2Args([]string{"--qnum=300", "--new", "--new", "--filter-udp=443"}); err == nil {
		t.Fatal("consecutive --new must be rejected")
	}
}

func TestCollapseDuplicateNew(t *testing.T) {
	raw := `NFQWS2_OPT="
--new

#Z2R_AUTO_STANDARD_4_END

--new

# комментарий перед следующим --new
--new

#Стратегии для UDP_YT
--filter-udp=443
"`
	c := Parse(raw)
	changed, err := c.CollapseDuplicateNew()
	if err != nil {
		t.Fatalf("CollapseDuplicateNew: %v", err)
	}
	if !changed {
		t.Fatal("expected consecutive --new across blank lines and comments to collapse")
	}
	opt, _ := c.NFQWS2Opt()
	if !strings.Contains(opt, "#Z2R_AUTO_STANDARD_4_END") || !strings.Contains(opt, "# комментарий перед следующим --new") {
		t.Fatalf("comments/markers lost:\n%s", opt)
	}
	if got := strings.Count(opt, "--new"); got != 2 {
		t.Fatalf("expected 2 --new separators (leading + one before UDP_YT), got %d:\n%s", got, opt)
	}
	changedAgain, err := c.CollapseDuplicateNew()
	if err != nil || changedAgain {
		t.Fatalf("second CollapseDuplicateNew = (%v, %v), want (false, nil)", changedAgain, err)
	}

	inline := Parse("NFQWS2_OPT=\"\n--new --new --filter-udp=443\n--new\n\"\n")
	if ch, err := inline.CollapseDuplicateNew(); err != nil || !ch {
		t.Fatalf("inline collapse = (%v, %v), want (true, nil)", ch, err)
	}
	inOpt, _ := inline.NFQWS2Opt()
	if !strings.Contains(inOpt, "\n--new --filter-udp=443\n") {
		t.Fatalf("inline duplicate --new not normalized:\n%s", inOpt)
	}
	if ch, err := inline.CollapseDuplicateNew(); err != nil || ch {
		t.Fatalf("single trailing --new should stay untouched: (%v, %v)", ch, err)
	}

	stock, err := Load(filepath.Join("..", "..", "assets", "config.default"))
	if err != nil {
		t.Fatalf("load config.default: %v", err)
	}
	if ch, err := stock.CollapseDuplicateNew(); err != nil || ch {
		t.Fatalf("assets/config.default unexpectedly modified by CollapseDuplicateNew: (%v, %v)", ch, err)
	}
}

func TestBuildNfqws2ArgsCollapsesConsecutiveNew(t *testing.T) {
	c := Parse(`EMPTY=
QNUM=210
NFQWS2_OPT="
--filter-tcp=443
--new
${EMPTY}
--new
--qnum=210
--new
--filter-udp=443
"
`)
	args := c.BuildNfqws2Args()
	for i := 1; i < len(args); i++ {
		if args[i] == "--new" && args[i-1] == "--new" {
			t.Fatalf("BuildNfqws2Args left duplicate --new: %v", args)
		}
	}
	rt := c.BuildNfqws2RuntimeArgs("/opt/zapret2/lua", "nobody", "")
	for i := 1; i < len(rt); i++ {
		if rt[i] == "--new" && rt[i-1] == "--new" {
			t.Fatalf("BuildNfqws2RuntimeArgs left duplicate --new after stripping --qnum: %v", rt)
		}
	}
	if err := ValidateNfqws2Args(rt); err != nil {
		t.Fatalf("runtime args invalid: %v\n%v", err, rt)
	}
}

func TestReadNfqws2ArgsFile(t *testing.T) {
	if _, err := ReadNfqws2ArgsFile(filepath.Join(t.TempDir(), "missing.args")); err == nil {
		t.Fatal("expected error for missing file")
	}

	c := Parse(`NFQWS2_OPT="
--new
--filter-tcp=80
--filter-udp=443
"
`)
	p := filepath.Join(t.TempDir(), "nfqws2.args")
	if err := c.WriteNfqws2Args(p); err != nil {
		t.Fatal(err)
	}
	got, err := ReadNfqws2ArgsFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := c.BuildNfqws2Args()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip = %v, want %v", got, want)
	}

	withComments := filepath.Join(t.TempDir(), "commented.args")
	if err := os.WriteFile(withComments, []byte("# comment\n\n  --qnum=300  \n--new\n# another\n--new\n--filter-udp=443\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := ReadNfqws2ArgsFile(withComments)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateNfqws2Args(parsed); err == nil {
		t.Fatalf("expected ReadNfqws2ArgsFile to preserve duplicate --new for validation: %v", parsed)
	}
}

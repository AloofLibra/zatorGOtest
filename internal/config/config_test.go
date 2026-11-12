package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleSimple = `# Last modified: 2026-11-10 15:03:39 UTC
WS_USER=nobody

# override firewall type
FWTYPE=iptables
IPSET_OPT="hashsize 262144 maxelem $SET_MAXELEM"
#IFACE_WAN=eth1
  INDENTED=yes
EMPTY=
SINGLE='quoted value'
COMMENTED=42   # trailing comment
QUOTED_COMMENT="a b" # c
NFQWS2_PORTS_TCP=80,443
MODE_FILTER=hostlist
`

func TestParseGetBasics(t *testing.T) {
	c := Parse(sampleSimple)

	tests := []struct {
		name   string
		want   string
		wantOK bool
	}{
		{"WS_USER", "nobody", true},
		{"FWTYPE", "iptables", true},
		{"IPSET_OPT", "hashsize 262144 maxelem $SET_MAXELEM", true},
		{"IFACE_WAN", "", false}, // закомментировано
		{"INDENTED", "yes", true},
		{"EMPTY", "", true},
		{"SINGLE", "quoted value", true},
		{"COMMENTED", "42", true},
		{"QUOTED_COMMENT", "a b", true},
		{"MISSING", "", false},
	}
	for _, tc := range tests {
		got, ok := c.Get(tc.name)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("Get(%s) = (%q,%v), want (%q,%v)", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}

	if raw, _ := c.GetRaw("COMMENTED"); raw != "42   # trailing comment" {
		t.Errorf("GetRaw(COMMENTED) = %q", raw)
	}
	if !c.Has("EMPTY") || c.Has("MISSING") {
		t.Error("Has returned wrong result")
	}
	if n, ok := c.GetInt("COMMENTED"); !ok || n != 42 {
		t.Errorf("GetInt = (%d,%v)", n, ok)
	}
	if _, ok := c.GetInt("WS_USER"); ok {
		t.Error("GetInt on non-numeric value must fail")
	}
	if got := c.GetDefault("MISSING", "dflt"); got != "dflt" {
		t.Errorf("GetDefault = %q", got)
	}
}

func TestFirstOccurrenceWins(t *testing.T) {
	c := Parse("A=1\nB=2\nA=3\n")
	if v, _ := c.Get("A"); v != "1" {
		t.Errorf("A = %q, want 1", v)
	}
	if err := c.Set("A", "9"); err != nil {
		t.Fatal(err)
	}
	if got, want := c.String(), "A=9\nB=2\nA=3\n"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestSet(t *testing.T) {
	tests := []struct {
		name, in, key, val, want string
		wantErr                  bool
	}{
		{name: "replace", in: "FWTYPE=iptables\nX=1\n", key: "FWTYPE", val: "nftables", want: "FWTYPE=nftables\nX=1\n"},
		{name: "append", in: "X=1\n", key: "NEW", val: "2", want: "X=1\nNEW=2\n"},
		{name: "append no final newline", in: "X=1", key: "NEW", val: "2", want: "X=1\nNEW=2"},
		{name: "empty file", in: "", key: "NEW", val: "2", want: "NEW=2\n"},
		{name: "raw quoted", in: "A=1\n", key: "A", val: `"x y"`, want: "A=\"x y\"\n"},
		{name: "drops indent", in: "  A=1\n", key: "A", val: "2", want: "A=2\n"},
		{name: "keeps commented", in: "#A=1\nA=2\n", key: "A", val: "3", want: "#A=1\nA=3\n"},
		{name: "crlf preserved", in: "A=1\r\nB=2\r\n", key: "A", val: "5", want: "A=5\r\nB=2\r\n"},
		{name: "bad name", in: "", key: "1BAD", val: "x", wantErr: true},
		{name: "newline value", in: "", key: "A", val: "x\ny", wantErr: true},
		{name: "empty value", in: "A=1\n", key: "A", val: "", want: "A=\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Parse(tc.in)
			err := c.Set(tc.key, tc.val)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := c.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetQuoted(t *testing.T) {
	tests := []struct{ val, want string }{
		{"simple", "V=simple\n"},
		{"with space", "V=\"with space\"\n"},
		{"", "V=\"\"\n"},
	}
	for _, tc := range tests {
		c := Parse("")
		if err := c.SetQuoted("V", tc.val); err != nil {
			t.Fatal(err)
		}
		if c.String() != tc.want {
			t.Errorf("SetQuoted(%q) = %q, want %q", tc.val, c.String(), tc.want)
		}
	}
	if err := Parse("").SetQuoted("V", `a"b`); err == nil {
		t.Error("expected error for embedded quote")
	}
}

func TestVars(t *testing.T) {
	c := Parse(sampleSimple + "NFQWS2_OPT=\"\n--new\n\"\n")
	vars := c.Vars()
	if vars["FWTYPE"] != "iptables" || vars["SINGLE"] != "quoted value" {
		t.Errorf("unexpected vars: %v", vars)
	}
	if _, ok := vars[OptVar]; ok {
		t.Error("Vars must not include NFQWS2_OPT")
	}
}

const sampleOpt = `FWTYPE=iptables
NFQWS2_OPT="

--lua-init=@/opt/zator/lua/a.lua
--new
FAKE_VAR=must-not-be-parsed
--filter-tcp=443
"
FLOWOFFLOAD=donttouch
`

func TestNFQWS2Opt(t *testing.T) {
	c := Parse(sampleOpt)
	opt, ok := c.NFQWS2Opt()
	if !ok {
		t.Fatal("NFQWS2_OPT not found")
	}
	want := "\n\n--lua-init=@/opt/zator/lua/a.lua\n--new\nFAKE_VAR=must-not-be-parsed\n--filter-tcp=443\n"
	if opt != want {
		t.Errorf("NFQWS2Opt() = %q, want %q", opt, want)
	}
	if c.Has("FAKE_VAR") {
		t.Error("variable inside NFQWS2_OPT must not be parsed")
	}
	if v, _ := c.Get("FLOWOFFLOAD"); v != "donttouch" {
		t.Errorf("FLOWOFFLOAD after multiline = %q", v)
	}

	if err := c.SetNFQWS2Opt("\n--new\n--skip"); err != nil {
		t.Fatal(err)
	}
	if got, want := c.String(), "FWTYPE=iptables\nNFQWS2_OPT=\"\n--new\n--skip\n\"\nFLOWOFFLOAD=donttouch\n"; got != want {
		t.Errorf("after SetNFQWS2Opt:\n%q\nwant\n%q", got, want)
	}
	if opt2, _ := c.NFQWS2Opt(); opt2 != "\n--new\n--skip\n" {
		t.Errorf("round trip opt = %q", opt2)
	}
	if v, _ := c.Get("FLOWOFFLOAD"); v != "donttouch" {
		t.Errorf("FLOWOFFLOAD lost: %q", v)
	}
	if err := c.SetNFQWS2Opt("a\n\"\nb\n"); err == nil {
		t.Error("expected error for lone quote line")
	}
}

func TestSetNFQWS2OptAppend(t *testing.T) {
	c := Parse("A=1\n")
	if err := c.SetNFQWS2Opt("\n--new\n"); err != nil {
		t.Fatal(err)
	}
	if got, want := c.String(), "A=1\nNFQWS2_OPT=\"\n--new\n\"\n"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestSetReplacesMultilineVar(t *testing.T) {
	c := Parse(sampleOpt)
	if err := c.Set(OptVar, `"x"`); err != nil {
		t.Fatal(err)
	}
	if got, want := c.String(), "FWTYPE=iptables\nNFQWS2_OPT=\"x\"\nFLOWOFFLOAD=donttouch\n"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestSaveAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("FWTYPE=iptables\n# keep me\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Path() != path {
		t.Errorf("Path() = %q", c.Path())
	}
	if err := c.Set("FWTYPE", "nftables"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "FWTYPE=nftables\n# keep me\n" {
		t.Errorf("saved content = %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected only the config file, got %d entries", len(entries))
	}

	if err := Parse("A=1\n").Save(); err != ErrNoPath {
		t.Errorf("Save without path = %v, want ErrNoPath", err)
	}
	if _, err := Load(filepath.Join(dir, "missing")); err == nil {
		t.Error("Load of missing file must fail")
	}
}

func TestLinesIsCopy(t *testing.T) {
	c := Parse("A=1\n")
	l := c.Lines()
	l[0] = "B=2"
	if c.String() != "A=1\n" {
		t.Error("Lines() must return a copy")
	}
}

func TestRoundTripUnchanged(t *testing.T) {
	for _, in := range []string{sampleSimple, sampleOpt, "", "A=1", "\n\n"} {
		if got := Parse(in).String(); got != in {
			t.Errorf("round trip changed %q -> %q", in, got)
		}
	}
	crlf := strings.ReplaceAll(sampleOpt, "\n", "\r\n")
	if got := Parse(crlf).String(); got != crlf {
		t.Error("CRLF round trip changed content")
	}
}

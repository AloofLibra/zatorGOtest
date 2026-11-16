package config

import (
	"reflect"
	"strings"
	"testing"
)

const sampleBlocks = `FWTYPE=iptables
NFQWS2_OPT="
--lua-init=@/opt/zator/lua/a.lua
#Z2R_AUTO_STANDARD_BEGIN
#Z2R_AUTO_STANDARD_3_BEGIN
--filter-tcp=443
--lua-desync=multisplit:pos=1:strategy=1
--lua-desync=multisplit:pos=2:strategy=12
#Z2R_AUTO_STANDARD_3_END

--new

#Z2R_AUTO_STANDARD_1_BEGIN
--lua-desync=fake:strategy=7
#Z2R_AUTO_STANDARD_1_END

  #Z2R_FALLBACK_BEGIN
#Z2R_AUTO_STANDARD_8_BEGIN
--skip --filter-tcp=443
#Z2R_AUTO_STANDARD_8_END
  #Z2R_FALLBACK_END
#Z2R_AUTO_STANDARD_3S_BEGIN
--x
#Z2R_AUTO_STANDARD_3S_END
#Z2R_AUTO_STANDARD_END
#Z2R_AUTO_BEGIN
#Z2R_AUTO_MODE=0
#Z2R_AUTO_FALLBACK_WAS=0
#Z2R_AUTO_3_BEGIN
--y
#Z2R_AUTO_3_END
#Z2R_AUTO_END
#Z2R_BROKEN_BEGIN
--never-closed
"
`

func TestFindBlock(t *testing.T) {
	c := Parse(sampleBlocks)

	tests := []struct {
		name      string
		wantOK    bool
		wantLines []string
	}{
		{"AUTO_STANDARD_3", true, []string{
			"--filter-tcp=443",
			"--lua-desync=multisplit:pos=1:strategy=1",
			"--lua-desync=multisplit:pos=2:strategy=12",
		}},
		{"AUTO_STANDARD_1", true, []string{"--lua-desync=fake:strategy=7"}},
		{"FALLBACK", true, []string{
			"#Z2R_AUTO_STANDARD_8_BEGIN",
			"--skip --filter-tcp=443",
			"#Z2R_AUTO_STANDARD_8_END",
		}},
		{"AUTO_STANDARD_3S", true, []string{"--x"}},
		{"AUTO_STANDARD_9", false, nil},
		{"BROKEN", false, nil}, // нет END
		{"", false, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, ok := c.FindBlock(tc.name)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && !reflect.DeepEqual(b.Lines, tc.wantLines) {
				t.Errorf("Lines = %q, want %q", b.Lines, tc.wantLines)
			}
		})
	}
}

func TestBlockMaxStrategy(t *testing.T) {
	c := Parse(sampleBlocks)
	b, _ := c.AutoStandardBlock("3")
	if got := b.MaxStrategy(); got != 12 {
		t.Errorf("MaxStrategy = %d, want 12", got)
	}
	b, _ = c.AutoStandardBlock("8")
	if got := b.MaxStrategy(); got != 0 {
		t.Errorf("MaxStrategy of block without strategies = %d, want 0", got)
	}
	if b.Text() != "--skip --filter-tcp=443" {
		t.Errorf("Text = %q", b.Text())
	}
}

func TestIDsAndBlocks(t *testing.T) {
	c := Parse(sampleBlocks)
	if got, want := c.AutoStandardIDs(), []string{"3", "1", "8", "3S"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AutoStandardIDs = %v, want %v", got, want)
	}
	if got, want := c.AutoIDs(), []string{"3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AutoIDs = %v, want %v", got, want)
	}
	if _, ok := c.AutoBlock("3"); !ok {
		t.Error("AutoBlock(3) not found")
	}

	var names []string
	for _, b := range c.Blocks() {
		names = append(names, b.Name)
	}
	want := []string{"AUTO_STANDARD", "AUTO_STANDARD_3", "AUTO_STANDARD_1", "FALLBACK",
		"AUTO_STANDARD_8", "AUTO_STANDARD_3S", "AUTO", "AUTO_3"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Blocks names = %v, want %v", names, want)
	}
}

func TestAutoModeFlag(t *testing.T) {
	c := Parse(sampleBlocks)
	if on, ok := c.AutoModeFlag(); !ok || on {
		t.Fatalf("AutoModeFlag = (%v,%v), want (false,true)", on, ok)
	}
	if err := c.SetAutoModeFlag(true); err != nil {
		t.Fatal(err)
	}
	if on, ok := c.AutoModeFlag(); !ok || !on {
		t.Fatalf("after set: (%v,%v)", on, ok)
	}
	if got := Parse("A=1\n").SetAutoModeFlag(true); got == nil {
		t.Error("expected error when marker is absent")
	}
	if _, ok := Parse("A=1\n").AutoModeFlag(); ok {
		t.Error("marker must be reported absent")
	}
}

func TestReplaceBlockBody(t *testing.T) {
	c := Parse(sampleBlocks)
	if err := c.ReplaceBlockBody("AUTO_STANDARD_1", []string{"--new-body", "--second"}); err != nil {
		t.Fatal(err)
	}
	b, ok := c.AutoStandardBlock("1")
	if !ok || !reflect.DeepEqual(b.Lines, []string{"--new-body", "--second"}) {
		t.Fatalf("block after replace: %+v ok=%v", b, ok)
	}
	// Остальные блоки и NFQWS2_OPT не пострадали.
	if _, ok := c.AutoStandardBlock("3"); !ok {
		t.Error("neighbour block lost")
	}
	if opt, ok := c.NFQWS2Opt(); !ok || opt == "" {
		t.Error("NFQWS2_OPT broken after block replace")
	}
	if err := c.ReplaceBlockBody("NOPE", nil); err == nil {
		t.Error("expected ErrNotFound")
	}
}

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

func TestRemoveLegacyQUIC443KeepsFollowingProfile(t *testing.T) {
	c := Parse(`--new
#Z2R_QUIC443_BEGIN
--filter-udp=443
--payload=quic_initial
--lua-desync=fake:blob=quic_google:repeats=10
#Z2R_QUIC443_END
--new
#Z2R_AUTO_STANDARD_5_BEGIN
--filter-udp=443
--lua-desync=fake:blob=quic_google:strategy=1
#Z2R_AUTO_STANDARD_5_END
`)
	if !c.RemoveLegacyQUIC443() {
		t.Fatal("expected legacy QUIC443 profile to be removed")
	}
	got := c.String()
	if strings.Contains(got, "QUIC443") || strings.Contains(got, "repeats=10") {
		t.Fatalf("legacy profile remains: %s", got)
	}
	if !strings.Contains(got, "--new\n#Z2R_AUTO_STANDARD_5_BEGIN") || !strings.Contains(got, "strategy=1") {
		t.Fatalf("shared QUIC strategy profile was removed or corrupted: %s", got)
	}
	if c.RemoveLegacyQUIC443() {
		t.Fatal("second cleanup should be a no-op")
	}
}

func TestRemoveLegacyQUIC443KeyedProfile(t *testing.T) {
	c := Parse(`NFQWS2_OPT="
--new
--filter-udp=443
--lua-desync=circular_locked:key=5:proto=udp
--payload=quic_initial
--lua-desync=fake:blob=quic_google:strategy=1
--new
--filter-udp=1026-65531
--lua-desync=circular_locked:key=7:proto=udp
--lua-desync=fake:blob=quic_google:strategy=18
"
`)
	if !c.RemoveLegacyQUIC443() {
		t.Fatal("expected keyed legacy QUIC profile to be removed")
	}
	opt, _ := c.NFQWS2Opt()
	if strings.Contains(opt, "key=5:proto=udp") || strings.Contains(opt, "quic_initial") {
		t.Fatalf("legacy QUIC profile remains: %s", opt)
	}
	if !strings.Contains(opt, "key=7:proto=udp") || !strings.Contains(opt, "strategy=18") {
		t.Fatalf("shared UDP QUIC strategies were removed: %s", opt)
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

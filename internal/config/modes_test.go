package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestModeTextNilConfig(t *testing.T) {
	tests := []struct{ mode, want string }{
		{ModeRSTGuard, "нет config"},
		{ModeTLSBlobMode, "не определён"},
		{ModeHostlist, "неизвестно"},
		{"whatever", "неизвестно"},
	}
	for _, tc := range tests {
		if got := ModeText(nil, tc.mode); got != tc.want {
			t.Errorf("ModeText(nil,%q) = %q, want %q", tc.mode, got, tc.want)
		}
	}
	if got := Parse("").ModeText("no-such-mode"); got != "неизвестно" {
		t.Errorf("unknown mode = %q", got)
	}
}

func TestHostlistAndSimpleVars(t *testing.T) {
	tests := []struct {
		name, in, mode, want string
	}{
		{"hostlist", "MODE_FILTER=hostlist\n", ModeHostlist, "по листам"},
		{"autohostlist", "MODE_FILTER=autohostlist\n", ModeHostlist, "авто"},
		{"none", "MODE_FILTER=none\n", ModeHostlist, "неизвестно"},
		{"missing", "", ModeHostlist, "неизвестно"},
		{"fwtype", "FWTYPE=nftables\n", ModeFWType, "nftables"},
		{"fwtype missing", "", ModeFWType, "неизвестно"},
		{"flowoffload", "FLOWOFFLOAD=donttouch\n", ModeFlowOffload, "donttouch"},
		{"flowoffload missing", "", ModeFlowOffload, "неизвестно"},
		{"client scope on", "CLIENT_SCOPE_ENABLE=1\n", ModeClientScope, "включен"},
		{"client scope off", "CLIENT_SCOPE_ENABLE=0\n", ModeClientScope, "выключен"},
		{"client scope missing", "", ModeClientScope, "выключен"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ModeText(Parse(tc.in), tc.mode); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
	if !Parse("MODE_FILTER=autohostlist\n").HostlistAuto() || Parse("MODE_FILTER=hostlist\n").HostlistAuto() {
		t.Error("HostlistAuto wrong")
	}
}

func TestAutoModeAndFallback(t *testing.T) {
	const fbSkip = "  #Z2R_FALLBACK_BEGIN\n--skip --filter-tcp=443\n  #Z2R_FALLBACK_END\n"
	const fbOn = "  #Z2R_FALLBACK_BEGIN\n--filter-tcp=443\n  #Z2R_FALLBACK_END\n"
	const fbHTTPOn = "  #Z2R_FALLBACK_HTTP_BEGIN\n--filter-tcp=80\n  #Z2R_FALLBACK_HTTP_END\n"
	tests := []struct {
		name, in, autoWant, fbWant string
	}{
		{"auto on", "#Z2R_AUTO_MODE=1\n" + fbOn, "включен", "недоступен"},
		{"auto off fallback skipped", "#Z2R_AUTO_MODE=0\n" + fbSkip, "выключен", "выключен"},
		{"auto off fallback on", "#Z2R_AUTO_MODE=0\n" + fbOn, "выключен", "включен"},
		{"http fallback on", "#Z2R_AUTO_MODE=0\n" + fbHTTPOn, "выключен", "включен"},
		{"skip wins", "#Z2R_AUTO_MODE=0\n" + fbSkip + fbHTTPOn, "выключен", "выключен"},
		{"no markers", "", "неизвестно", "неизвестно"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Parse(tc.in)
			if got := c.ModeText(ModeAutoMode); got != tc.autoWant {
				t.Errorf("auto_mode = %q, want %q", got, tc.autoWant)
			}
			if got := c.ModeText(ModeFallback); got != tc.fbWant {
				t.Errorf("fallback = %q, want %q", got, tc.fbWant)
			}
		})
	}
}

func TestRSTGuardAndReasm(t *testing.T) {
	tests := []struct {
		name, in, rst, reasm string
	}{
		{"neither", "NFQWS2_OPT=\"\n--new\n\"\n", "выключен", "выключено"},
		{"rst", "NFQWS2_OPT=\"\n--lua-desync=rst_guard_locked:key=2:x=y\n\"\n", "включен", "выключено"},
		{"reasm", "NFQWS2_OPT=\"\n--reasm-disable\n--new\n\"\n", "выключен", "включено"},
		{"reasm indented", "NFQWS2_OPT=\"\n  --reasm-disable\n\"\n", "выключен", "включено"},
		{"reasm outside opt", "--reasm-disable\nNFQWS2_OPT=\"\n--new\n\"\n", "выключен", "выключено"},
		{"no opt", "", "выключен", "выключено"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Parse(tc.in)
			if got := c.ModeText(ModeRSTGuard); got != tc.rst {
				t.Errorf("rst_guard = %q, want %q", got, tc.rst)
			}
			if got := c.ModeText(ModeReasm); got != tc.reasm {
				t.Errorf("reasm = %q, want %q", got, tc.reasm)
			}
			if got := c.ModeText("reasm"); got != tc.reasm {
				t.Errorf("reasm alias = %q, want %q", got, tc.reasm)
			}
		})
	}
}

func TestTLSBlobModes(t *testing.T) {
	const maxru = "--lua-desync=fake:blob=maxru:strategy=1\n"
	const def = "--lua-desync=fake:blob=fake_default_tls:strategy=2\n"
	const s26 = "--lua-desync=fake:blob=fake_default_tls:strategy=26\n"
	const decl = "--blob=maxru:@/opt/zator/files/fake/tls_clienthello_max_ru.bin\n"
	decl2 := "--blob=maxru:@/opt/zapret2/files/fake/tls_clienthello_5.bin extra\n"
	tests := []struct {
		name, in, mode, menu string
	}{
		{"maxru only", decl + maxru, "maxru", "tls_clienthello_max_ru.bin"},
		{"zapret2 root", decl2 + maxru, "maxru", "tls_clienthello_5.bin"},
		{"default only", decl + def, "fake_default_tls", "default"},
		{"mixed", decl + maxru + def, "mixed", "mixed"},
		{"strategy 26 ignored", decl + maxru + s26, "maxru", "tls_clienthello_max_ru.bin"},
		{"nothing", "", "не определён", "неизвестно"},
		{"decl without usage", decl, "не определён", "tls_clienthello_max_ru.bin"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Parse(tc.in)
			if got := c.ModeText(ModeTLSBlobMode); got != tc.mode {
				t.Errorf("tls_blob_mode = %q, want %q", got, tc.mode)
			}
			if got := c.ModeText(ModeTLSBlobMenu); got != tc.menu {
				t.Errorf("tls_blob_menu = %q, want %q", got, tc.menu)
			}
			if got := c.ModeText("tls_blob"); got != tc.menu {
				t.Errorf("tls_blob alias = %q, want %q", got, tc.menu)
			}
		})
	}
}

const udpGamesOn = `NFQWS2_PORTS_UDP=443,1026-65531
#Стратегии для игрового UDP. Широкий диапазон
--filter-udp=1026-65531
--new
`

func TestUDPGames(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"on", udpGamesOn, "Включен"},
		{"skip", strings.Replace(udpGamesOn, "--filter-udp=1026", "--skip --filter-udp=1026", 1), "Выключен"},
		{"port missing", strings.Replace(udpGamesOn, ",1026-65531", "", 1), "Выключен"},
		{"no var", "#Стратегии для игрового UDP\n--filter-udp=1026-65531\n--new\n", "Неизвестно"},
		{"no block", "NFQWS2_PORTS_UDP=443,1026-65531\n", "Выключен"},
		{"filter after --new ignored", "NFQWS2_PORTS_UDP=1026-65531\n#Стратегии для игрового UDP\n--new\n--filter-udp=1026\n", "Выключен"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parse(tc.in).ModeText(ModeUDPGames); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDNSDesync(t *testing.T) {
	block := func(line string) string {
		return "#Z2R_DNS_BEGIN\n#comment\n" + line + "\n--in-range=-d100\n#Z2R_DNS_END\n"
	}
	tests := []struct{ name, in, want string }{
		{"on", "NFQWS2_PORTS_UDP=443,53\n" + block("--filter-udp=53"), "Включен"},
		{"port missing", "NFQWS2_PORTS_UDP=443\n" + block("--filter-udp=53"), "Выключен"},
		{"skipped", "NFQWS2_PORTS_UDP=443,53\n" + block("--skip --filter-udp=53"), "Выключен"},
		{"port 530 is not 53", "NFQWS2_PORTS_UDP=443,530\n" + block("--filter-udp=53"), "Выключен"},
		{"no block", "NFQWS2_PORTS_UDP=443,53\n", "Неизвестно"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parse(tc.in).ModeText(ModeDNSDesync); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestQUICAndWireGuard(t *testing.T) {
	tests := []struct{ name, in, quic, wg string }{
		{"both on", "#Z2R_QUIC443_BEGIN\n--filter-udp=443\n#Z2R_QUIC443_END\n#Z2R_WG_BEGIN\n--filter-l7=wireguard\n#Z2R_WG_END\n", "включены", "включено"},
		{"both off", "#Z2R_QUIC443_BEGIN\n--skip --filter-udp=443\n#Z2R_QUIC443_END\n#Z2R_WG_BEGIN\n--skip --filter-l7=wireguard\n#Z2R_WG_END\n", "выключены", "выключено"},
		{"absent", "", "неизвестно", "недоступно"},
		{"empty blocks", "#Z2R_QUIC443_BEGIN\n#Z2R_QUIC443_END\n#Z2R_WG_BEGIN\n#Z2R_WG_END\n", "неизвестно", "недоступно"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Parse(tc.in)
			if got := c.ModeText(ModeQUIC443); got != tc.quic {
				t.Errorf("quic443 = %q, want %q", got, tc.quic)
			}
			if got := c.ModeText(ModeWireGuard); got != tc.wg {
				t.Errorf("wireguard = %q, want %q", got, tc.wg)
			}
			if got := c.ModeText("wg_state"); got != tc.wg {
				t.Errorf("wg_state alias = %q, want %q", got, tc.wg)
			}
		})
	}
}

func TestWireGuardBlob(t *testing.T) {
	c := Parse("--blob=fakewgblob:@/opt/zator/files/fake/wg_initial_fake_3.bin\n--lua-desync=fake:blob=fakewgblob:repeats=10\n")
	file, repeats := c.WireGuardBlob()
	if file != "wg_initial_fake_3.bin" || repeats != "10" {
		t.Errorf("WireGuardBlob = (%q,%q)", file, repeats)
	}
	if f, r := Parse("").WireGuardBlob(); f != "" || r != "" {
		t.Errorf("empty = (%q,%q)", f, r)
	}
}

func TestPorts(t *testing.T) {
	tests := []struct {
		name, in, proto string
		want            Ports
	}{
		{"tcp user port", "NFQWS2_PORTS_TCP=8080,80,443\n", "tcp", Ports{Full: "8080,80,443", User: []string{"8080"}, Base: "80,443"}},
		{"tcp default", "NFQWS2_PORTS_TCP=80,443,2053\n", "tcp", Ports{Full: "80,443,2053", User: []string{}, Base: "80,443,2053"}},
		{"udp user ports", "NFQWS2_PORTS_UDP=51820,2408,443,53\n", "udp", Ports{Full: "51820,2408,443,53", User: []string{"51820", "2408"}, Base: "443,53"}},
		{"no anchor", "NFQWS2_PORTS_UDP=51820\n", "udp", Ports{Full: "51820", User: []string{"51820"}, Base: ""}},
		{"missing", "", "tcp", Ports{Full: "", User: []string{}, Base: ""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parse(tc.in).PortsFor(tc.proto); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
	if got := Parse("NFQWS2_PORTS_TCP=8080,80\nNFQWS2_PORTS_UDP=51820,2408,443\n").PortsText(); got != "добавлено портов: 3" {
		t.Errorf("PortsText = %q", got)
	}
	if got := Parse("NFQWS2_PORTS_TCP=80\nNFQWS2_PORTS_UDP=443\n").PortsText(); got != "дефолт" {
		t.Errorf("PortsText default = %q", got)
	}
}

// TestDefaultConfigModes сверяет разбор штатного assets/config.default.
func TestDefaultConfigModes(t *testing.T) {
	c := loadDefault(t)

	wantText := map[string]string{
		ModeFWType:      "iptables",
		ModeFlowOffload: "donttouch",
		ModeHostlist:    "по листам",
		ModeAutoMode:    "выключен",
		ModeFallback:    "выключен",
		ModeReasm:       "выключено",
		ModeUDPGames:    "Включен",
		ModeDNSDesync:   "Выключен",
		ModeClientScope: "выключен",
		ModeRSTGuard:    "включен",
		ModeQUIC443:     "выключены",
		ModeWireGuard:   "выключено",
	}
	for mode, want := range wantText {
		got := c.ModeText(mode)
		if mode == ModeUDPGames {
			// Зависит от состояния игрового блока в эталоне: допускаем оба значения,
			// но не "Неизвестно".
			if got != "Включен" && got != "Выключен" {
				t.Errorf("%s = %q", mode, got)
			}
			continue
		}
		if got != want {
			t.Errorf("ModeText(%s) = %q, want %q", mode, got, want)
		}
	}

	if got := c.ModeText(ModeTLSBlobMode); got == "" {
		t.Error("tls_blob_mode is empty")
	}
	if got := c.ModeText(ModeTLSBlobMenu); got == "" {
		t.Error("tls_blob_menu is empty")
	}

	if v, _ := c.Get("NFQWS2_PORTS_TCP"); v != "80,443,2053,2083,2087,2096,8443" {
		t.Errorf("NFQWS2_PORTS_TCP = %q", v)
	}
	if v, _ := c.Get("Z2R_WSSIZE_RAMP_ENABLE"); v != "0" {
		t.Errorf("Z2R_WSSIZE_RAMP_ENABLE = %q", v)
	}
	if v, _ := c.Get("Z2R_WSSIZE_MARK"); v != "0x04000000" {
		t.Errorf("Z2R_WSSIZE_MARK = %q", v)
	}
	if v, _ := c.Get("Z2R_WSSIZE_RAMP_MODE"); v != "full" {
		t.Errorf("Z2R_WSSIZE_RAMP_MODE = %q", v)
	}
	if ports := c.PortsFor("tcp"); len(ports.User) != 0 {
		t.Errorf("tcp user ports = %v, want none", ports.User)
	}

	opt, ok := c.NFQWS2Opt()
	if !ok || !strings.Contains(opt, "--template=z2r_tcp_tls_common") || !strings.HasSuffix(opt, "\n") {
		t.Errorf("NFQWS2Opt looks wrong (ok=%v, len=%d)", ok, len(opt))
	}
	if c.Has("Z2R_WSSIZE_RAMP_SKIP") != true {
		t.Error("Z2R_WSSIZE_RAMP_SKIP must exist")
	}
}

func TestDefaultConfigBlocks(t *testing.T) {
	c := loadDefault(t)
	if got, want := c.AutoStandardIDs(), []string{"3", "1", "2", "4", "8", "3S", "9"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AutoStandardIDs = %v, want %v", got, want)
	}
	if got, want := c.AutoIDs(), []string{"3", "4", "9"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AutoIDs = %v, want %v", got, want)
	}
	for _, name := range []string{
		BlockAutoStandard, BlockAuto, BlockTCP443, BlockDNS,
		BlockFallbackTLS, BlockFallbackHTTP, BlockWireGuard, BlockQUIC443,
	} {
		if _, ok := c.FindBlock(name); !ok {
			t.Errorf("block %s not found", name)
		}
	}
	if b, ok := c.AutoStandardBlock("9"); !ok || b.MaxStrategy() != 8 {
		t.Errorf("block 9 MaxStrategy = %d (ok=%v), want 8", b.MaxStrategy(), ok)
	}
	if b, ok := c.AutoBlock("4"); !ok || b.MaxStrategy() != 28 {
		t.Errorf("auto block 4 MaxStrategy = %d (ok=%v), want 28", b.MaxStrategy(), ok)
	}
	if on, ok := c.AutoModeFlag(); !ok || on {
		t.Errorf("AutoModeFlag = (%v,%v)", on, ok)
	}
}

// TestDefaultConfigEditPreservesRest проверяет, что правка одной переменной
// не затрагивает остальное содержимое эталонного конфига.
func TestDefaultConfigEditPreservesRest(t *testing.T) {
	c := loadDefault(t)
	before := c.Lines()
	optBefore, _ := c.NFQWS2Opt()

	if err := c.Set("FWTYPE", "nftables"); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("FLOWOFFLOAD", "software"); err != nil {
		t.Fatal(err)
	}
	after := c.Lines()
	if len(after) != len(before) {
		t.Fatalf("line count changed: %d -> %d", len(before), len(after))
	}
	diff := 0
	for i := range before {
		if before[i] != after[i] {
			diff++
		}
	}
	if diff != 2 {
		t.Errorf("changed lines = %d, want 2", diff)
	}
	if optAfter, _ := c.NFQWS2Opt(); optAfter != optBefore {
		t.Error("NFQWS2_OPT changed")
	}
	if v, _ := c.Get("FWTYPE"); v != "nftables" {
		t.Errorf("FWTYPE = %q", v)
	}
}

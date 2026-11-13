package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// loadDefault загружает assets/config.default репозитория.
func loadDefault(t *testing.T) *Config {
	t.Helper()
	path := filepath.Join("..", "..", "assets", "config.default")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("assets/config.default недоступен: %v", err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDefaultProfiles(t *testing.T) {
	wantMax := []int{43, 43, 43, 28, 32, 32, 20, 43, 8, 20}
	profiles := DefaultProfiles()
	if len(profiles) != 10 {
		t.Fatalf("len = %d, want 10", len(profiles))
	}
	for i, p := range profiles {
		if p.ID != i+1 {
			t.Errorf("profile[%d].ID = %d", i, p.ID)
		}
		if p.MaxStrategy != wantMax[i] {
			t.Errorf("profile %d max = %d, want %d", p.ID, p.MaxStrategy, wantMax[i])
		}
		if p.Label == "" || p.Description == "" || p.Title == "" || len(p.Protos) == 0 {
			t.Errorf("profile %d has empty metadata: %+v", p.ID, p)
		}
	}
	if profiles[6].Label != "UDP Games" || !profiles[6].IsUDPGames {
		t.Errorf("profile 7 = %+v", profiles[6])
	}
	if !profiles[7].IsFallback || !profiles[8].IsFallback || profiles[0].IsFallback {
		t.Error("fallback flags are wrong")
	}
	if !profiles[9].IsDNSDesync {
		t.Error("profile 10 must be DNS desync")
	}
}

func TestProfileHelpers(t *testing.T) {
	protoTests := []struct {
		id   int
		want []string
	}{
		{1, []string{"tls", "http"}},
		{2, []string{"tls"}},
		{3, []string{"tls"}},
		{4, []string{"tls"}},
		{5, []string{"udp"}},
		{6, []string{"udp"}},
		{7, []string{"udp"}},
		{8, []string{"tls"}},
		{9, []string{"http"}},
		{10, []string{"udp"}},
		{0, nil},
		{11, nil},
	}
	for _, tc := range protoTests {
		if got := ProfileProtos(tc.id); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ProfileProtos(%d) = %v, want %v", tc.id, got, tc.want)
		}
	}
	if !ProfileHasProto(1, "http") || ProfileHasProto(2, "http") || ProfileHasProto(99, "tls") {
		t.Error("ProfileHasProto wrong")
	}
	if DefaultMaxStrategy(4) != 28 || DefaultMaxStrategy(0) != 0 || DefaultMaxStrategy(11) != 0 {
		t.Error("DefaultMaxStrategy wrong")
	}
	if ProfileTitle(3) != "TCP 443 (RKN)" || ProfileTitle(42) != "Профиль 42" {
		t.Error("ProfileTitle wrong")
	}
	if ProfileLabel(10) != "DNS Антиспуф" {
		t.Error("ProfileLabel wrong")
	}
	if ValidProfile(0) || !ValidProfile(1) || !ValidProfile(10) || ValidProfile(11) {
		t.Error("ValidProfile wrong")
	}
	if _, ok := DefaultProfile(11); ok {
		t.Error("DefaultProfile(11) must fail")
	}
	// Возвращаемые срезы — копии.
	p := ProfileProtos(1)
	p[0] = "changed"
	if ProfileProtos(1)[0] != "tls" {
		t.Error("ProfileProtos must return a copy")
	}
}

// Шаблон z2r_tcp_tls_common в актуальном config.default содержит strategy=44 —
// разобранные максимумы TCP-профилей 1/2/3/8 и DefaultMaxStrategy совпадают.
func TestProfileMaxStrategyFromDefaultConfig(t *testing.T) {
	c := loadDefault(t)
	want := []int{44, 44, 44, 28, 32, 32, 20, 44, 8, 20}
	for i, w := range want {
		id := i + 1
		if got := c.ProfileMaxStrategy(id); got != w {
			t.Errorf("ProfileMaxStrategy(%d) = %d, want %d", id, got, w)
		}
	}
	for i, p := range c.Profiles() {
		if p.MaxStrategy != want[i] {
			t.Errorf("Profiles()[%d].MaxStrategy = %d, want %d", i, p.MaxStrategy, want[i])
		}
	}
}

func TestProfileMaxStrategyKeyed(t *testing.T) {
	const cfg = `NFQWS2_OPT="
--lua-init=@/x.lua
--template=common
--lua-desync=fake:strategy=3 --lua-desync=multisplit:strategy=30
--new
--filter-tcp=443
--lua-desync=multisplit:strategy=5
--new
--filter-tcp=443
--in-range=-s32768 --lua-desync=circular_locked:key=2:payload=tls_client_hello
--import=common
--new
--filter-tcp=443
--lua-desync=circular_quality:key=12:fails=3
--lua-desync=fake:strategy=99
"
`
	c := Parse(cfg)
	if got := c.ProfileMaxStrategy(2); got != 30 {
		t.Errorf("keyed profile 2 = %d, want 30 (template via import)", got)
	}
	// key=12 не должен совпасть с key=1.
	if got := c.ProfileMaxStrategy(1); got != 5 {
		t.Errorf("profile 1 (positional) = %d, want 5", got)
	}
	if got := c.ProfileMaxStrategy(3); got != 99 {
		t.Errorf("profile 3 (positional) = %d, want 99", got)
	}
	if got := c.ProfileMaxStrategy(7); got != 0 {
		t.Errorf("profile 7 (absent) = %d, want 0", got)
	}
}

func TestProfileMaxStrategyFallbackMarkers(t *testing.T) {
	const cfg = `NFQWS2_OPT="
--filter-tcp=80
--lua-desync=fake:strategy=1
--new
  #Z2R_FALLBACK_BEGIN
--skip --filter-tcp=443
--lua-desync=fake:strategy=17
  #Z2R_FALLBACK_END
--new
  #Z2R_FALLBACK_HTTP_BEGIN
--skip --filter-tcp=80
--lua-desync=fake:strategy=6
  #Z2R_FALLBACK_HTTP_END
"
`
	c := Parse(cfg)
	if got := c.ProfileMaxStrategy(ProfileFallbackT); got != 17 {
		t.Errorf("profile 8 = %d, want 17", got)
	}
	if got := c.ProfileMaxStrategy(ProfileFallbackH); got != 6 {
		t.Errorf("profile 9 = %d, want 6", got)
	}
}

func TestProfileMaxStrategyOrDefault(t *testing.T) {
	empty := Parse("FWTYPE=iptables\n")
	if got := empty.ProfileMaxStrategy(1); got != 0 {
		t.Errorf("raw = %d, want 0", got)
	}
	if got := empty.ProfileMaxStrategyOrDefault(1); got != 44 {
		t.Errorf("or-default = %d, want 44", got)
	}
	for i, p := range empty.Profiles() {
		if p.MaxStrategy != DefaultMaxStrategy(i+1) {
			t.Errorf("Profiles()[%d] = %d", i, p.MaxStrategy)
		}
	}
}

func TestCSVHelpers(t *testing.T) {
	if !CSVContains("a,b,c", "b") || CSVContains("a,b,c", "d") || CSVContains("", "a") {
		t.Error("CSVContains wrong")
	}
	addTests := []struct{ csv, tokens, want string }{
		{"443", "53", "443,53"},
		{"443,53", "53", "443,53"},
		{"", "1,2", "1,2"},
		{"443", "1,443,2", "443,1,2"},
		{"443", "", "443"},
	}
	for _, tc := range addTests {
		if got := CSVAdd(tc.csv, tc.tokens); got != tc.want {
			t.Errorf("CSVAdd(%q,%q) = %q, want %q", tc.csv, tc.tokens, got, tc.want)
		}
	}
	rmTests := []struct{ csv, tokens, want string }{
		{"443,53,80", "53", "443,80"},
		{"443,53", "53,443", ""},
		{"443,,53", "x", "443,53"},
		{"", "53", ""},
	}
	for _, tc := range rmTests {
		if got := CSVRemove(tc.csv, tc.tokens); got != tc.want {
			t.Errorf("CSVRemove(%q,%q) = %q, want %q", tc.csv, tc.tokens, got, tc.want)
		}
	}
}

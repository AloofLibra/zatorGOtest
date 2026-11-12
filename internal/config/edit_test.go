package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// reparse проверяет, что результат правок переживает Save/Load.
func reparse(t *testing.T, c *Config) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := c.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// toggleCase описывает пару «установить / прочитать» для переключателя.
type toggleCase struct {
	name string
	set  func(*Config, bool) error
	text func(*Config) string
	on   string
	off  string
}

func toggleCases() []toggleCase {
	return []toggleCase{
		{"fallback", (*Config).SetFallback, (*Config).FallbackText, "включен", "выключен"},
		{"wireguard", (*Config).SetWireGuard, (*Config).WireGuardText, "включено", "выключено"},
		{"quic443", (*Config).SetQUIC443, (*Config).QUIC443Text, "включены", "выключены"},
		{"dns_desync", (*Config).SetDNSDesync, (*Config).DNSDesyncText, "Включен", "Выключен"},
		{"udp_games", (*Config).SetUDPGames, (*Config).UDPGamesText, "Включен", "Выключен"},
		{"rst_guard", (*Config).SetRSTGuard, (*Config).RSTGuardText, "включен", "выключен"},
		{"reasm", (*Config).SetReasm, (*Config).ReasmText, "включено", "выключено"},
	}
}

func TestToggleRoundTrip(t *testing.T) {
	for _, tc := range toggleCases() {
		t.Run(tc.name, func(t *testing.T) {
			c := loadDefault(t)
			// Сначала включаем, затем выключаем, затем снова включаем:
			// повторные вызовы идемпотентны, текст читается после перезагрузки.
			for _, step := range []struct {
				on   bool
				want string
			}{{true, tc.on}, {true, tc.on}, {false, tc.off}, {false, tc.off}, {true, tc.on}} {
				if err := tc.set(c, step.on); err != nil {
					t.Fatalf("set(%v): %v", step.on, err)
				}
				if got := tc.text(c); got != step.want {
					t.Fatalf("после set(%v): %q, want %q", step.on, got, step.want)
				}
				if got := tc.text(reparse(t, c)); got != step.want {
					t.Fatalf("после перезагрузки: %q, want %q", got, step.want)
				}
			}
		})
	}
}

func TestToggleOffRestoresOriginalText(t *testing.T) {
	for _, tc := range toggleCases() {
		t.Run(tc.name, func(t *testing.T) {
			c := loadDefault(t)
			orig := tc.text(c)
			on := orig == tc.on
			if err := tc.set(c, !on); err != nil {
				t.Fatal(err)
			}
			if err := tc.set(c, on); err != nil {
				t.Fatal(err)
			}
			if got := tc.text(c); got != orig {
				t.Fatalf("после возврата: %q, want %q", got, orig)
			}
		})
	}
}

func TestTogglesWithoutBlocksReturnError(t *testing.T) {
	c := Parse("NFQWS2_PORTS_TCP=\"80,443\"\nNFQWS2_PORTS_UDP=\"443\"\n")
	if err := c.SetFallback(true); err == nil {
		t.Error("SetFallback без блоков должен вернуть ошибку")
	}
	if err := c.SetWireGuard(true); err == nil {
		t.Error("SetWireGuard без блока должен вернуть ошибку")
	}
	if err := c.SetQUIC443(true); err == nil {
		t.Error("SetQUIC443 без блока должен вернуть ошибку")
	}
	if err := c.SetDNSDesync(true); err == nil {
		t.Error("SetDNSDesync без блока должен вернуть ошибку")
	}
	if err := c.SetUDPGames(true); err == nil {
		t.Error("SetUDPGames без блока должен вернуть ошибку")
	}
}

func TestDNSDesyncAndUDPGamesTouchPorts(t *testing.T) {
	c := loadDefault(t)
	if err := c.SetDNSDesync(true); err != nil {
		t.Fatal(err)
	}
	if !CSVContains(c.PortsFor("udp").Full, DNSPorts) {
		t.Errorf("порт %s не добавлен: %q", DNSPorts, c.PortsFor("udp").Full)
	}
	if err := c.SetDNSDesync(false); err != nil {
		t.Fatal(err)
	}
	if CSVContains(c.PortsFor("udp").Full, DNSPorts) {
		t.Errorf("порт %s не убран: %q", DNSPorts, c.PortsFor("udp").Full)
	}
	if err := c.SetUDPGames(true); err != nil {
		t.Fatal(err)
	}
	if !CSVContains(c.PortsFor("udp").Full, UDPGamesPorts) {
		t.Errorf("диапазон %s не добавлен: %q", UDPGamesPorts, c.PortsFor("udp").Full)
	}
	if err := c.SetUDPGames(false); err != nil {
		t.Fatal(err)
	}
	if CSVContains(c.PortsFor("udp").Full, UDPGamesPorts) {
		t.Errorf("диапазон %s не убран: %q", UDPGamesPorts, c.PortsFor("udp").Full)
	}
}

func TestSetHostlistAuto(t *testing.T) {
	c := loadDefault(t)
	if err := c.SetHostlistAuto(true); err != nil {
		t.Fatal(err)
	}
	if !c.HostlistAuto() || !reparse(t, c).HostlistAuto() {
		t.Fatal("hostlist=авто не включился")
	}
	if err := c.SetHostlistAuto(false); err != nil {
		t.Fatal(err)
	}
	if c.HostlistAuto() {
		t.Fatal("hostlist=авто не выключился")
	}
}

func TestSetAutoModeRoundTrip(t *testing.T) {
	c := loadDefault(t)
	orig := c.String()
	if err := c.SetAutoMode(true); err != nil {
		t.Fatal(err)
	}
	if c.AutoModeText() != "включен" {
		t.Fatalf("AutoModeText = %q", c.AutoModeText())
	}
	if c.FallbackText() != "недоступен" {
		t.Errorf("при авторежиме fallback = %q, want недоступен", c.FallbackText())
	}
	if c.String() == orig {
		t.Error("авторежим не изменил конфиг")
	}
	if got := reparse(t, c).AutoModeText(); got != "включен" {
		t.Errorf("после перезагрузки: %q", got)
	}
	if err := c.SetAutoMode(false); err != nil {
		t.Fatal(err)
	}
	if c.AutoModeText() != "выключен" {
		t.Fatalf("AutoModeText = %q", c.AutoModeText())
	}
	// Выключенный авторежим возвращает исходное содержимое.
	if c.String() != orig {
		t.Error("выключение авторежима не вернуло исходный конфиг")
	}
}

func TestSetAutoModeNoMarker(t *testing.T) {
	c := Parse("FOO=1\n")
	if err := c.SetAutoMode(true); err == nil {
		t.Error("без маркера #Z2R_AUTO_MODE ожидалась ошибка")
	}
}

func TestRSTGuardIgnoresComments(t *testing.T) {
	c := Parse("# rst_guard_locked:key=foo\nX=1\n")
	if got := c.RSTGuardText(); got != "выключен" {
		t.Errorf("закомментированная строка: %q", got)
	}
	c = Parse("  --lua-desync=rst_guard_locked:key=foo\nX=1\n")
	if got := c.RSTGuardText(); got != "включен" {
		t.Errorf("активная строка: %q", got)
	}
	if err := c.SetRSTGuard(false); err != nil {
		t.Fatal(err)
	}
	if got := c.RSTGuardText(); got != "выключен" {
		t.Errorf("после выключения: %q", got)
	}
	if !strings.Contains(c.String(), "# ") && !strings.Contains(c.String(), "#") {
		t.Error("строка должна быть закомментирована, а не удалена")
	}
	if err := c.SetRSTGuard(true); err != nil {
		t.Fatal(err)
	}
	if got := c.RSTGuardText(); got != "включен" {
		t.Errorf("после включения: %q", got)
	}
}

func TestSetReasmIsIdempotent(t *testing.T) {
	c := loadDefault(t)
	if err := c.SetReasm(true); err != nil {
		t.Fatal(err)
	}
	once := c.String()
	if err := c.SetReasm(true); err != nil {
		t.Fatal(err)
	}
	if c.String() != once {
		t.Error("повторное включение reasm изменило конфиг")
	}
	if n := strings.Count(c.String(), "--reasm-disable"); n != 1 {
		t.Errorf("--reasm-disable встречается %d раз(а)", n)
	}
	if err := c.SetReasm(false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.String(), "--reasm-disable") {
		t.Error("--reasm-disable не удалён")
	}
}

func TestAddUserPorts(t *testing.T) {
	c := Parse("NFQWS2_PORTS_TCP=\"80,443,2053\"\nNFQWS2_PORTS_UDP=\"443\"\n")
	added, skipped, err := c.AddUserPorts("tcp", []string{"8080", "9000-9100", "443"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(added, []string{"8080", "9000-9100"}) || !reflect.DeepEqual(skipped, []string{"443"}) {
		t.Fatalf("added=%v skipped=%v", added, skipped)
	}
	p := c.PortsFor("tcp")
	if !reflect.DeepEqual(p.User, []string{"8080", "9000-9100"}) || p.Base != "80,443,2053" {
		t.Fatalf("ports = %+v", p)
	}
	if c.PortsText() != "добавлено портов: 2" {
		t.Errorf("PortsText = %q", c.PortsText())
	}
	// Повторное добавление пропускается.
	added, skipped, err = c.AddUserPorts("tcp", []string{"8080"})
	if err != nil || len(added) != 0 || !reflect.DeepEqual(skipped, []string{"8080"}) {
		t.Fatalf("повтор: added=%v skipped=%v err=%v", added, skipped, err)
	}
	if got := reparse(t, c).PortsFor("tcp").User; !reflect.DeepEqual(got, []string{"8080", "9000-9100"}) {
		t.Errorf("после перезагрузки: %v", got)
	}
}

func TestRemoveUserPort(t *testing.T) {
	c := Parse("NFQWS2_PORTS_TCP=\"8080,9000-9100,80,443\"\nNFQWS2_PORTS_UDP=\"443\"\n")
	found, err := c.RemoveUserPort("tcp", "8080")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if got := c.PortsFor("tcp"); !reflect.DeepEqual(got.User, []string{"9000-9100"}) || got.Base != "80,443" {
		t.Fatalf("ports = %+v", got)
	}
	// Штатный порт не является пользовательским и не удаляется.
	found, err = c.RemoveUserPort("tcp", "443")
	if err != nil || found {
		t.Fatalf("штатный порт: found=%v err=%v", found, err)
	}
	if c.PortsFor("tcp").Base != "80,443" {
		t.Errorf("штатные порты изменены: %q", c.PortsFor("tcp").Full)
	}
	if found, _ := c.RemoveUserPort("tcp", "12345"); found {
		t.Error("несуществующий порт найден")
	}
}

func TestSetPortToken(t *testing.T) {
	c := Parse("NFQWS2_PORTS_UDP=\"443\"\n")
	if err := c.SetPortToken("udp", "53", true); err != nil {
		t.Fatal(err)
	}
	if !CSVContains(c.PortsFor("udp").Full, "53") {
		t.Fatalf("порт не добавлен: %q", c.PortsFor("udp").Full)
	}
	if err := c.SetPortToken("udp", "53", true); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(","+c.PortsFor("udp").Full+",", ",53,"); n != 1 {
		t.Errorf("порт продублирован (%d): %q", n, c.PortsFor("udp").Full)
	}
	if err := c.SetPortToken("udp", "53", false); err != nil {
		t.Fatal(err)
	}
	if CSVContains(c.PortsFor("udp").Full, "53") {
		t.Errorf("порт не убран: %q", c.PortsFor("udp").Full)
	}
}

package config

import (
	"regexp"
	"strconv"
	"strings"
)

// Режимы для ModeText (имена совпадают с config_mode_text в lib/config.sh;
// в скобках — допустимые синонимы).
const (
	ModeFlowOffload = "flowoffload"
	ModeFWType      = "fwtype"
	ModeHostlist    = "hostlist"
	ModeFallback    = "fallback"
	ModeAutoMode    = "auto_mode"
	ModeRSTGuard    = "rst_guard"
	ModeReasm       = "reasm_disable" // синоним: "reasm"
	ModeUDPGames    = "udp_games"
	ModeDNSDesync   = "dns_desync"
	ModeTLSBlobMode = "tls_blob_mode"
	ModeTLSBlobMenu = "tls_blob_menu" // синоним: "tls_blob"
	ModeQUIC443     = "quic443"
	ModeWireGuard   = "wireguard" // синоним: "wg_state"
	ModeClientScope = "client_scope"
	ModePorts       = "ports"
	unknownText     = "неизвестно"
	unknownTextCap  = "Неизвестно"
	undefinedText   = "не определён"
	noConfigText    = "нет config"
	udpGamesHeader  = "#Стратегии для игрового UDP"
)

var (
	udpGamesPortRe     = regexp.MustCompile(`(^|,)1026-65531(,|$)`)
	dnsPortRe          = regexp.MustCompile(`(^|,)53(,|$)`)
	skipLineRe         = regexp.MustCompile(`^\s*--skip(\s|$)`)
	filterTCPLineRe    = regexp.MustCompile(`^\s*--filter-tcp=`)
	reasmDisableRe     = regexp.MustCompile(`^\s*--reasm-disable`)
	udpGamesOffRe      = regexp.MustCompile(`^\s*--skip\s+--filter-udp=1026`)
	udpGamesOnRe       = regexp.MustCompile(`^\s*--filter-udp=1026`)
	dnsOnRe            = regexp.MustCompile(`^\s*--filter-udp=53\s*$`)
	dnsOffRe           = regexp.MustCompile(`^\s*--skip\s+--filter-udp=53\s*$`)
	quicOnRe           = regexp.MustCompile(`^\s*--filter-udp=443\s*$`)
	quicOffRe          = regexp.MustCompile(`^\s*--skip\s+--filter-udp=443\s*$`)
	wgSkipRe           = regexp.MustCompile(`^\s*--skip\s+--filter-l7=wireguard\s*$`)
	blobMaxruFileRe    = regexp.MustCompile(`--blob=maxru:@/opt/(?:zapret2|zator)/files/fake/(\S+)`)
	wgBlobRe           = regexp.MustCompile(`--blob=fakewgblob:@/opt/(?:zapret2|zator)/files/fake/(\S+)`)
	wgRepeatsRe        = regexp.MustCompile(`blob=fakewgblob:repeats=([0-9]+)`)
	rstGuardMarkerText = "--lua-desync=rst_guard_locked:key="
)

// ModeText возвращает человекочитаемое состояние режима для CLI/WebUI на
// русском языке — те же строки, что отдаёт config_mode_text. Для nil-конфига
// возвращаются значения «конфиг не найден» ("нет config" для rst_guard,
// "не определён" для tls_blob_mode, иначе "неизвестно").
func ModeText(c *Config, mode string) string {
	if c == nil {
		switch mode {
		case ModeRSTGuard:
			return noConfigText
		case ModeTLSBlobMode:
			return undefinedText
		}
		return unknownText
	}
	return c.ModeText(mode)
}

// ModeText возвращает текст состояния режима; см. функцию ModeText.
func (c *Config) ModeText(mode string) string {
	switch mode {
	case ModeFlowOffload:
		return c.FlowOffload()
	case ModeFWType:
		return c.FWType()
	case ModeHostlist:
		return c.HostlistText()
	case ModeFallback:
		return c.FallbackText()
	case ModeAutoMode:
		return c.AutoModeText()
	case ModeRSTGuard:
		return c.RSTGuardText()
	case ModeReasm, "reasm":
		return c.ReasmText()
	case ModeUDPGames:
		return c.UDPGamesText()
	case ModeDNSDesync:
		return c.DNSDesyncText()
	case ModeTLSBlobMode:
		return c.TLSBlobModeValue()
	case ModeTLSBlobMenu, "tls_blob":
		return c.TLSBlobMenuValue()
	case ModeQUIC443:
		return c.QUIC443Text()
	case ModeWireGuard, "wg_state":
		return c.WireGuardText()
	case ModeClientScope:
		return c.ClientScopeText()
	case ModePorts:
		return c.PortsText()
	}
	return unknownText
}

// FWType возвращает FWTYPE (iptables|nftables) или "неизвестно".
func (c *Config) FWType() string {
	if v, ok := c.Get("FWTYPE"); ok && v != "" {
		return v
	}
	return unknownText
}

// FlowOffload возвращает FLOWOFFLOAD (donttouch|none|software|hardware) или "неизвестно".
func (c *Config) FlowOffload() string {
	if v, ok := c.Get("FLOWOFFLOAD"); ok && v != "" {
		return v
	}
	return unknownText
}

// HostlistText: "авто" (MODE_FILTER=autohostlist), "по листам" (hostlist), иначе "неизвестно".
func (c *Config) HostlistText() string {
	switch c.GetDefault("MODE_FILTER", "") {
	case "autohostlist":
		return "авто"
	case "hostlist":
		return "по листам"
	}
	return unknownText
}

// HostlistAuto сообщает, включён ли автоматический hostlist.
func (c *Config) HostlistAuto() bool { return c.GetDefault("MODE_FILTER", "") == "autohostlist" }

// AutoModeText: "включен" | "выключен" | "неизвестно" по маркеру #Z2R_AUTO_MODE.
func (c *Config) AutoModeText() string {
	on, ok := c.AutoModeFlag()
	switch {
	case !ok:
		return unknownText
	case on:
		return "включен"
	}
	return "выключен"
}

// FallbackText: "недоступен" (авторежим включён) | "выключен" (есть --skip) |
// "включен" (есть --filter-tcp=) | "неизвестно".
func (c *Config) FallbackText() string {
	if on, ok := c.AutoModeFlag(); ok && on {
		return "недоступен"
	}
	var body []string
	for _, name := range []string{BlockFallbackTLS, BlockFallbackHTTP} {
		if b, ok := c.FindBlock(name); ok {
			body = append(body, b.Lines...)
		}
	}
	if anyLine(body, skipLineRe) {
		return "выключен"
	}
	if anyLine(body, filterTCPLineRe) {
		return "включен"
	}
	return unknownText
}

// RSTGuardText: "включен" если в конфиге есть незакомментированная строка
// с rst_guard_locked:key=, иначе "выключен".
func (c *Config) RSTGuardText() string {
	for _, ln := range c.lines {
		if strings.Contains(ln, rstGuardMarkerText) && !strings.HasPrefix(strings.TrimSpace(ln), "#") {
			return "включен"
		}
	}
	return "выключен"
}

// ReasmText: "включено" если в NFQWS2_OPT есть строка --reasm-disable, иначе "выключено".
func (c *Config) ReasmText() string {
	if e, ok := c.find(OptVar); ok {
		if anyLine(c.lines[e.start:e.end+1], reasmDisableRe) {
			return "включено"
		}
	}
	return "выключено"
}

// UDPGamesText: "Включен" | "Выключен" | "Неизвестно" (нет NFQWS2_PORTS_UDP).
func (c *Config) UDPGamesText() string {
	ports, ok := c.Get("NFQWS2_PORTS_UDP")
	if !ok {
		return unknownTextCap
	}
	if udpGamesPortRe.MatchString(ports) && c.udpGamesActive() {
		return "Включен"
	}
	return "Выключен"
}

// udpGamesActive: блок «Стратегии для игрового UDP» (до ближайшего --new)
// содержит активный --filter-udp=1026 и не содержит --skip --filter-udp=1026.
func (c *Config) udpGamesActive() bool {
	var blk []string
	started := false
	for _, ln := range c.lines {
		if !started {
			if strings.Contains(ln, udpGamesHeader) {
				started = true
				blk = append(blk, ln)
			}
			continue
		}
		blk = append(blk, ln)
		if newLineRe.MatchString(ln) {
			break
		}
	}
	if anyLine(blk, udpGamesOffRe) {
		return false
	}
	return anyLine(blk, udpGamesOnRe)
}

// DNSDesyncText: "Включен" | "Выключен" | "Неизвестно" (нет блока #Z2R_DNS_*).
func (c *Config) DNSDesyncText() string {
	b, ok := c.FindBlock(BlockDNS)
	if !ok {
		return unknownTextCap
	}
	ports, _ := c.Get("NFQWS2_PORTS_UDP")
	if anyLine(b.Lines, dnsOnRe) && !anyLine(b.Lines, dnsOffRe) && dnsPortRe.MatchString(ports) {
		return "Включен"
	}
	return "Выключен"
}

// QUIC443Text: "включены" | "выключены" | "неизвестно" по блоку #Z2R_QUIC443_*.
func (c *Config) QUIC443Text() string {
	b, ok := c.FindBlock(BlockQUIC443)
	if !ok {
		return unknownText
	}
	switch {
	case anyLine(b.Lines, quicOnRe):
		return "включены"
	case anyLine(b.Lines, quicOffRe):
		return "выключены"
	}
	return unknownText
}

// WireGuardText: "включено" | "выключено" | "недоступно" (нет блока #Z2R_WG_*).
func (c *Config) WireGuardText() string {
	b, ok := c.FindBlock(BlockWireGuard)
	if !ok {
		return "недоступно"
	}
	if anyLine(b.Lines, wgSkipRe) {
		return "выключено"
	}
	for _, ln := range b.Lines {
		if strings.Contains(ln, "--filter-l7=wireguard") {
			return "включено"
		}
	}
	return "недоступно"
}

// WireGuardBlob возвращает имя файла блоба WG и число повторов (оба могут быть "").
func (c *Config) WireGuardBlob() (file, repeats string) {
	for _, ln := range c.lines {
		if file == "" {
			if m := wgBlobRe.FindStringSubmatch(ln); m != nil {
				file = m[1]
			}
		}
		if repeats == "" {
			if m := wgRepeatsRe.FindStringSubmatch(ln); m != nil {
				repeats = m[1]
			}
		}
	}
	return file, repeats
}

// ClientScopeText: "включен" если CLIENT_SCOPE_ENABLE=1, иначе "выключен".
func (c *Config) ClientScopeText() string {
	if c.GetDefault("CLIENT_SCOPE_ENABLE", "0") == "1" {
		return "включен"
	}
	return "выключен"
}

// TLSBlobModeValue определяет, какой TLS-блоб используют стратегии:
// "maxru" | "fake_default_tls" | "mixed" | "не определён".
// Стратегии с strategy=26 (seqovl-паттерн) не учитываются.
func (c *Config) TLSBlobModeValue() string {
	var maxru, def bool
	for _, ln := range c.lines {
		if !strings.Contains(ln, "--lua-desync=") || strings.Contains(ln, "strategy=26") {
			continue
		}
		if strings.Contains(ln, "blob=maxru") {
			maxru = true
		}
		if strings.Contains(ln, "blob=fake_default_tls") {
			def = true
		}
	}
	switch {
	case maxru && !def:
		return "maxru"
	case def && !maxru:
		return "fake_default_tls"
	case def && maxru:
		return "mixed"
	}
	return undefinedText
}

// TLSBlobMenuValue — значение для меню/WebUI: "default" (fake_default_tls),
// "mixed", имя файла блоба maxru либо "неизвестно".
func (c *Config) TLSBlobMenuValue() string {
	switch c.TLSBlobModeValue() {
	case "fake_default_tls":
		return "default"
	case "mixed":
		return "mixed"
	}
	if f := c.TLSBlobFile(); f != "" {
		return f
	}
	return unknownText
}

// TLSBlobFile возвращает имя файла из первой декларации --blob=maxru:@…/fake/<файл>.
func (c *Config) TLSBlobFile() string {
	for _, ln := range c.lines {
		if m := blobMaxruFileRe.FindStringSubmatch(ln); m != nil {
			return m[1]
		}
	}
	return ""
}

// Ports описывает список портов NFQWS2_PORTS_{TCP,UDP}.
type Ports struct {
	Full string   `json:"full"` // значение переменной целиком
	User []string `json:"user"` // пользовательские порты (левее якоря)
	Base string   `json:"base"` // штатные порты (от якоря и правее)
}

// PortsFor возвращает разбор портов: proto "tcp" (якорь 80) или "udp" (якорь 443).
func (c *Config) PortsFor(proto string) Ports {
	name, anchor := "NFQWS2_PORTS_TCP", "80"
	if proto == "udp" {
		name, anchor = "NFQWS2_PORTS_UDP", "443"
	}
	full := c.GetDefault(name, "")
	p := Ports{Full: full, User: []string{}}
	var base []string
	started := false
	for _, tok := range strings.Split(full, ",") {
		if tok == anchor {
			started = true
		}
		switch {
		case started:
			base = append(base, tok)
		case tok != "":
			p.User = append(p.User, tok)
		}
	}
	p.Base = strings.Join(base, ",")
	return p
}

// PortsText: "добавлено портов: N" либо "дефолт".
func (c *Config) PortsText() string {
	n := len(c.PortsFor("tcp").User) + len(c.PortsFor("udp").User)
	if n == 0 {
		return "дефолт"
	}
	return "добавлено портов: " + strconv.Itoa(n)
}

func anyLine(lines []string, re *regexp.Regexp) bool {
	for _, ln := range lines {
		if re.MatchString(ln) {
			return true
		}
	}
	return false
}

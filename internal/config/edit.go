package config

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Редакторы режимов: порт backup_smart_set_* / config_set_auto_mode из CLI.
// Все они меняют только строки конфига (переключают `--skip`, порты, маркеры)
// и не пишут файл — вызывающий код сам вызывает Save.

var (
	skipStripRe    = regexp.MustCompile(`^(\s*)--skip(\s+|$)`)
	filterTCPAnyRe = regexp.MustCompile(`^\s*(--skip\s+)?--filter-tcp=`)
	filterWGRe     = regexp.MustCompile(`^\s*(--skip\s+)?--filter-l7=wireguard\s*$`)
	filterQUICRe   = regexp.MustCompile(`^\s*(--skip\s+)?--filter-udp=443\s*$`)
	filterDNSRe    = regexp.MustCompile(`^\s*(--skip\s+)?--filter-udp=53\s*$`)
	filterGamesRe  = regexp.MustCompile(`^\s*(--skip\s+)?--filter-udp=1026`)
)

// UDPGamesPorts — диапазон портов игрового UDP (профиль 7).
const UDPGamesPorts = "1026-65531"

// setSkipPrefix добавляет (skip=true) или убирает `--skip` в начале строки.
func setSkipPrefix(line string, skip bool) string {
	has := skipStripRe.MatchString(line)
	switch {
	case skip && !has:
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]
		return indent + "--skip " + trimmed
	case !skip && has:
		return skipStripRe.ReplaceAllString(line, "$1")
	}
	return line
}

// toggleSkipRange в строках [from, to) (кроме комментариев), подходящих под
// match, включает (enable) или выключает блок. Возвращает число совпадений.
func (c *Config) toggleSkipRange(from, to int, match *regexp.Regexp, enable bool) int {
	n := 0
	for i := from; i < to && i < len(c.lines); i++ {
		ln := c.lines[i]
		if strings.HasPrefix(strings.TrimSpace(ln), "#") || !match.MatchString(ln) {
			continue
		}
		c.lines[i] = setSkipPrefix(ln, !enable)
		n++
	}
	if n > 0 {
		c.reindex()
	}
	return n
}

// toggleSkipBlock применяет toggleSkipRange к телу блока name.
func (c *Config) toggleSkipBlock(name string, match *regexp.Regexp, enable bool) (int, error) {
	b, ok := c.FindBlock(name)
	if !ok {
		return 0, fmt.Errorf("%w: block %s", ErrNotFound, name)
	}
	n := c.toggleSkipRange(b.Begin+1, b.End, match, enable)
	if n == 0 {
		return 0, fmt.Errorf("%w: no filter line in block %s", ErrNotFound, name)
	}
	return n, nil
}

// SetPortToken добавляет или убирает токен в списке портов NFQWS2_PORTS_{TCP,UDP}
// (proto "tcp"|"udp"). Токен добавляется в конец списка.
func (c *Config) SetPortToken(proto, token string, present bool) error {
	name := "NFQWS2_PORTS_TCP"
	if proto == "udp" {
		name = "NFQWS2_PORTS_UDP"
	} else if proto != "tcp" {
		return fmt.Errorf("%w: proto %q", ErrInvalid, proto)
	}
	cur := c.GetDefault(name, "")
	var next string
	if present {
		next = CSVAdd(cur, token)
	} else {
		next = CSVRemove(cur, token)
	}
	if next == cur {
		return nil
	}
	return c.Set(name, next)
}

// AddUserPorts добавляет пользовательские порты в начало списка (левее якоря).
// Возвращает добавленные и пропущенные токены.
func (c *Config) AddUserPorts(proto string, tokens []string) (added, skipped []string, err error) {
	name := "NFQWS2_PORTS_TCP"
	if proto == "udp" {
		name = "NFQWS2_PORTS_UDP"
	} else if proto != "tcp" {
		return nil, nil, fmt.Errorf("%w: proto %q", ErrInvalid, proto)
	}
	cur := c.GetDefault(name, "")
	var fresh []string
	for _, t := range tokens {
		if t == "" || CSVContains(cur, t) || containsStr(fresh, t) {
			skipped = append(skipped, t)
			continue
		}
		fresh = append(fresh, t)
		added = append(added, t)
	}
	if len(fresh) == 0 {
		return added, skipped, nil
	}
	next := strings.Join(fresh, ",")
	if cur != "" {
		next += "," + cur
	}
	return added, skipped, c.Set(name, next)
}

// RemoveUserPort убирает пользовательский порт (только левее якоря).
// found=false, если порта нет среди пользовательских.
func (c *Config) RemoveUserPort(proto, token string) (found bool, err error) {
	if proto != "tcp" && proto != "udp" {
		return false, fmt.Errorf("%w: proto %q", ErrInvalid, proto)
	}
	if !containsStr(c.PortsFor(proto).User, token) {
		return false, nil
	}
	name := "NFQWS2_PORTS_TCP"
	if proto == "udp" {
		name = "NFQWS2_PORTS_UDP"
	}
	cur := c.GetDefault(name, "")
	user := c.PortsFor(proto).User
	var keep []string
	for _, u := range user {
		if u != token {
			keep = append(keep, u)
		}
	}
	base := c.PortsFor(proto).Base
	parts := append(keep, nonEmpty(base)...)
	if strings.Join(parts, ",") == cur {
		return true, nil
	}
	return true, c.Set(name, strings.Join(parts, ","))
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// SetFallback включает/выключает безразборный режим (блоки FALLBACK и
// FALLBACK_HTTP): убирает или добавляет `--skip` перед `--filter-tcp=`.
func (c *Config) SetFallback(on bool) error {
	found := 0
	for _, name := range []string{BlockFallbackTLS, BlockFallbackHTTP} {
		if _, ok := c.FindBlock(name); !ok {
			continue
		}
		n, err := c.toggleSkipBlock(name, filterTCPAnyRe, on)
		if err != nil {
			continue
		}
		found += n
	}
	if found == 0 {
		return fmt.Errorf("%w: fallback blocks", ErrNotFound)
	}
	return nil
}

// SetWireGuard включает/выключает стратегию WireGuard (блок WG).
func (c *Config) SetWireGuard(on bool) error {
	_, err := c.toggleSkipBlock(BlockWireGuard, filterWGRe, on)
	return err
}

// SetQUIC443 включает/выключает фейки QUIC на UDP 443 (блок QUIC443).
func (c *Config) SetQUIC443(on bool) error {
	_, err := c.toggleSkipBlock(BlockQUIC443, filterQUICRe, on)
	return err
}

// SetDNSDesync включает/выключает антиспуф DNS (блок DNS + порт 53).
func (c *Config) SetDNSDesync(on bool) error {
	if _, err := c.toggleSkipBlock(BlockDNS, filterDNSRe, on); err != nil {
		return err
	}
	return c.SetPortToken("udp", DNSPorts, on)
}

// SetUDPGames включает/выключает игровой UDP (профиль 7: блок под заголовком
// «Стратегии для игрового UDP» + порты 1026-65531).
func (c *Config) SetUDPGames(on bool) error {
	start := -1
	for i, ln := range c.lines {
		if strings.Contains(ln, udpGamesHeader) {
			start = i
			break
		}
	}
	if start < 0 {
		return fmt.Errorf("%w: UDP games block", ErrNotFound)
	}
	end := len(c.lines)
	for i := start + 1; i < len(c.lines); i++ {
		if newLineRe.MatchString(c.lines[i]) {
			end = i
			break
		}
	}
	if c.toggleSkipRange(start+1, end, filterGamesRe, on) == 0 {
		return fmt.Errorf("%w: no filter line in UDP games block", ErrNotFound)
	}
	return c.SetPortToken("udp", UDPGamesPorts, on)
}

// SetHostlistAuto переключает MODE_FILTER между autohostlist и hostlist.
func (c *Config) SetHostlistAuto(auto bool) error {
	if auto {
		return c.Set("MODE_FILTER", "autohostlist")
	}
	return c.Set("MODE_FILTER", "hostlist")
}

// SetAutoMode включает/выключает авторотацию: меняет маркер #Z2R_AUTO_MODE и
// переключает `--skip` у TCP-профилей «стандартных» блоков и блоков авторотации
// (fallback-блоки остаются как есть: при включённой авторотации они выключены).
func (c *Config) SetAutoMode(on bool) error {
	if err := c.SetAutoModeFlag(on); err != nil {
		return err
	}
	std, okStd := c.FindBlock(BlockAutoStandard)
	auto, okAuto := c.FindBlock(BlockAuto)
	if !okStd && !okAuto {
		return nil
	}
	var skipRanges [][2]int
	for _, name := range []string{BlockFallbackTLS, BlockFallbackHTTP} {
		if b, ok := c.FindBlock(name); ok {
			skipRanges = append(skipRanges, [2]int{b.Begin, b.End})
		}
	}
	inSkip := func(i int) bool {
		for _, r := range skipRanges {
			if i >= r[0] && i <= r[1] {
				return true
			}
		}
		return false
	}
	apply := func(b Block, enable bool, avoidFallback bool) {
		for i := b.Begin + 1; i < b.End; i++ {
			if avoidFallback && inSkip(i) {
				continue
			}
			ln := c.lines[i]
			if strings.HasPrefix(strings.TrimSpace(ln), "#") || !filterTCPAnyRe.MatchString(ln) {
				continue
			}
			c.lines[i] = setSkipPrefix(ln, !enable)
		}
	}
	if okStd {
		apply(std, !on, true)
	}
	if okAuto {
		apply(auto, on, false)
	}
	c.reindex()
	return nil
}

// SetRSTGuard включает/выключает RST-guard: строки с
// `--lua-desync=rst_guard_locked:key=` комментируются (`#`) либо возвращаются.
func (c *Config) SetRSTGuard(on bool) error {
	changed := 0
	for i, ln := range c.lines {
		if !strings.Contains(ln, rstGuardMarkerText) {
			continue
		}
		t := strings.TrimLeft(ln, " \t")
		commented := strings.HasPrefix(t, "#")
		switch {
		case on && commented:
			c.lines[i] = strings.TrimLeft(strings.TrimPrefix(t, "#"), " \t")
			changed++
		case !on && !commented:
			c.lines[i] = "#" + t
			changed++
		}
	}
	if changed > 0 {
		c.reindex()
		return nil
	}
	if enabled := c.RSTGuardText() == "включен"; enabled == on {
		return nil
	}
	return fmt.Errorf("%w: rst_guard lines", ErrNotFound)
}

// EnsureLuaInit оставляет ровно один lua-init с таким именем файла и
// приводит его путь к absPath. Возвращает true, если конфиг изменился.
func (c *Config) EnsureLuaInit(absPath string) (bool, error) {
	absPath = strings.TrimSpace(absPath)
	if absPath == "" {
		return false, fmt.Errorf("%w: empty lua-init path", ErrInvalid)
	}
	needle := "--lua-init=@" + absPath
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrNotFound, OptVar)
	}
	lines := strings.Split(opt, "\n")
	out := make([]string, 0, len(lines)+1)
	base := path.Base(absPath)
	found, changed := false, false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "--lua-init=@") && path.Base(strings.TrimPrefix(t, "--lua-init=@")) == base {
			if found {
				changed = true
				continue
			}
			found = true
			if t != needle {
				changed = true
				out = append(out, needle)
			} else {
				out = append(out, ln)
			}
			continue
		}
		out = append(out, ln)
	}
	if !found {
		at := 0
		for i, ln := range out {
			t := strings.TrimSpace(ln)
			if strings.HasPrefix(t, "--lua-init=") {
				at = i + 1
			}
		}
		out = append(out[:at], append([]string{needle}, out[at:]...)...)
		changed = true
	}
	if !changed {
		return false, nil
	}
	if err := c.SetNFQWS2Opt(strings.Join(out, "\n")); err != nil {
		return false, err
	}
	return true, nil
}

// SetReasm включает/выключает `--reasm-disable` в NFQWS2_OPT.
func (c *Config) SetReasm(on bool) error {
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, OptVar)
	}
	lines := strings.Split(opt, "\n")
	out := make([]string, 0, len(lines)+1)
	for _, ln := range lines {
		if reasmDisableRe.MatchString(ln) {
			continue
		}
		out = append(out, ln)
	}
	if on {
		at := 0
		for i, ln := range out {
			t := strings.TrimSpace(ln)
			if strings.HasPrefix(t, "--lua-init=") || strings.HasPrefix(t, "--blob=") {
				at = i + 1
				continue
			}
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			break
		}
		out = append(out[:at], append([]string{"--reasm-disable"}, out[at:]...)...)
	}
	return c.SetNFQWS2Opt(strings.Join(out, "\n"))
}

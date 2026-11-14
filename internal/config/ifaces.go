package config

import (
	"fmt"
	"regexp"
	"strings"
)

var ifaceAssignRe = regexp.MustCompile(`^[ \t]*#?[ \t]*(IFACE_(?:LAN|WAN|WAN6))=(.*)$`)

// IfaceLAN / IfaceWAN — имена переменных конфига.
const (
	IfaceLAN  = "IFACE_LAN"
	IfaceWAN  = "IFACE_WAN"
	IfaceWAN6 = "IFACE_WAN6"
)

// GetIface возвращает значение IFACE_* без кавычек; ok=false если переменная
// не задана (закомментированные шаблоны eth0/eth1 не считаются).
func (c *Config) GetIface(name string) (string, bool) {
	return c.Get(name)
}

// SetIface записывает IFACE_LAN/WAN/WAN6. Если в файле есть закомментированная
// строка `#IFACE_WAN=…`, она раскомментируется и заменяется; иначе — Set.
func (c *Config) SetIface(name, value string) error {
	switch name {
	case IfaceLAN, IfaceWAN, IfaceWAN6:
	default:
		return fmt.Errorf("%w: %s", ErrInvalid, name)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%w: empty %s", ErrInvalid, name)
	}
	if strings.ContainsAny(value, "\r\n\"\\`$;&|<>()*'?") {
		return fmt.Errorf("%w: value of %s", ErrInvalid, name)
	}
	line := name + "=" + quoteIfNeeded(value)
	// 1) активное присваивание
	if e, ok := c.find(name); ok {
		c.spliceLines(e.start, e.end+1, []string{line})
		return nil
	}
	// 2) закомментированный шаблон той же переменной
	for i, ln := range c.lines {
		m := ifaceAssignRe.FindStringSubmatch(ln)
		if m == nil || m[1] != name {
			continue
		}
		c.spliceLines(i, i+1, []string{line})
		return nil
	}
	// 3) вставка рядом с блоком IFACE_* комментариев
	if idx := c.findIfaceAnchor(); idx >= 0 {
		c.spliceLines(idx, idx, []string{line})
		return nil
	}
	return c.Set(name, quoteIfNeeded(value))
}

// SetIfaces задаёт LAN и WAN (WAN6 копирует WAN, если пуст).
func (c *Config) SetIfaces(lan, wan string) error {
	if lan != "" {
		if err := c.SetIface(IfaceLAN, lan); err != nil {
			return err
		}
	}
	if wan != "" {
		if err := c.SetIface(IfaceWAN, wan); err != nil {
			return err
		}
		if _, ok := c.Get(IfaceWAN6); !ok {
			_ = c.SetIface(IfaceWAN6, wan)
		}
	}
	return nil
}

// NeedsIfaceAutodetect — true, если IFACE не заданы или остались шаблонные eth0/eth1.
func (c *Config) NeedsIfaceAutodetect() bool {
	lan, lanOK := c.Get(IfaceLAN)
	wan, wanOK := c.Get(IfaceWAN)
	if !lanOK || !wanOK {
		return true
	}
	lan, wan = strings.TrimSpace(lan), strings.TrimSpace(wan)
	if lan == "" || wan == "" {
		return true
	}
	// Типичный шаблон из config.default — почти наверняка неверный на Keenetic.
	if (lan == "eth0" && wan == "eth1") || (lan == "eth1" && wan == "eth0") {
		return true
	}
	return false
}

func (c *Config) findIfaceAnchor() int {
	for i, ln := range c.lines {
		if strings.Contains(ln, "IFACE_LAN") || strings.Contains(ln, "IFACE_WAN") {
			return i
		}
	}
	return -1
}

func quoteIfNeeded(v string) string {
	if strings.ContainsAny(v, " \t") {
		return `"` + v + `"`
	}
	return v
}

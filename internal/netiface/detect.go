// Package netiface определяет WAN/LAN интерфейсы для IFACE_WAN / IFACE_LAN
// в конфиге zapret2 (classic linux / Keenetic / Entware).
package netiface

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Info — текущие и рекомендуемые интерфейсы.
type Info struct {
	WAN       string `json:"wan"`
	LAN       string `json:"lan"`
	Suggested struct {
		WAN string `json:"wan"`
		LAN string `json:"lan"`
	} `json:"suggested"`
	Available []Iface `json:"available"`
	Source    string  `json:"source"` // route|bridge|manual|empty
}

// Iface — сетевой интерфейс, пригодный для выбора в UI.
type Iface struct {
	Name  string   `json:"name"`
	Up    bool     `json:"up"`
	Addrs []string `json:"addrs,omitempty"`
	Kind  string   `json:"kind"` // wan_candidate|lan_candidate|other
}

// Detector читает sysfs/proc относительно Root (по умолчанию "/").
type Detector struct {
	Root string
}

func (d Detector) root() string {
	if d.Root == "" {
		return "/"
	}
	return d.Root
}

// List возвращает интерфейсы из /sys/class/net (без lo).
func (d Detector) List() ([]Iface, error) {
	dir := filepath.Join(d.root(), "sys", "class", "net")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	wanHint, _ := d.DefaultRouteDev()
	out := make([]Iface, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if name == "" || name == "lo" {
			continue
		}
		if isNoiseIface(name) {
			continue
		}
		inf := Iface{Name: name, Up: linkIsUp(filepath.Join(dir, name)), Addrs: ipv4Addrs(name)}
		switch {
		case name == wanHint:
			inf.Kind = "wan_candidate"
		case isLANName(name) || hasPrivateIPv4(inf.Addrs):
			inf.Kind = "lan_candidate"
		default:
			inf.Kind = "other"
		}
		out = append(out, inf)
	}
	return out, nil
}

// DefaultRouteDev — устройство default-маршрута из /proc/net/route.
func (d Detector) DefaultRouteDev() (string, error) {
	f, err := os.Open(filepath.Join(d.root(), "proc", "net", "route"))
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return "", fmt.Errorf("netiface: empty route table")
	}
	bestMetric := int(^uint(0) >> 1)
	best := ""
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 8 {
			continue
		}
		// Iface Destination Gateway Flags RefCnt Use Metric Mask
		if fields[1] != "00000000" {
			continue
		}
		metric := 0
		fmt.Sscanf(fields[6], "%d", &metric)
		iface := fields[0]
		if iface == "" || iface == "*" {
			continue
		}
		if best == "" || metric < bestMetric {
			best = iface
			bestMetric = metric
		}
	}
	if best == "" {
		return "", fmt.Errorf("netiface: no default route")
	}
	return best, nil
}

// DetectWANLAN подбирает WAN (default route) и LAN (bridge / private IPv4).
func (d Detector) DetectWANLAN() (wan, lan, source string, err error) {
	wan, err = d.DefaultRouteDev()
	if err != nil {
		return "", "", "", err
	}
	list, listErr := d.List()
	if listErr != nil {
		return wan, "", "route", nil
	}
	// 1) явные bridge-имена
	for _, pref := range []string{"br0", "br-lan", "br1", "bridge0"} {
		for _, inf := range list {
			if inf.Name == pref && inf.Name != wan {
				return wan, pref, "bridge", nil
			}
		}
	}
	// 2) любой lan_candidate с private IP
	for _, inf := range list {
		if inf.Name == wan {
			continue
		}
		if inf.Kind == "lan_candidate" && hasPrivateIPv4(inf.Addrs) {
			return wan, inf.Name, "bridge", nil
		}
	}
	// 3) любой up с private IP
	for _, inf := range list {
		if inf.Name == wan || !inf.Up {
			continue
		}
		if hasPrivateIPv4(inf.Addrs) {
			return wan, inf.Name, "route", nil
		}
	}
	return wan, "", "route", nil
}

func linkIsUp(sysPath string) bool {
	b, err := os.ReadFile(filepath.Join(sysPath, "operstate"))
	if err != nil {
		return false
	}
	s := strings.TrimSpace(string(b))
	return s == "up" || s == "unknown"
}

func ipv4Addrs(iface string) []string {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP == nil {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil {
			continue
		}
		out = append(out, ipNet.String())
	}
	return out
}

func hasPrivateIPv4(addrs []string) bool {
	for _, a := range addrs {
		ip, _, err := net.ParseCIDR(a)
		if err != nil {
			ip = net.ParseIP(a)
		}
		if ip == nil {
			continue
		}
		if ip.IsPrivate() {
			return true
		}
	}
	return false
}

// DetectLANIPv4 mirrors the WebUI launcher: prefer the router bridge address,
// then a private IPv4 on another interface, and finally any non-loopback IPv4.
func DetectLANIPv4() string {
	for _, name := range []string{"br-lan", "br0"} {
		if ip := interfaceIPv4(name); ip != "" {
			return ip
		}
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var fallback string
	for _, iface := range interfaces {
		if iface.Name == "lo" || iface.Flags&net.FlagUp == 0 {
			continue
		}
		for _, ip := range interfaceIPv4s(iface) {
			if ip.IsPrivate() {
				return ip.String()
			}
			if fallback == "" && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				fallback = ip.String()
			}
		}
	}
	return fallback
}

func interfaceIPv4(name string) string {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	ips := interfaceIPv4s(*iface)
	if len(ips) == 0 {
		return ""
	}
	return ips[0].String()
}

func interfaceIPv4s(iface net.Interface) []net.IP {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	var ips []net.IP
	for _, addr := range addrs {
		var ip net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		}
		if ip4 := ip.To4(); ip4 != nil {
			ips = append(ips, ip4)
		}
	}
	return ips
}

func isLANName(name string) bool {
	n := strings.ToLower(name)
	return strings.HasPrefix(n, "br") ||
		strings.HasPrefix(n, "lan") ||
		n == "bridge0"
}

// isNoiseIface отсекает виртуальные/служебные ifaces Keenetic и Linux,
// которые не должны попадать в выпадающий список WAN/LAN.
func isNoiseIface(name string) bool {
	n := strings.ToLower(name)
	prefixes := []string{
		"ra", "apcli", "sit", "gre", "gretap", "ip6tnl", "tun", "tap",
		"dummy", "xfrm", "nwg", "ezcfg", "ifb", "teql", "veth",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	switch n {
	case "sstp-br-link", "sstp-bridge", "sstp-peer-link":
		return true
	}
	if strings.HasPrefix(n, "sstp-") {
		return true
	}
	return false
}

// ValidName — безопасное имя интерфейса для записи в config.
func ValidName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// ValidList — один или несколько имён через пробел.
func ValidList(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	for _, p := range strings.Fields(v) {
		if !ValidName(p) {
			return false
		}
	}
	return true
}

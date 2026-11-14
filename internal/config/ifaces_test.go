package config

import "testing"

func TestSetIfaceUncommentsTemplate(t *testing.T) {
	c := Parse(`#IFACE_LAN=eth0
#IFACE_WAN=eth1
INIT_APPLY_FW=1
`)
	if err := c.SetIfaces("br0", "eth3"); err != nil {
		t.Fatal(err)
	}
	lan, ok := c.Get(IfaceLAN)
	if !ok || lan != "br0" {
		t.Fatalf("LAN=%q ok=%v", lan, ok)
	}
	wan, ok := c.Get(IfaceWAN)
	if !ok || wan != "eth3" {
		t.Fatalf("WAN=%q ok=%v", wan, ok)
	}
	text := c.String()
	if containsLine(text, "#IFACE_LAN=eth0") || containsLine(text, "#IFACE_WAN=eth1") {
		t.Fatalf("template comments remain:\n%s", text)
	}
	wan6, ok := c.Get(IfaceWAN6)
	if !ok || wan6 != "eth3" {
		t.Fatalf("WAN6=%q ok=%v", wan6, ok)
	}
}

func TestNeedsIfaceAutodetect(t *testing.T) {
	c := Parse(`#IFACE_LAN=eth0
#IFACE_WAN=eth1
`)
	if !c.NeedsIfaceAutodetect() {
		t.Fatal("commented should need detect")
	}
	_ = c.SetIfaces("br0", "eth3")
	if c.NeedsIfaceAutodetect() {
		t.Fatal("set ifaces should not need detect")
	}
	c2 := Parse("IFACE_LAN=eth0\nIFACE_WAN=eth1\n")
	if !c2.NeedsIfaceAutodetect() {
		t.Fatal("eth0/eth1 template should need detect")
	}
}

func containsLine(text, want string) bool {
	for _, ln := range splitLines(text) {
		if ln == want {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

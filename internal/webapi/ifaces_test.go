package webapi

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestIfacesGetAndSet(t *testing.T) {
	e := newEnv(t, envOpts{})
	cfgPath := e.app.Paths.ZapretConfig
	if err := os.WriteFile(cfgPath, []byte(`#IFACE_LAN=eth0
#IFACE_WAN=eth1
FWTYPE=iptables
FLOWOFFLOAD=donttouch
`), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, out := e.get("/cgi-bin/settings.cgi?setting=ifaces")
	wantStatus(t, resp, http.StatusOK)
	if out["needs_autodetect"] != true {
		t.Fatalf("needs_autodetect=%v out=%v", out["needs_autodetect"], out)
	}

	resp, out = e.post("/cgi-bin/settings.cgi", "setting", "ifaces", "action", "set", "lan", "br0", "wan", "eth3", "restart", "0")
	wantOK(t, resp, out)
	if out["lan"] != "br0" || out["wan"] != "eth3" {
		t.Fatalf("after set: %v", out)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "IFACE_LAN=br0") || !strings.Contains(text, "IFACE_WAN=eth3") {
		t.Fatalf("config not updated:\n%s", text)
	}
	if strings.Contains(text, "#IFACE_LAN=eth0") {
		t.Fatal("commented LAN template remains")
	}
}

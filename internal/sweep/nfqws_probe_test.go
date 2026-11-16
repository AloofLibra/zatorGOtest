package sweep

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/firewall"
	"github.com/AloofLibra/zator/internal/paths"
)

func TestNfqwsProbeBuildArgs(t *testing.T) {
	p := paths.New(t.TempDir(), t.TempDir())
	cfg := config.Parse(`FWTYPE=iptables
QNUM=200
NFQWS2_OPT="
--qnum=200
--lua-init=@/opt/zator/lua/client-scope-config.lua
--lua-init=@/opt/zator/lua/locked.lua
--lua-init=@/opt/zator/lua/strategy-lock-manager.lua
--filter-tcp=443
--filter-mark=0x04000000/0x04000000
--lua-desync=wssize_ramp:mode=full:wsize=1:scale=2:open=1
"
`)
	probe, err := NewNfqwsProbe(NfqwsProbeConfig{
		Paths: p, Config: cfg, Runner: firewall.NewRecordingRunner(),
		User: "nobody", UID: 65534, GID: 65534,
	})
	if err != nil {
		t.Fatal(err)
	}
	luaDir := filepath.Join(probe.WorkDir(), "lua")
	args, err := probe.buildArgs(luaDir)
	if err != nil {
		t.Fatal(err)
	}
	if !containsArg(args, "--qnum=65535") {
		t.Fatalf("qnum: %v", args)
	}
	if !containsArg(args, "--user=nobody") {
		t.Fatalf("user: %v", args)
	}
	if !containsArg(args, "--fwmark=0x40000000") {
		t.Fatalf("fwmark: %v", args)
	}
	if !containsArg(args, "--filter-mark=0x04000000/0x04000000") || !containsArg(args, "--lua-desync=wssize_ramp:mode=full:wsize=1:scale=2:open=1") {
		t.Fatalf("WSSIZE probe options must match the primary queue: %v", args)
	}
	lib := "--lua-init=@" + filepath.ToSlash(filepath.Join(p.ZapretRoot, "lua", "zapret-lib.lua"))
	if !containsArg(args, lib) {
		t.Fatalf("zapret-lib: %v", args)
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--qnum=") && a != "--qnum=65535" {
			t.Fatalf("лишний qnum: %s", a)
		}
		if strings.HasPrefix(a, "--user=") && a != "--user=nobody" {
			t.Fatalf("лишний user: %s", a)
		}
	}
	locked := "--lua-init=@" + filepath.ToSlash(filepath.Join(luaDir, "locked.lua"))
	slm := "--lua-init=@" + filepath.ToSlash(filepath.Join(luaDir, "strategy-lock-manager.lua"))
	if !containsArg(args, locked) || !containsArg(args, slm) {
		t.Fatalf("lua paths: %v", args)
	}
	if !containsArg(args, "--lua-init=@/opt/zator/lua/client-scope-config.lua") {
		t.Fatalf("прочие lua-init должны сохраняться: %v", args)
	}
}

func TestNfqwsProbeWriteProbeLua(t *testing.T) {
	p := paths.New(t.TempDir(), t.TempDir())
	if err := os.MkdirAll(p.LuaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `local LOCKED_PATH = "/opt/zator/state/orchestra/locked.tsv"
local X = "/opt/zator/state/orchestra/foo"
`
	if err := os.WriteFile(filepath.Join(p.LuaDir, "locked.lua"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Parse(`FWTYPE=iptables
NFQWS2_OPT="
--new
"
`)
	probe, err := NewNfqwsProbe(NfqwsProbeConfig{
		Paths: p, Config: cfg, Runner: firewall.NewRecordingRunner(),
		User: "nobody", UID: 65534, GID: 65534,
	})
	if err != nil {
		t.Fatal(err)
	}
	luaDir := filepath.Join(probe.WorkDir(), "lua")
	if err := os.MkdirAll(luaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := probe.writeProbeLua(luaDir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(luaDir, "locked.lua"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.ToSlash(probe.OrchestraDir())
	if !strings.Contains(string(got), want+"/locked.tsv") {
		t.Fatalf("path not rewritten: %s", got)
	}
	if strings.Contains(string(got), productionOrchestraLuaPath) {
		t.Fatal("production path still present")
	}
}

func TestNewNfqwsProbeDefaults(t *testing.T) {
	p := paths.New(t.TempDir(), t.TempDir())
	_, err := NewNfqwsProbe(NfqwsProbeConfig{Paths: p})
	if err == nil {
		t.Fatal("ожидалась ошибка без Config")
	}
	cfg := config.Parse(`FWTYPE=nftables
NFQWS2_OPT="
--new
"
`)
	probe, err := NewNfqwsProbe(NfqwsProbeConfig{
		Paths: p, Config: cfg,
		User: "nobody", UID: 65534, GID: 65534,
		resolveUser: func() (string, uint32, uint32) { return "nobody", 65534, 65534 },
	})
	if err != nil {
		t.Fatal(err)
	}
	if probe.WorkDir() != filepath.Join(p.StateDir, "supersweep-probe") {
		t.Errorf("WorkDir = %s", probe.WorkDir())
	}
	if probe.OrchestraDir() != filepath.Join(probe.WorkDir(), "orchestra") {
		t.Errorf("OrchestraDir = %s", probe.OrchestraDir())
	}
	if probe.cfg.UID == 0 || probe.cfg.User == "" {
		t.Errorf("UID/User empty: uid=%d user=%q", probe.cfg.UID, probe.cfg.User)
	}
}

func TestNewNfqwsProbeRequiresPasswdUser(t *testing.T) {
	p := paths.New(t.TempDir(), t.TempDir())
	cfg := config.Parse(`FWTYPE=iptables
NFQWS2_OPT="
--new
"
`)
	_, err := NewNfqwsProbe(NfqwsProbeConfig{
		Paths: p, Config: cfg,
		resolveUser: func() (string, uint32, uint32) { return "", 0, 0 },
	})
	if err == nil {
		t.Fatal("ожидалась ошибка без passwd-user")
	}
}

func TestResolveProbeUserFallback(t *testing.T) {
	name, uid, _ := firewall.ResolveProbeUser()
	// На Windows passwd-user часто нет — пустой результат ожидаем.
	if name != "" && uid == 0 {
		t.Fatalf("ResolveProbeUser inconsistent: %q %d", name, uid)
	}
	if name == "" && uid != 0 {
		t.Fatalf("ResolveProbeUser inconsistent: %q %d", name, uid)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

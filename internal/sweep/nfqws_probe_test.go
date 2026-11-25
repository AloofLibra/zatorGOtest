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
NFQWS2_ENABLE=1
Z2R_WSSIZE_RAMP_ENABLE=1
Z2R_WSSIZE_RAMP_MODE=full
Z2R_WSSIZE_RAMP_WSIZE=1
Z2R_WSSIZE_RAMP_SCALE=2
Z2R_WSSIZE_RAMP_OPEN=1
NFQWS2_OPT="
--blob=fake_default_tls:@/opt/zator/files/fake/tls.bin
--template=z2r_tcp_tls_common
--out-range=-s34228
--payload=tls_client_hello
--lua-desync=fake:blob=fake_default_tls:strategy=1
--new
#Z2R_AUTO_STANDARD_BEGIN
--filter-mark=0x04000000/0x04000000
--lua-desync=wssize_ramp${Z2R_WSSIZE_RAMP_ARGS}
--new
#Z2R_AUTO_STANDARD_1_BEGIN
--filter-tcp=443 --hostlist=/profile1.txt
--lua-desync=circular_locked:key=1
--import=z2r_tcp_tls_common
#Z2R_AUTO_STANDARD_1_END
--new
#Z2R_AUTO_STANDARD_2_BEGIN
--filter-tcp=443 --hostlist-domains=googlevideo.com
--lua-desync=circular_quality:key=2
--import=z2r_tcp_tls_common
#Z2R_AUTO_STANDARD_2_END
--new
#Z2R_AUTO_STANDARD_3_BEGIN
--filter-tcp=443 --hostlist=/profile3.txt
--lua-desync=circular_locked:key=3
--import=z2r_tcp_tls_common
#Z2R_AUTO_STANDARD_3_END
"
`)
	probe, err := NewNfqwsProbe(NfqwsProbeConfig{
		Paths: p, Config: cfg, Runner: firewall.NewRecordingRunner(),
		User: "nobody", UID: 65534, GID: 65534,
	})
	if err != nil {
		t.Fatal(err)
	}
	probe.profile = 2
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
	if !containsArg(args, "--lua-desync=circular_locked:key=2:allow_nohost=1") || !containsArg(args, "--ipset="+filepath.ToSlash(firewall.DefaultWssizeIPFile(p.ZatorRoot))) {
		t.Fatalf("profile 2 strategy and Googlevideo set missing: %v", args)
	}
	for _, forbidden := range []string{"--hostlist=/profile1.txt", "--hostlist=/profile3.txt", "--lua-desync=circular_quality:key=2", "--lua-init=@/opt/zator/lua/client-scope-config.lua"} {
		if containsArg(args, forbidden) {
			t.Fatalf("unrelated runtime option leaked into isolated probe: %s", forbidden)
		}
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
	if !containsArg(args, locked) || !containsArg(args, "--lua-init=@"+filepath.ToSlash(filepath.Join(p.LuaDir, "fake-adapt.lua"))) || !containsArg(args, "--lua-init=@"+filepath.ToSlash(filepath.Join(p.LuaDir, "wssize-ramp.lua"))) {
		t.Fatalf("lua paths: %v", args)
	}
}

func TestNfqwsProbeWriteProbeLua(t *testing.T) {
	p := paths.New(t.TempDir(), t.TempDir())
	if err := os.MkdirAll(p.LuaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.StateOrchestraDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.StateDir, "supersweep-probe", "orchestra"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := `local LOCKED_PATH = "/opt/zator/state/orchestra/locked.tsv"
local X = "/opt/zator/state/orchestra/foo"
`
	if err := os.WriteFile(filepath.Join(p.LuaDir, "locked.lua"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	managerSrc := `local SLM_AUTO_LOCKED_PATH = "/tmp/auto_locked.tsv"
local SLM_AUTO_LOCKED_RELOAD_STAMP = "/tmp/z2r-auto-locked.reload"
`
	if err := os.WriteFile(filepath.Join(p.LuaDir, "strategy-lock-manager.lua"), []byte(managerSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"blob_override.tsv": "1\tcustom1\n2\tcustom2\n3\tcustom3\n4\tcustom4\n8\tcustom8\n",
		"mode_override.tsv": "1\tclone\n2\tclone\n3\tclone\n4\tclone\n8\tclone\n",
		"sni_override.tsv":  "1\twww.google.com\n2\twww.google.com\n3\twww.google.com\n4\twww.google.com\n8\twww.google.com\n",
		"clonesize.tsv":     "1\t964\n2\t964\n3\t964\n4\t964\n8\t964\n",
	} {
		if err := os.WriteFile(filepath.Join(p.StateOrchestraDir(), name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
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
	manager, err := os.ReadFile(filepath.Join(luaDir, "strategy-lock-manager.lua"))
	if err != nil {
		t.Fatal(err)
	}
	privateAutoPath := filepath.ToSlash(filepath.Join(probe.OrchestraDir(), "auto_locked.tsv"))
	if !strings.Contains(string(manager), `SLM_AUTO_LOCKED_PATH = "`+privateAutoPath+`"`) || strings.Contains(string(manager), `SLM_AUTO_LOCKED_PATH = "/tmp/auto_locked.tsv"`) {
		t.Fatalf("isolated probe auto-lock path was not redirected: %s", manager)
	}
	for name, want := range map[string]string{
		"blob_override.tsv": "1\tcustom1\n2\tcustom2\n3\tcustom3\n4\tcustom4\n8\tcustom8\n",
		"mode_override.tsv": "1\tclone\n2\tclone\n3\tclone\n4\tclone\n8\tclone\n",
		"sni_override.tsv":  "1\twww.google.com\n2\twww.google.com\n3\twww.google.com\n4\twww.google.com\n8\twww.google.com\n",
		"clonesize.tsv":     "1\t964\n2\t964\n3\t964\n4\t964\n8\t964\n",
	} {
		got, err := os.ReadFile(filepath.Join(probe.OrchestraDir(), name))
		if err != nil || string(got) != want {
			t.Fatalf("override snapshot %s = %q, %v; want %q", name, got, err, want)
		}
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
	if probe.WorkDir() != filepath.Join(paths.RuntimeTempDir(p.ZatorRoot), "supersweep-probe") {
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

func TestWriteProbeLuaIsolatesSNIPoolAndPin(t *testing.T) {
	root := t.TempDir()
	p := paths.New(root, filepath.Join(root, "zapret2"))
	for _, dir := range []string{p.LuaDir, p.StateOrchestraDir(), p.ExtraStratsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(p.LuaDir, "locked.lua"), []byte(`local L="/opt/zator/state/orchestra/locked.tsv"`), 0o644)
	_ = os.WriteFile(filepath.Join(p.LuaDir, "sni-pool.lua"), []byte(`local SP="/opt/zator/state/orchestra/sni_pool.tsv"
local PIN="/opt/zator/state/orchestra/sni_pool.pin"
local SZ="/opt/zator/extra_strats/sni_zator.txt"`), 0o644)
	_ = os.WriteFile(p.SNIPoolTSV(), []byte("1\t@google\n"), 0o644)
	_ = os.WriteFile(p.SNIPoolPolicyTSV(), []byte("1\trr\n"), 0o644)

	cfg := config.Parse(`FWTYPE=iptables
NFQWS2_OPT="
--template=z2r_tcp_tls_common
--new
#Z2R_AUTO_STANDARD_1_BEGIN
--filter-tcp=443 --hostlist=/profile1.txt
--lua-desync=circular_locked:key=1
--import=z2r_tcp_tls_common
#Z2R_AUTO_STANDARD_1_END
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
	_ = os.MkdirAll(luaDir, 0o755)
	if err := probe.writeProbeLua(luaDir); err != nil {
		t.Fatal(err)
	}
	probeLua, err := os.ReadFile(filepath.Join(luaDir, "sni-pool.lua"))
	if err != nil {
		t.Fatal(err)
	}
	wantOrch := filepath.ToSlash(probe.OrchestraDir())
	if !strings.Contains(string(probeLua), wantOrch+"/sni_pool.pin") {
		t.Fatalf("sni_pool.pin path not rewritten to probe dir:\n%s", probeLua)
	}
	if _, err := os.Stat(filepath.Join(probe.OrchestraDir(), "sni_pool.pin")); err != nil {
		t.Fatalf("expected private probe sni_pool.pin: %v", err)
	}
	if _, err := os.Stat(p.SNIPoolPin()); !os.IsNotExist(err) {
		t.Fatalf("global sni_pool.pin must not be touched by probe, err=%v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(probe.OrchestraDir(), "sni_pool.tsv")); string(got) != "1\t@google\n" {
		t.Fatalf("sni_pool.tsv snapshot mismatch: %q", got)
	}
	probe.profile = 1
	args, err := probe.buildArgs(luaDir)
	if err != nil {
		t.Fatal(err)
	}
	wantInit := "--lua-init=@" + filepath.ToSlash(filepath.Join(luaDir, "sni-pool.lua"))
	if !containsArg(args, wantInit) {
		t.Fatalf("missing sni-pool --lua-init in args: %v", args)
	}
	if err := probe.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(probe.OrchestraDir(), "sni_pool.pin")); !os.IsNotExist(err) {
		t.Fatalf("probe sni_pool.pin should be cleaned up on Stop, err=%v", err)
	}
}

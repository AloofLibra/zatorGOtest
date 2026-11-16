package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/platform"
)

func TestMigratePackageA(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{
		ZatorRoot:  root,
		StateDir:   filepath.Join(root, "state"),
		ConfigDir:  filepath.Join(root, "config"),
		ZapretRoot: filepath.Join(root, "zapret2"),
		LuaDir:     filepath.Join(root, "lua"),
	}
	p.ZapretConfig = filepath.Join(p.ZapretRoot, "config")
	a := NewWith(p, platform.Platform{})

	raw := `Z2R_WSSIZE_RAMP_ENABLE=0
Z2R_WSSIZE_RAMP_MODE=full
Z2R_WSSIZE_RAMP_WSIZE=1
Z2R_WSSIZE_RAMP_SCALE=1
Z2R_WSSIZE_RAMP_OPEN=1
Z2R_WSSIZE_RAMP_ARGS=
Z2R_WSSIZE_RAMP_SKIP=--skip
if [ "$Z2R_WSSIZE_RAMP_ENABLE" = 1 ]; then
  Z2R_WSSIZE_RAMP_ARGS=":mode=$Z2R_WSSIZE_RAMP_MODE:wsize=1:scale=1:open=1"
  Z2R_WSSIZE_RAMP_SKIP=
fi
NFQWS2_OPT="
--lua-desync=circular_quality:key=2:fails=2:default_strategy=41:exclude_substrings=x:payload=tls_client_hello
"
`
	cfg := config.Parse(raw)
	if !a.migratePackageA(cfg) {
		t.Fatal("expected migration changes")
	}
	if v, _ := cfg.Get("Z2R_WSSIZE_RAMP_ENABLE"); v != "0" {
		t.Fatalf("ENABLE=%q", v)
	}
	if v, _ := cfg.Get("Z2R_WSSIZE_RAMP_MODE"); v != "full" {
		t.Fatalf("MODE=%q", v)
	}
	if v, _ := cfg.Get("Z2R_WSSIZE_RAMP_SKIP"); v != "--skip" {
		t.Fatalf("SKIP=%q", v)
	}
	if strings.Contains(cfg.String(), "if [") {
		t.Fatal("shell if not stripped")
	}
	opt, _ := cfg.NFQWS2Opt()
	if !strings.Contains(opt, "soft_success_in=0") {
		t.Fatalf("soft_success missing: %s", opt)
	}
	// Second pass: user disabled WSSIZE — do not force back on.
	_ = cfg.Set("Z2R_WSSIZE_RAMP_ENABLE", "0")
	_, _ = cfg.SyncWssizeRampVars()
	if a.migratePackageA(cfg) {
		// Sync may no-op; forcing ENABLE back would be a bug.
	}
	if v, _ := cfg.Get("Z2R_WSSIZE_RAMP_ENABLE"); v != "0" {
		t.Fatalf("user disable overwritten: %q", v)
	}
}

func TestMigratePackageAEnablesUnconfiguredWssize(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{ZatorRoot: root, StateDir: filepath.Join(root, "state")}
	a := NewWith(p, platform.Platform{})
	cfg := config.Parse(`NFQWS2_OPT="
--new
--filter-tcp=443
"
`)
	if !a.migratePackageA(cfg) {
		t.Fatal("expected WSSIZE defaults to be added")
	}
	if v, _ := cfg.Get("Z2R_WSSIZE_RAMP_ENABLE"); v != "1" {
		t.Fatalf("ENABLE=%q", v)
	}
	if v, _ := cfg.Get("Z2R_WSSIZE_RAMP_MODE"); v != "lite" {
		t.Fatalf("MODE=%q", v)
	}
	if v, _ := cfg.Get("Z2R_WSSIZE_RAMP_SKIP"); v != "" {
		t.Fatalf("SKIP=%q", v)
	}
}

func TestMigratePackageADoesNotEnableCircularQuality(t *testing.T) {
	root := t.TempDir()
	a := NewWith(paths.Paths{ZatorRoot: root, StateDir: filepath.Join(root, "state")}, platform.Platform{})
	cfg := config.Parse(`Z2R_WSSIZE_RAMP_ENABLE=1
Z2R_WSSIZE_RAMP_MODE=full
NFQWS2_OPT="
#Z2R_AUTO_STANDARD_2_BEGIN
--filter-tcp=443 --hostlist-domains=googlevideo.com
--in-range=-s32768 --lua-desync=circular_locked:key=2:exclude_substrings=/opt/zator/lists/netrogat_substrings.txt:payload=tls_client_hello
--import=z2r_tcp_tls_common
#Z2R_AUTO_STANDARD_2_END
"
`)
	a.migratePackageA(cfg)
	opt, _ := cfg.NFQWS2Opt()
	if !strings.Contains(opt, "circular_locked:key=2:allow_nohost=1") || strings.Contains(opt, "circular_quality:key=2") {
		t.Fatalf("legacy Googlevideo handler changed during migration: %s", opt)
	}
}

func TestApplyZapretRuntimeWritesArgs(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{
		ZatorRoot:  root,
		StateDir:   filepath.Join(root, "state"),
		ConfigDir:  filepath.Join(root, "config"),
		ZapretRoot: filepath.Join(root, "zapret2"),
		LuaDir:     filepath.Join(root, "lua"),
	}
	p.ZapretConfig = filepath.Join(p.ZapretRoot, "config")
	if err := os.MkdirAll(p.ZapretRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	a := NewWith(p, platform.Platform{})

	cfg := config.Parse(`Z2R_WSSIZE_RAMP_ENABLE=1
Z2R_WSSIZE_MARK=0x04000000
Z2R_WSSIZE_RAMP_MODE=lite
Z2R_WSSIZE_RAMP_WSIZE=1
Z2R_WSSIZE_RAMP_SCALE=1
Z2R_WSSIZE_RAMP_OPEN=1
Z2R_WSSIZE_RAMP_ARGS=
Z2R_WSSIZE_RAMP_SKIP=--skip
NFQWS2_OPT="
#Z2R_AUTO_STANDARD_BEGIN
#Z2R_AUTO_STANDARD_3_BEGIN
--filter-tcp=443
"
`)
	cfg.SetPath(p.ZapretConfig)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApplyZapretRuntime(); err != nil {
		t.Fatalf("ApplyZapretRuntime: %v", err)
	}
	argsPath := filepath.Join(p.ZapretRoot, "nfqws2.args")
	b, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "--skip") {
		t.Fatalf("args still have --skip:\n%s", got)
	}
	if !strings.Contains(got, "wssize_ramp:mode=lite") {
		t.Fatalf("missing wssize args:\n%s", got)
	}
	if !strings.Contains(got, "--lua-init=@"+filepath.ToSlash(filepath.Join(p.ZapretRoot, "lua", "zapret-lib.lua"))) {
		t.Fatalf("missing zapret-lib init:\n%s", got)
	}
	if !strings.Contains(got, filepath.ToSlash(filepath.Join(p.LuaDir, "wssize-ramp.lua"))) {
		t.Fatalf("missing WSSIZE Lua module:\n%s", got)
	}
	live, err := config.Load(p.ZapretConfig)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := live.Get("Z2R_WSSIZE_RAMP_SKIP"); v != "" {
		t.Fatalf("live SKIP=%q", v)
	}
}

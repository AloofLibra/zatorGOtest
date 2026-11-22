package firewall

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// shSyntaxCheck выполняет `sh -n` для скрипта, если sh доступен.
func shSyntaxCheck(t *testing.T, script string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh не найден, проверка синтаксиса пропущена")
	}
	f := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(f, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		f = filepath.ToSlash(f)
	}
	if out, err := exec.Command(sh, "-n", f).CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s", err, out)
	}
}

func TestRenderWssizeHook(t *testing.T) {
	cfg := DefaultWssizeConfig("/opt/zator")
	script, err := RenderWssizeHook(cfg, "/opt/zator")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#!/bin/sh",
		"Z2R_GOOGLEVIDEO_SET=z2r_googlevideo4",
		`Z2R_WSSIZE_MARK="${Z2R_WSSIZE_MARK:-0x04000000}"`,
		`"${Z2R_SUPERSWEEP_MARK:-0x08000000}"`,
		"/opt/zator/tools/googlevideo-dns-listener.sh",
		"/opt/zator/lists/googlevideo-ipv4.txt",
		"zapret_custom_firewall()",
		"zapret_custom_firewall_nft()",
		"zapret_custom_firewall_nft_flush()",
		"zapret_custom_daemons()",
		"Z2R_WS_MARK", "Z2R_WS_OUT", "Z2R_WS_IN",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("hook missing %q", want)
		}
	}
	if strings.Contains(script, "@@") || strings.Contains(script, "\r") {
		t.Error("unresolved token or CR in rendered script")
	}
	shSyntaxCheck(t, script)

	// Нестандартные значения попадают в шаблон.
	cfg.Marks.Wssize = 0x02000000
	cfg.SetName = "gv4"
	script, err = RenderWssizeHook(cfg, "/data/zator")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "Z2R_GOOGLEVIDEO_SET=gv4") || !strings.Contains(script, "${Z2R_WSSIZE_MARK:-0x02000000}") ||
		!strings.Contains(script, "/data/zator/tools/") {
		t.Error("custom values not rendered")
	}
}

func TestRenderWssizeHookRejectsBadInput(t *testing.T) {
	cfg := DefaultWssizeConfig("/opt/zator")
	if _, err := RenderWssizeHook(cfg, "/opt/zator; rm -rf /"); !errors.Is(err, ErrUnsafeScriptValue) {
		t.Errorf("unsafe root: %v", err)
	}
	if _, err := RenderWssizeHook(cfg, "relative/path"); !errors.Is(err, ErrUnsafeScriptValue) {
		t.Errorf("relative root: %v", err)
	}
	cfg.SetName = "x;y"
	if _, err := RenderWssizeHook(cfg, "/opt/zator"); !errors.Is(err, ErrUnsafeScriptValue) {
		t.Errorf("unsafe set: %v", err)
	}
	cfg = DefaultWssizeConfig("/opt/zator")
	cfg.Marks.Wssize = cfg.Marks.Desync
	if _, err := RenderWssizeHook(cfg, "/opt/zator"); !errors.Is(err, ErrMarkCollision) {
		t.Errorf("collision: %v", err)
	}
}

func TestRenderClientScopeHook(t *testing.T) {
	cfg := DefaultClientScopeConfig("/opt/zator")
	script, err := RenderClientScopeHook(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#!/bin/sh",
		`CLIENT_SCOPE_CHAIN="${CLIENT_SCOPE_CHAIN:-ZATOR_CLIENT_SCOPE}"`,
		"CLIENT_SCOPE_NFT_TABLE=zator_client_scope",
		`CLIENT_SCOPE_MAP_FILE="${CLIENT_SCOPE_MAP_FILE:-/opt/zator/extra_strats/cache/client_scope.tsv}"`,
		`CLIENT_SCOPE_MARK_SHIFT="${CLIENT_SCOPE_MARK_SHIFT:-8}"`,
		`CLIENT_SCOPE_MARK_MASK="${CLIENT_SCOPE_MARK_MASK:-0x0000ff00}"`,
		"priority -190",
		"zapret_custom_firewall()",
		"zapret_custom_firewall_nft()",
		"zapret_custom_firewall_nft_flush()",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("hook missing %q", want)
		}
	}
	if strings.Contains(script, "@@") || strings.Contains(script, "\r") {
		t.Error("unresolved token or CR in rendered script")
	}
	// Скрипт должен быть POSIX sh: без bash-ismов.
	for _, bashism := range []string{"[[", "<<<", "BASH_SOURCE", "declare ", "read -a"} {
		if strings.Contains(script, bashism) {
			t.Errorf("bashism %q in custom.d hook", bashism)
		}
	}
	shSyntaxCheck(t, script)
}

func TestRenderClientScopeHookRejectsBadInput(t *testing.T) {
	for name, mutate := range map[string]func(*ClientScopeConfig){
		"chain":   func(c *ClientScopeConfig) { c.IptablesChain = "A;B" },
		"table":   func(c *ClientScopeConfig) { c.IptablesTable = "mangle; reboot" },
		"nft":     func(c *ClientScopeConfig) { c.NftChain = "$(id)" },
		"comment": func(c *ClientScopeConfig) { c.Comment = `x"; reboot; "` },
		"quote":   func(c *ClientScopeConfig) { c.Comment = "it's" },
		"map":     func(c *ClientScopeConfig) { c.MapFile = "/tmp/a b; reboot" },
		"shift":   func(c *ClientScopeConfig) { c.Shift = 40 },
	} {
		cfg := DefaultClientScopeConfig("/opt/zator")
		mutate(&cfg)
		if _, err := RenderClientScopeHook(cfg); !errors.Is(err, ErrUnsafeScriptValue) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestWriteAndRemoveCustomD(t *testing.T) {
	root := t.TempDir()
	script, err := RenderWssizeHook(DefaultWssizeConfig("/opt/zator"), "/opt/zator")
	if err != nil {
		t.Fatal(err)
	}
	p, err := WriteCustomD(root, CustomDSysV, CustomDWssizeName, script)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "init.d", "sysv", "custom.d", "90-z2r-wssize-ramp.sh"); p != want {
		t.Errorf("path = %q, want %q", p, want)
	}
	data, err := os.ReadFile(p)
	if err != nil || string(data) != script {
		t.Fatalf("content mismatch: %v", err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm()&0o100 == 0 {
			t.Errorf("hook must be executable: %v", st.Mode())
		}
	}

	// Перезапись.
	if _, err := WriteCustomD(root, CustomDSysV, CustomDWssizeName, "#!/bin/sh\n"); err != nil {
		t.Fatal(err)
	}
	if data, _ = os.ReadFile(p); string(data) != "#!/bin/sh\n" {
		t.Error("overwrite failed")
	}

	// OpenWrt-вариант и client scope.
	cs, _ := RenderClientScopeHook(DefaultClientScopeConfig("/opt/zator"))
	p2, err := WriteCustomD(root, CustomDOpenWrt, CustomDClientScopeName, cs)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "init.d", "openwrt", "custom.d", "95-zator-client-scope.sh"); p2 != want {
		t.Errorf("path = %q", p2)
	}

	// OpenRC-вариант.
	p3, err := WriteCustomD(root, CustomDOpenRC, CustomDWssizeName, script)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "init.d", "openrc", "custom.d", "90-z2r-wssize-ramp.sh"); p3 != want {
		t.Errorf("openrc path = %q, want %q", p3, want)
	}
	if err := RemoveCustomD(root, CustomDOpenRC, CustomDWssizeName); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p3); !os.IsNotExist(err) {
		t.Error("openrc hook must be removed")
	}

	for _, bad := range []string{"", "../x", "a/b", `a\b`, ".", ".."} {
		if _, err := WriteCustomD(root, CustomDSysV, bad, "x"); err == nil {
			t.Errorf("name %q must be rejected", bad)
		}
	}
	if _, err := WriteCustomD(root, "bsd", "x", "x"); err == nil {
		t.Error("unknown flavor must be rejected")
	}

	if err := RemoveCustomD(root, CustomDSysV, CustomDWssizeName); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("file must be removed")
	}
	if err := RemoveCustomD(root, CustomDSysV, CustomDWssizeName); err != nil {
		t.Errorf("removing a missing hook must not fail: %v", err)
	}
	if err := RemoveCustomD(root, CustomDSysV, "../x"); err == nil {
		t.Error("path traversal must be rejected")
	}
}

func TestCustomDHelpers(t *testing.T) {
	if CustomDDir("/opt/zapret2", CustomDOpenWrt) != filepath.Join("/opt/zapret2", "init.d", "openwrt", "custom.d") {
		t.Error("CustomDDir")
	}
	if FlavorForInit(true) != CustomDOpenWrt || FlavorForInit(false) != CustomDSysV {
		t.Error("FlavorForInit")
	}
}

func TestRenderTemplateUnresolvedToken(t *testing.T) {
	if _, err := renderTemplate("a @@X@@ @@Y@@", map[string]string{"X": "1"}); err == nil {
		t.Error("unresolved token must be reported")
	}
}

package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewDefaults(t *testing.T) {
	p := New("", "")
	if p.ZatorRoot != filepath.Clean(DefaultZatorRoot) {
		t.Errorf("ZatorRoot = %q", p.ZatorRoot)
	}
	if p.ZapretConfig != filepath.Join(filepath.Clean(DefaultZapretRoot), "config") {
		t.Errorf("ZapretConfig = %q", p.ZapretConfig)
	}
	if want := filepath.Join(p.ExtraStratsDir, "cache", "orchestra", "locked.tsv"); p.LockedTSV != want {
		t.Errorf("LockedTSV = %q, want %q", p.LockedTSV, want)
	}
	// locked.manual.tsv (точка, не дефис) — как в orchestra и Lua.
	if want := filepath.Join(p.ExtraStratsDir, "cache", "orchestra", "locked.manual.tsv"); p.LockedManualTSV != want {
		t.Errorf("LockedManualTSV = %q, want %q", p.LockedManualTSV, want)
	}
	if p.StateOrchestraDir() != filepath.Join(p.ExtraStratsDir, "cache", "orchestra") {
		t.Errorf("StateOrchestraDir = %q", p.StateOrchestraDir())
	}
	if want := filepath.Join(p.ConfigDir, "zator.json"); p.ZatorConfig != want {
		t.Errorf("ZatorConfig = %q, want %q", p.ZatorConfig, want)
	}
}

func TestFromEnv(t *testing.T) {
	zr, zp := t.TempDir(), t.TempDir()
	t.Setenv(EnvZatorRoot, zr)
	t.Setenv(EnvZapretRoot, zp)
	p := FromEnv()
	if p.ZatorRoot != filepath.Clean(zr) || p.ZapretRoot != filepath.Clean(zp) {
		t.Fatalf("env not applied: %+v", p)
	}
	if p.BinDir != filepath.Join(zr, "bin") {
		t.Errorf("BinDir = %q", p.BinDir)
	}
}

func TestEnsureDirsAndInstalled(t *testing.T) {
	p := New(t.TempDir(), t.TempDir())
	if p.Installed() {
		t.Fatal("не должно быть установлено до EnsureDirs/конфига")
	}
	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, d := range p.Dirs() {
		if !dirExists(d) {
			t.Errorf("каталог %s не создан", d)
		}
	}
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

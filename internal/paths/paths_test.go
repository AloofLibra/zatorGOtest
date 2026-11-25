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
	if want := filepath.Join(p.StateDir, "orchestra", "locked.tsv"); p.LockedTSV != want {
		t.Errorf("LockedTSV = %q, want %q", p.LockedTSV, want)
	}
	// locked.manual.tsv (точка, не дефис) — как в orchestra и Lua.
	if want := filepath.Join(p.StateDir, "orchestra", "locked.manual.tsv"); p.LockedManualTSV != want {
		t.Errorf("LockedManualTSV = %q, want %q", p.LockedManualTSV, want)
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

func Test160Helpers(t *testing.T) {
	p := New("/opt/zator", "/opt/zapret2")
	checks := map[string]string{
		p.CustomBlobDir():       filepath.Join(p.FakeDir, "custom_blob"),
		p.ToolsBinDir():         filepath.Join(p.ZatorRoot, "tools", "bin"),
		p.CustomStrategiesTSV(): filepath.Join(p.StateOrchestraDir(), "custom_strategies.tsv"),
		p.CustomBlobsTSV():      filepath.Join(p.StateOrchestraDir(), "custom_blobs.tsv"),
		p.BlobFilesTSV():        filepath.Join(p.StateOrchestraDir(), "blob_files.tsv"),
		p.SNIPoolTSV():          filepath.Join(p.StateOrchestraDir(), "sni_pool.tsv"),
		p.SNIPoolPolicyTSV():    filepath.Join(p.StateOrchestraDir(), "sni_pool_policy.tsv"),
		p.SNIPoolPin():          filepath.Join(p.StateOrchestraDir(), "sni_pool.pin"),
		p.SNIZatorTxt():         filepath.Join(p.ExtraStratsDir, "sni_zator.txt"),
		p.SNIUserTxt():          filepath.Join(p.ExtraStratsDir, "sni_user.txt"),
	}
	for got, want := range checks {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

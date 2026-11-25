package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/customblobs"
	"github.com/AloofLibra/zator/internal/customstrats"
	"github.com/AloofLibra/zator/internal/orchestra"
	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/platform"
)

func TestEnsureOrchestraStateDefaultSNIPoolAndOverlays(t *testing.T) {
	root := t.TempDir()
	p := paths.New(filepath.Join(root, "zator"), filepath.Join(root, "zapret2"))
	if err := os.MkdirAll(p.ZapretRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.FakeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.FakeDir, "tls_clienthello_5.bin"), []byte("tls"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.FakeDir, "quic_custom.bin"), []byte("quic"), 0o644); err != nil {
		t.Fatal(err)
	}

	rawCfg := `NFQWS2_OPT="
--lua-init=@/opt/zator/lua/locked.lua
--blob=maxru:@/opt/zator/files/fake/tls_clienthello_max_ru.bin
--blob=z2r_prof_5:@/opt/zator/files/fake/quic_initial_www_google_com.bin
--template=z2r_tcp_tls_common
--lua-desync=fake:blob=maxru:repeats=1:strategy=43
--lua-desync=circular_locked:key=1:strategy=1
"
`
	if err := os.WriteFile(p.ZapretConfig, []byte(rawCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	a := NewWith(p, platform.Platform{Kind: platform.KindVPS})
	if err := a.ensureOrchestraState(); err != nil {
		t.Fatalf("ensureOrchestraState: %v", err)
	}
	poolData, err := os.ReadFile(p.SNIPoolTSV())
	if err != nil {
		t.Fatalf("read sni_pool.tsv: %v", err)
	}
	if !strings.Contains(string(poolData), "1\t@google") || !strings.Contains(string(poolData), "2\t@google") {
		t.Fatalf("unexpected default sni_pool.tsv:\n%s", poolData)
	}

	// Seed blob_files, custom_blobs and custom_strategies, then reapply on fresh config load.
	orch := orchestra.NewStore(p.StateOrchestraDir())
	if err := orch.SetBlobFile("z2r_prof_5", "quic_custom.bin"); err != nil {
		t.Fatal(err)
	}
	cb := customblobs.NewStore(p.CustomBlobsTSV(), p.CustomBlobDir())
	cfg, _ := config.Load(p.ZapretConfig)
	if _, err := cb.Upload("userfake.bin", []byte("FAKEBYTES"), cfg); err != nil {
		t.Fatal(err)
	}
	cs := customstrats.NewStore(p.CustomStrategiesTSV(), p.CustomBlobsTSV(), orch)
	if _, err := cs.Add(customstrats.BlockTPL, "fake:blob=userfake:repeats=2", "user", cfg); err != nil {
		t.Fatal(err)
	}

	changed, err := a.ReapplyConfigOverlays(cfg)
	if err != nil || !changed {
		t.Fatalf("ReapplyConfigOverlays: changed=%v err=%v", changed, err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	out := cfg.String()
	if !strings.Contains(out, "--blob=z2r_prof_5:@/opt/zator/files/fake/quic_custom.bin") {
		t.Errorf("z2r_prof_5 blob not applied:\n%s", out)
	}
	if !strings.Contains(out, "--blob=userfake:@") {
		t.Errorf("custom blob declaration missing:\n%s", out)
	}
	if !strings.Contains(out, "--lua-desync=fake:blob=userfake:repeats=2:strategy=44") {
		t.Errorf("custom strategy missing:\n%s", out)
	}
}

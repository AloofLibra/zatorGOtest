package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCustomMarkersAndStockMax(t *testing.T) {
	legacy := `NFQWS2_OPT="
--blob=maxru:@/opt/zator/files/fake/tls_clienthello_max_ru.bin
--template=z2r_tcp_tls_common
--lua-desync=fake:blob=maxru:strategy=44
--new
#Z2R_AUTO_STANDARD_4_BEGIN
--lua-desync=circular_locked:key=4
--lua-desync=fake:blob=maxru:strategy=28
#Z2R_AUTO_STANDARD_4_END
--new
--lua-desync=circular_locked:key=5:proto=udp
--lua-desync=fake:blob=quic_google:strategy=32
--new
--lua-desync=circular_locked:key=6:proto=udp
--lua-desync=fake:blob=stun_fake:strategy=32
--new
--lua-desync=circular_locked:key=7:proto=udp
--lua-desync=fake:blob=quic_google:strategy=20
--new
"
`
	c := Parse(legacy)
	if err := c.EnsureCustomBlobMarkers(); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureCustomStrategyMarkers(); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.ReadMarkerSection(CustomBlobsBegin, CustomBlobsEnd); !ok {
		t.Fatal("missing custom blobs markers")
	}
	if err := c.ReplaceMarkerSection(CustomBlobsBegin, CustomBlobsEnd, []string{"--blob=my_fake:@/opt/zator/files/fake/custom_blob/my_fake.bin"}); err != nil {
		t.Fatal(err)
	}
	if err := c.ReplaceMarkerSection(CustomTplBegin, CustomTplEnd, []string{"--lua-desync=fake:blob=my_fake:strategy=45"}); err != nil {
		t.Fatal(err)
	}

	maxTpl, err := c.BlockStockMaxStrategy("tpl")
	if err != nil || maxTpl != 44 {
		t.Fatalf("BlockStockMaxStrategy(tpl) = %d, %v (want 44)", maxTpl, err)
	}
	maxP4, err := c.BlockStockMaxStrategy("p4")
	if err != nil || maxP4 != 28 {
		t.Fatalf("BlockStockMaxStrategy(p4) = %d, %v (want 28)", maxP4, err)
	}

	withoutCustom := c.DeclaredBlobNames(false)
	withCustom := c.DeclaredBlobNames(true)
	if strings.Contains(strings.Join(withoutCustom, ","), "my_fake") {
		t.Fatalf("stock blobs must exclude custom section: %v", withoutCustom)
	}
	if !strings.Contains(strings.Join(withCustom, ","), "my_fake") {
		t.Fatalf("all blobs must include custom section: %v", withCustom)
	}
}

func TestConfigDefaultHas160Markers(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "assets", "config.default"))
	if err != nil {
		t.Fatal(err)
	}
	c := Parse(string(data))
	opt, _ := c.NFQWS2Opt()
	if !strings.Contains(opt, "--lua-init=@/opt/zator/lua/sni-pool.lua") {
		t.Fatal("config.default missing sni-pool.lua")
	}
	if !strings.Contains(opt, "--blob=z2r_prof_5:@/opt/zator/files/fake/quic_initial_www_google_com.bin") {
		t.Fatal("config.default missing z2r_prof_5")
	}
	for _, blk := range []string{"tpl", "p4", "p5", "p6", "p7"} {
		mb, me, _ := CustomBlockMarkers(blk)
		if _, ok := c.ReadMarkerSection(mb, me); !ok {
			t.Fatalf("config.default missing markers for %s", blk)
		}
	}
	if _, ok := c.ReadMarkerSection(CustomBlobsBegin, CustomBlobsEnd); !ok {
		t.Fatal("config.default missing custom blobs markers")
	}
}

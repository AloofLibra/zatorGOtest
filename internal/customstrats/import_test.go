package customstrats

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestJoinWrapsAndImportBlockcheck2(t *testing.T) {
	cfg, _ := loadSampleConfig(t, true)
	dir := t.TempDir()
	st := NewStore(filepath.Join(dir, "custom_strategies.tsv"), "", nil)

	// Includes: wrapped continuation, blockcheck2 header, unknown blob remap, combo strategy=7, non-combo lines, and zapret v1 rejection.
	input := `
* SUMMARY
# comment ignored
curl_test_https_tls12 ipv4 rutracker.org : nfqws2 --blob=tlsgoogle:@/tmp/tls.bin --payload=tls_client_hello --out-range=-d10 --lua-desync=fake:blob=tlsgoogle:repeats=2 --lua-desync=multispl
it:pos=midsld:strategy=5
nfqws2 --lua-desync=fake:blob=unknown_other:badsum:strategy=1
--lua-desync=fake:blob=maxru:strategy=7
--lua-desync=send:repeats=3:strategy=7
--lua-desync=fake:blob=maxru
--lua-desync=multisplit:pos=1
curl_test_https_tls12 ipv4 example.com : nfqws --dpi-desync=fake
`

	res, err := st.Import(BlockTPL, input, cfg)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Added != 5 || res.Failed != 1 || res.Dups != 0 {
		t.Fatalf("unexpected ImportResult: %+v", res)
	}
	rows, _ := st.List("")
	if rows[0].Comment != "blockcheck rutracker.org" {
		t.Errorf("row[0].Comment=%q", rows[0].Comment)
	}
	if rows[0].Spec != "fake:blob=maxru:repeats=2 --lua-desync=multisplit:pos=midsld" {
		t.Errorf("row[0].Spec=%q", rows[0].Spec)
	}
	if rows[1].Comment != "импорт" || rows[1].Spec != "fake:blob=maxru:badsum" {
		t.Errorf("row[1]=%+v", rows[1])
	}
	if rows[2].Spec != "fake:blob=maxru --lua-desync=send:repeats=3" {
		t.Errorf("combo join failed: row[2].Spec=%q", rows[2].Spec)
	}
	if len(res.Notes) < 2 {
		t.Errorf("expected blob substitution notes, got %+v", res.Notes)
	}
	if !strings.Contains(res.Errors[0].Reason, "zapret v1") {
		t.Errorf("expected zapret v1 error, got %+v", res.Errors[0])
	}

	// Re-importing the exact same text must classify all valid specs as duplicates.
	res2, err := st.Import(BlockTPL, "--lua-desync=fake:blob=maxru\n--lua-desync=fake:blob=maxru\n", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Added != 0 || res2.Dups != 2 {
		t.Fatalf("expected 0 added / 2 dups, got %+v", res2)
	}
}

func TestImportUDPAndCloneTargets(t *testing.T) {
	cfg, _ := loadSampleConfig(t, true)
	dir := t.TempDir()
	st := NewStore(filepath.Join(dir, "custom_strategies.tsv"), "", nil)

	res, err := st.Import(BlockP5, "nfqws2 --lua-desync=fake:blob=mystery_quic:repeats=4:ip_id=rnd\n", cfg)
	if err != nil || res.Added != 1 {
		t.Fatalf("UDP import: %+v err=%v", res, err)
	}
	rows, _ := st.List(BlockP5)
	if rows[0].Spec != "fake:blob=quic_google:repeats=4:ip_id=rnd" {
		t.Fatalf("expected quic_google fallback, got %q", rows[0].Spec)
	}
}

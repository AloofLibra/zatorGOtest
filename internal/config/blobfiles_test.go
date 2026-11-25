package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlotBlobFileAndApplyBlobFiles(t *testing.T) {
	fakeDir := t.TempDir()
	for _, name := range []string{"tls_clienthello_5.bin", "quic_5.bin"} {
		if err := os.WriteFile(filepath.Join(fakeDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := Parse(`NFQWS2_OPT="
--blob=maxru:@/opt/zator/files/fake/tls_clienthello_max_ru.bin
--blob=z2r_prof_2:@/opt/zator/files/fake/tls_clienthello_max_ru.bin
--blob=z2r_prof_5:@/opt/zapret2/files/fake/quic_initial_www_google_com.bin
--lua-desync=fake:blob=fake_default_tls:strategy=1
--lua-desync=multisplit:seqovl=666:seqovl_pattern=custom:strategy=26
"
`)
	changed, err := c.ApplyBlobFiles(map[string]string{
		"maxru":      "tls_clienthello_5.bin",
		"z2r_prof_2": "tls_clienthello_5.bin",
		"z2r_prof_5": "quic_5.bin",
	}, fakeDir)
	if err != nil || !changed {
		t.Fatalf("ApplyBlobFiles = %v, %v", changed, err)
	}
	if f, ok := c.SlotBlobFile("maxru"); !ok || f != "tls_clienthello_5.bin" {
		t.Fatalf("maxru = %q, %v", f, ok)
	}
	if f, ok := c.SlotBlobFile("z2r_prof_5"); !ok || f != "quic_5.bin" {
		t.Fatalf("z2r_prof_5 = %q, %v", f, ok)
	}
	if m := c.TLSBlobModeValue(); m != "maxru" {
		t.Fatalf("TLSBlobModeValue = %q", m)
	}
}

func TestEnsureSlotBlob(t *testing.T) {
	c := Parse(`NFQWS2_OPT="
--blob=maxru:@/opt/zator/files/fake/tls_clienthello_max_ru.bin
#Z2R_CUSTOM_BLOBS_BEGIN
#Z2R_CUSTOM_BLOBS_END
--template=z2r_tcp_tls_common
"
`)
	added, err := c.EnsureSlotBlob("z2r_prof_5", "quic_initial_www_google_com.bin")
	if err != nil || !added {
		t.Fatalf("EnsureSlotBlob = %v, %v", added, err)
	}
	if f, ok := c.SlotBlobFile("z2r_prof_5"); !ok || f != "quic_initial_www_google_com.bin" {
		t.Fatalf("SlotBlobFile = %q, %v", f, ok)
	}
	added2, err := c.EnsureSlotBlob("z2r_prof_5", "other.bin")
	if err != nil || added2 {
		t.Fatalf("second EnsureSlotBlob should be noop: added=%v err=%v", added2, err)
	}
}

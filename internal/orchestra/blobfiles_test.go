package orchestra

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBlobFilesAndLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	if !IsCloneProfile("1") || IsCloneProfile("5") {
		t.Fatal("IsCloneProfile mismatch")
	}
	if !IsBlobOverrideProfile("5") || IsBlobOverrideProfile("6") {
		t.Fatal("IsBlobOverrideProfile mismatch")
	}
	if !ValidBlobFileForProfile("quic_initial_www_google_com.bin", "5") {
		t.Fatal("expected valid QUIC blob")
	}
	if ValidBlobFileForProfile("tls_clienthello_1.bin", "5") {
		t.Fatal("TLS blob must be rejected for profile 5")
	}
	if !ValidBlobFileForProfile("tls_clienthello_1.bin", "2") || !ValidBlobFileForProfile("custom_tls.bin", "1") {
		t.Fatal("expected valid TLS blob")
	}

	if err := s.SetBlobFile("maxru", "tls_clienthello_5.bin"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBlobFile("z2r_prof_5", "quic_5.bin"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetBlobFile("z2r_prof_5")
	if err != nil || !ok || got != "quic_5.bin" {
		t.Fatalf("GetBlobFile = %q, %v, %v", got, ok, err)
	}
	if err := s.ClearBlobFile("maxru"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetBlobFile("maxru"); ok {
		t.Fatal("expected maxru cleared")
	}

	// Legacy migration from 10a70bc (profile\tfile_without_bin -> profile\tz2r_prof_N + blob_files.tsv)
	if err := os.WriteFile(filepath.Join(dir, BlobOverrideFileName), []byte("2\ttls_clienthello_5\n8\tfake_default_tls\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := s.MigrateLegacyBlobOverrides(
		func(slot string) string { return "tls_clienthello_5.bin" },
		func(file string) bool { return file == "tls_clienthello_5.bin" },
	)
	if err != nil || n != 1 {
		t.Fatalf("MigrateLegacyBlobOverrides = %d, %v", n, err)
	}
	v, ok, err := s.GetOverride(OverrideBlob, "2")
	if err != nil || !ok || v != "z2r_prof_2" {
		t.Fatalf("after migration blob_override[2] = %q, %v, %v", v, ok, err)
	}
	bf, ok, err := s.GetBlobFile("z2r_prof_2")
	if err != nil || !ok || bf != "tls_clienthello_5.bin" {
		t.Fatalf("after migration blob_files[z2r_prof_2] = %q, %v, %v", bf, ok, err)
	}
}

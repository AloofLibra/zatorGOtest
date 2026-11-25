package backup

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildBash160Archive(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range entries {
		data := []byte(body)
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompat160ArchiveLayoutAndRoundTrip(t *testing.T) {
	srcArchive := filepath.Join(t.TempDir(), "z2r_backup_20261125_120000.tar")
	bashEntries := map[string]string{
		"extra_strats/cache/orchestra/locked.tsv":            "1\ttls\t5\n5\tudp\t2\n",
		"extra_strats/cache/orchestra/blob_files.tsv":        "z2r_prof_5\tquic_initial_www_google_com.bin\n",
		"extra_strats/cache/orchestra/custom_blobs.tsv":      "1\tbash_b\tbash_fake.bin\tb160\n",
		"extra_strats/cache/orchestra/custom_strategies.tsv": "1\tp5\tfake:blob=bash_b:repeats=4\t1\tb160\t12\n",
		"extra_strats/cache/orchestra/sni_pool.tsv":          "1\t@google\n2\t@my_set\n",
		"extra_strats/cache/orchestra/sni_pool_policy.tsv":   "2\trand\n",
		"extra_strats/sni_user.txt":                          "# set: my_set\nexample.com\n",
		"files/fake/custom_blob/bash_fake.bin":               "BASH160BLOB",
		"config": oldConfig +
			"Z2R_WSSIZE_RAMP_ENABLE=1\nZ2R_WSSIZE_RAMP_MODE=full\nZ2R_WSSIZE_RAMP_SCALE=2\n",
	}
	buildBash160Archive(t, srcArchive, bashEntries)

	mid := newEnv(t)
	res, err := mid.m.RestoreFile(srcArchive, RestoreOptions{Mode: ModeFull})
	if err != nil {
		t.Fatalf("RestoreFile bash 1.6.0 tar: %v", err)
	}
	if !res.ConfigReplaced {
		t.Fatalf("expected ConfigReplaced=true, got %+v", res)
	}

	checks := map[string]string{
		mid.p.LockedTSV:                                     bashEntries["extra_strats/cache/orchestra/locked.tsv"],
		mid.p.BlobFilesTSV():                                bashEntries["extra_strats/cache/orchestra/blob_files.tsv"],
		mid.p.CustomBlobsTSV():                              bashEntries["extra_strats/cache/orchestra/custom_blobs.tsv"],
		mid.p.CustomStrategiesTSV():                         bashEntries["extra_strats/cache/orchestra/custom_strategies.tsv"],
		mid.p.SNIPoolTSV():                                  bashEntries["extra_strats/cache/orchestra/sni_pool.tsv"],
		mid.p.SNIPoolPolicyTSV():                            bashEntries["extra_strats/cache/orchestra/sni_pool_policy.tsv"],
		mid.p.SNIUserTxt():                                  bashEntries["extra_strats/sni_user.txt"],
		filepath.Join(mid.p.CustomBlobDir(), "bash_fake.bin"): "BASH160BLOB",
	}
	for path, want := range checks {
		if got := read(t, path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}

	// Re-pack from Go Zator and restore into clean destination (smart mode).
	created, err := mid.m.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("mid Create: %v", err)
	}
	dst := newEnv(t)
	write(t, dst.p.ZapretConfig, liveConfig+"# wssize_ramp\nZ2R_WSSIZE_RAMP_ENABLE=0\nZ2R_WSSIZE_RAMP_MODE=lite\nZ2R_WSSIZE_RAMP_SCALE=1\n")
	smartRes, err := dst.m.RestoreFile(created.Path, RestoreOptions{Mode: ModeSmart})
	if err != nil {
		t.Fatalf("dst smart restore: %v", err)
	}
	if !strings.Contains(read(t, dst.p.ZapretConfig), "Z2R_WSSIZE_RAMP_ENABLE=1") {
		t.Errorf("Z2R_WSSIZE_RAMP_ENABLE not transferred in smart mode: %+v", smartRes.SmartApplied)
	}
	if read(t, dst.p.BlobFilesTSV()) != bashEntries["extra_strats/cache/orchestra/blob_files.tsv"] {
		t.Errorf("round-trip blob_files.tsv mismatch")
	}
	if read(t, filepath.Join(dst.p.CustomBlobDir(), "bash_fake.bin")) != "BASH160BLOB" {
		t.Errorf("round-trip custom blob binary mismatch")
	}
}

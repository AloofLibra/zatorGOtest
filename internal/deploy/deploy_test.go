package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/paths"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.1", -1},
		{"v1.2.0", "1.2.0", 0},
		{"1.0.0-rc1", "1.0.0", -1},
		{"2.0.0", "1.9.9", 1},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q,%q)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestParseLatestAndRun(t *testing.T) {
	root := t.TempDir()
	p := paths.New(filepath.Join(root, "zator"), filepath.Join(root, "zapret2"))
	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	payload := []byte("hello-zator\n")
	sum := sha256.Sum256(payload)
	sumHex := hex.EncodeToString(sum[:])
	rel := "lua/hello.lua"
	manifest := fmt.Sprintf("%s|/opt/zator/%s|auto|%s|%d|0\n", rel, rel, sumHex, len(payload))
	verEnv := "ZATOR_VERSION=\"1.2.3\"\nZATOR_SHA=\"abc\"\nWEBUI_VERSION=\"1.2.3\"\nWEBUI_SHA=\"def\"\nTRACKING=\"latest\"\n"

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	writeTar := func(name string, data []byte) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))})
		_, _ = tw.Write(data)
	}
	writeTar(rel, payload)
	writeTar(ManifestRel, []byte(manifest))
	writeTar(VersionEnvRel, []byte(verEnv))
	_ = tw.Close()
	_ = gz.Close()
	archiveBytes := buf.Bytes()
	archSum := sha256.Sum256(archiveBytes)
	archHex := hex.EncodeToString(archSum[:])

	latest := Latest{
		SchemaVersion: 1,
		Release:       "1.2.3",
		ZatorSHA:      "newsha",
		WebUISHA:      "newweb",
		ZatorDate:     "2026-01-01",
		WebUIDate:     "2026-01-01",
		Assets: map[string]Asset{
			VariantFull: {
				Name:   "zator-full.tar.gz",
				Size:   int64(len(archiveBytes)),
				SHA256: archHex,
			},
		},
	}
	latestJSON, _ := json.Marshal(latest)

	mux := http.NewServeMux()
	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(latestJSON)
	})
	mux.HandleFunc("/zator-full.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archiveBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	u := New(p)
	u.LatestURL = srv.URL + "/latest.json"
	u.Offline = false

	ctx := context.Background()
	check, err := u.CheckLatest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !check.UpdateAvailable() {
		t.Fatalf("expected update available: %+v", check)
	}

	rep, err := u.Run(ctx, RunOptions{Variant: VariantFull, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Apply == nil || rep.Apply.Installed < 1 {
		t.Fatalf("apply: %+v", rep.Apply)
	}
	got, err := os.ReadFile(filepath.Join(p.ZatorRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("file content = %q", got)
	}
}

func TestDetectOfflineBundle(t *testing.T) {
	dir := t.TempDir()
	vendor := filepath.Join(dir, "vendor")
	_ = os.MkdirAll(vendor, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "zator-full.tar.gz"), []byte("x"), 0o644)
	st := DetectOffline(func(string) string { return "" }, dir)
	if !st.Offline || st.Reason != "bundle" || st.Bundle == nil {
		t.Fatalf("status = %+v", st)
	}
}

func TestKeepIfExists(t *testing.T) {
	root := t.TempDir()
	p := paths.New(filepath.Join(root, "zator"), filepath.Join(root, "zapret2"))
	_ = p.EnsureDirs()
	existing := []byte("user-list\n")
	targetRel := "lists/netrogat.txt"
	_ = os.MkdirAll(filepath.Join(p.ZatorRoot, "lists"), 0o755)
	_ = os.WriteFile(filepath.Join(p.ZatorRoot, filepath.FromSlash(targetRel)), existing, 0o644)

	newPayload := []byte("new-list\n")
	sum := sha256.Sum256(newPayload)
	manifest := fmt.Sprintf("%s|/opt/zator/%s|keep-if-exists|%s|%d|0\n", targetRel, targetRel, hex.EncodeToString(sum[:]), len(newPayload))
	staging := t.TempDir()
	_ = os.MkdirAll(filepath.Join(staging, "lists"), 0o755)
	_ = os.MkdirAll(filepath.Join(staging, filepath.FromSlash(filepath.Dir(ManifestRel))), 0o755)
	_ = os.WriteFile(filepath.Join(staging, filepath.FromSlash(targetRel)), newPayload, 0o644)
	_ = os.WriteFile(filepath.Join(staging, filepath.FromSlash(ManifestRel)), []byte(manifest), 0o644)

	u := New(p)
	res, err := u.Apply(staging, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kept != 1 {
		t.Fatalf("kept=%d installed=%d", res.Kept, res.Installed)
	}
	got, _ := os.ReadFile(filepath.Join(p.ZatorRoot, filepath.FromSlash(targetRel)))
	if !bytes.Equal(got, existing) {
		t.Fatalf("keep-if-exists overwritten: %q", got)
	}
}

func TestSafeAssetURL(t *testing.T) {
	u := &Updater{LatestURL: "https://example.com/releases/latest.json"}
	link, err := u.AssetURL(Asset{Name: "zator-core.tar.gz"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(link, "/zator-core.tar.gz") {
		t.Fatalf("url=%s", link)
	}
	_ = io.Discard
}

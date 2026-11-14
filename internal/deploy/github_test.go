package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/fetch"
	"github.com/AloofLibra/zator/internal/paths"
)

func TestLatestJSONURLUsesKoviand(t *testing.T) {
	t.Setenv(EnvOwner, "")
	t.Setenv(EnvRepo, "")
	t.Setenv(EnvLatestURL, "")
	got := LatestJSONURL()
	want := "https://github.com/Koviand/zatorGO/releases/download/latest/latest.json"
	if got != want {
		t.Fatalf("LatestJSONURL() = %q, want %q", got, want)
	}
}

func TestLatestJSONURLEnvOverride(t *testing.T) {
	t.Setenv(EnvOwner, "Acme")
	t.Setenv(EnvRepo, "zator")
	t.Setenv(EnvLatestURL, "")
	got := LatestJSONURL()
	want := "https://github.com/Acme/zator/releases/download/latest/latest.json"
	if got != want {
		t.Fatalf("LatestJSONURL() = %q, want %q", got, want)
	}
}

func TestFetchGitHubLatest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/Koviand/zatorGO/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     "v0.1.5",
			"published_at": "2026-11-13T05:00:00Z",
			"assets":       []any{},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	prev := GitHubAPIBase
	GitHubAPIBase = srv.URL
	defer func() { GitHubAPIBase = prev }()

	t.Setenv(EnvOwner, "Koviand")
	t.Setenv(EnvRepo, "zatorGO")

	u := New(paths.New(t.TempDir(), t.TempDir()))
	u.Offline = false
	l, err := u.FetchGitHubLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if l.Release != "v0.1.5" {
		t.Fatalf("release = %q", l.Release)
	}
}

func TestParseSHA256SUMS(t *testing.T) {
	body := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  zator-linux-amd64\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb *zator-linux-arm64\n")
	if got := parseSHA256SUMS(body, "zator-linux-amd64"); got != strings.Repeat("a", 64) {
		t.Fatalf("amd64 = %q", got)
	}
	if got := parseSHA256SUMS(body, "zator-linux-arm64"); got != strings.Repeat("b", 64) {
		t.Fatalf("arm64 = %q", got)
	}
	if got := parseSHA256SUMS(body, "missing"); got != "" {
		t.Fatalf("missing = %q", got)
	}
}

func TestApplyGitHubBinaryRejectsBadSHA(t *testing.T) {
	payload := []byte("fake-zator-binary")
	sum := sha256.Sum256(payload)
	good := hex.EncodeToString(sum[:])
	bad := strings.Repeat("0", 64)
	if bad == good {
		t.Fatal("unexpected collision")
	}
	wantName := binaryAssetName()

	mux := http.NewServeMux()
	var srvURL string
	mux.HandleFunc("/repos/Koviand/zatorGO/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v0.1.16",
			"assets": []map[string]any{
				{
					"name":                 wantName,
					"browser_download_url": srvURL + "/" + wantName,
					"size":                 len(payload),
				},
				{
					"name":                 "SHA256SUMS",
					"browser_download_url": srvURL + "/SHA256SUMS",
					"size":                 80,
				},
			},
		})
	})
	mux.HandleFunc("/"+wantName, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	})
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(bad + "  " + wantName + "\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	prev := GitHubAPIBase
	GitHubAPIBase = srv.URL
	defer func() { GitHubAPIBase = prev }()

	t.Setenv(EnvOwner, "Koviand")
	t.Setenv(EnvRepo, "zatorGO")

	root := t.TempDir()
	p := paths.New(filepath.Join(root, "zator"), filepath.Join(root, "zapret2"))
	u := New(p)
	u.Offline = false
	u.Fetch = fetch.NewClient()
	u.Fetch.HTTP = srv.Client()

	_, tag, err := u.ApplyGitHubBinary(context.Background())
	if err == nil {
		t.Fatal("ожидался отказ по SHA")
	}
	if !errors.Is(err, fetch.ErrChecksum) {
		t.Fatalf("err = %v, want ErrChecksum (tag=%s)", err, tag)
	}
}

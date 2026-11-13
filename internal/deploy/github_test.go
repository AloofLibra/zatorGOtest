package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AloofLibra/zator/internal/paths"
)

func TestLatestJSONURLUsesKoviand(t *testing.T) {
	t.Setenv(EnvOwner, "")
	t.Setenv(EnvRepo, "")
	t.Setenv(EnvLatestURL, "")
	got := LatestJSONURL()
	want := "https://github.com/Koviand/zatorGOtest/releases/download/latest/latest.json"
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
	mux.HandleFunc("/repos/Koviand/zatorGOtest/releases/latest", func(w http.ResponseWriter, r *http.Request) {
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
	t.Setenv(EnvRepo, "zatorGOtest")

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

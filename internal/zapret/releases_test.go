package zapret

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestListReleasesFiltersDraftsAssetsAndUnsafeURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/bol-van/zapret2/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"tag_name":     "v1.0.8",
					"name":         "v1.0.8",
					"published_at": "2026-11-25T08:32:59Z",
					"html_url":     "https://github.com/bol-van/zapret2/releases/tag/v1.0.8",
					"assets": []map[string]string{
						{"name": "zapret2-v1.0.8.tar.gz"},
						{"name": "zapret2-v1.0.8-openwrt-embedded.tar.gz"},
					},
				},
				{
					"tag_name":     "v1.0.7-reasm-fix", // недопустимый для official суффикс
					"published_at": "2026-11-05T08:01:55Z",
					"html_url":     "https://github.com/bol-van/zapret2/releases/tag/v1.0.7-reasm-fix",
					"assets":       []map[string]string{{"name": "zapret2-v1.0.7-reasm-fix.tar.gz"}},
				},
				{
					"tag_name":     "v0.8.4", // без архивов — должен быть отфильтрован
					"published_at": "2026-01-17T00:00:00Z",
					"html_url":     "https://github.com/bol-van/zapret2/releases/tag/v0.8.4",
					"assets":       []map[string]string{},
				},
				{
					"tag_name":     "v0.8.3",
					"draft":        true, // черновик — должен быть отфильтрован
					"published_at": "2026-01-09T00:00:00Z",
					"html_url":     "https://github.com/bol-van/zapret2/releases/tag/v0.8.3",
					"assets":       []map[string]string{{"name": "zapret2-v0.8.3.tar.gz"}},
				},
				{
					"tag_name":     "v0.8.2",
					"published_at": "2026-01-04T00:00:00Z",
					"html_url":     "javascript:alert(1)",
					"assets":       []map[string]string{{"name": "zapret2-v0.8.2.tar.gz"}},
				},
			})
		case "/repos/MarkinAlexander/zapret2/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"tag_name":     "v1.0.8-reasm-fix",
					"name":         "v1.0.8-reasm-fix",
					"published_at": "2026-11-25T20:56:54Z",
					"html_url":     "https://github.com/MarkinAlexander/zapret2/releases/tag/v1.0.8-reasm-fix",
					"assets": []map[string]string{
						{"name": "zapret2-v1.0.8-reasm-fix.tar.gz"},
						{"name": "zapret2-v1.0.8-reasm-fix-openwrt-embedded.tar.gz"},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	origBase := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = origBase })

	std, err := ListReleases(context.Background(), SourceOfficial, false, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if len(std) != 2 || std[0].Version != "1.0.8" || std[1].Version != "0.8.2" {
		t.Fatalf("unexpected official std releases: %+v", std)
	}
	if std[0].URL != "https://github.com/bol-van/zapret2/releases/tag/v1.0.8" || std[1].URL != "" {
		t.Fatalf("URL sanitization failed: %+v", std)
	}

	emb, err := ListReleases(context.Background(), SourceOfficial, true, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if len(emb) != 1 || emb[0].Version != "1.0.8" {
		t.Fatalf("expected only v1.0.8 for embedded, got %+v", emb)
	}

	fork, official, errs := ListAllReleases(context.Background(), false, srv.Client())
	if len(errs) != 0 || len(fork) != 1 || fork[0].Version != "1.0.8-reasm-fix" || len(official) != 2 {
		t.Fatalf("ListAllReleases = fork:%+v official:%+v errs:%v", fork, official, errs)
	}
}

func TestListAllReleasesPartialFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/MarkinAlexander/zapret2/releases" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"tag_name": "v1.0.8-reasm-fix",
					"assets":   []map[string]string{{"name": "zapret2-v1.0.8-reasm-fix.tar.gz"}},
				},
			})
			return
		}
		http.Error(w, "upstream down", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	origBase := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = origBase })

	fork, official, errs := ListAllReleases(context.Background(), false, srv.Client())
	if len(fork) != 1 || fork[0].Version != "1.0.8-reasm-fix" {
		t.Fatalf("expected fork release preserved on official failure: %+v", fork)
	}
	if official == nil || len(official) != 0 {
		t.Fatalf("expected empty non-nil official slice: %#v", official)
	}
	if errs[SourceOfficial] == "" || errs[SourceFork] != "" {
		t.Fatalf("errs = %+v", errs)
	}
}

func TestParseNfqws2VersionOutputAndDetectInstalled(t *testing.T) {
	cases := []struct {
		in      string
		wantVer string
		wantSrc Source
		wantOK  bool
	}{
		{"github version v1.0.8-reasm-fix (deadbeef) lua_compat_ver 5\n", "1.0.8-reasm-fix", SourceFork, true},
		{"github android version v1.0.8 (abc1234) lua_compat_ver 5", "1.0.8", SourceOfficial, true},
		{"self-built version Nov 25 2026 12:00:00 lua_compat_ver 5", "self-built", "", true},
		{"usage: nfqws2 ...", "", "", false},
	}
	for _, tc := range cases {
		ver, src, ok := ParseNfqws2VersionOutput(tc.in)
		if ver != tc.wantVer || src != tc.wantSrc || ok != tc.wantOK {
			t.Errorf("Parse(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, ver, src, ok, tc.wantVer, tc.wantSrc, tc.wantOK)
		}
	}

	root := t.TempDir()
	if _, ok := DetectInstalled(context.Background(), root); ok {
		t.Fatal("expected false on empty root")
	}
	if err := writeInstalled(root, Installed{Version: "1.0.7-reasm-fix", Source: SourceFork, ArchDir: "linux-x86_64"}); err != nil {
		t.Fatal(err)
	}
	in, ok := DetectInstalled(context.Background(), root)
	if !ok || in.Version != "1.0.7-reasm-fix" || in.Source != SourceFork || in.ArchDir != "linux-x86_64" {
		t.Fatalf("DetectInstalled = (%+v, %v)", in, ok)
	}
	if err := os.Remove(filepath.Join(root, VersionFile)); err != nil {
		t.Fatal(err)
	}
	if _, ok := DetectInstalled(context.Background(), root); ok {
		t.Fatal("expected false after removing version file without nfqws2 binary")
	}
}

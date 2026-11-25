package zapret

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Release описывает доступный релиз zapret2 в выбранном источнике.
type Release struct {
	Source      Source `json:"source"`
	Version     string `json:"version"`
	Tag         string `json:"tag"`
	Name        string `json:"name,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	Prerelease  bool   `json:"prerelease,omitempty"`
	URL         string `json:"url,omitempty"`
}

var (
	githubAPIBase = "https://api.github.com"

	ghVersionRe = regexp.MustCompile(`(?i)github(?:\s+android)?\s+version\s+v?([0-9][0-9A-Za-z._-]*)`)
	selfBuiltRe = regexp.MustCompile(`(?i)self-built(?:\s+android)?\s+version\s+([^\r\n]+)`)
)

// ListReleases загружает список релизов zapret2 из GitHub Releases API для
// указанного источника, отфильтровывая черновики и релизы без нужного архива.
func ListReleases(ctx context.Context, s Source, embedded bool, client *http.Client) ([]Release, error) {
	if !s.Valid() {
		return nil, fmt.Errorf("zapret: неизвестный источник %q", s)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100",
		strings.TrimRight(githubAPIBase, "/"), s.Owner(), repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "zator")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zapret: GitHub API %s: HTTP %d", url, resp.StatusCode)
	}

	var raw []struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
		PublishedAt string `json:"published_at"`
		HTMLURL     string `json:"html_url"`
		Assets      []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("zapret: разбор релизов %s: %w", s, err)
	}

	releases := []Release{}
	for _, item := range raw {
		if item.Draft {
			continue
		}
		ver, err := NormalizeVersion(s, item.TagName)
		if err != nil {
			continue
		}
		wantAsset := AssetName(ver, embedded)
		hasAsset := false
		for _, a := range item.Assets {
			if a.Name == wantAsset {
				hasAsset = true
				break
			}
		}
		if !hasAsset {
			continue
		}
		safeURL := ""
		if strings.HasPrefix(item.HTMLURL, "https://github.com/") {
			safeURL = item.HTMLURL
		}
		releases = append(releases, Release{
			Source:      s,
			Version:     ver,
			Tag:         "v" + ver,
			Name:        strings.TrimSpace(item.Name),
			PublishedAt: item.PublishedAt,
			Prerelease:  item.Prerelease,
			URL:         safeURL,
		})
	}
	return releases, nil
}

// ListAllReleases параллельно запрашивает списки релизов для форка (reasm-fix)
// и официального репозитория, возвращая частичные данные при сбое одного из них.
func ListAllReleases(ctx context.Context, embedded bool, client *http.Client) (fork, official []Release, errs map[Source]string) {
	fork = []Release{}
	official = []Release{}
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		out = map[Source]string{}
	)
	fetchOne := func(s Source, dst *[]Release) {
		defer wg.Done()
		rels, err := ListReleases(ctx, s, embedded, client)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			out[s] = err.Error()
			return
		}
		if rels != nil {
			*dst = rels
		}
	}
	wg.Add(2)
	go fetchOne(SourceFork, &fork)
	go fetchOne(SourceOfficial, &official)
	wg.Wait()
	return fork, official, out
}

// DetectInstalled возвращает сведения об установленном zapret2: сначала по
// маркеру zator-zapret.version, а при его отсутствии (установка через z2r.sh)
// — через `nfqws2 --version`.
func DetectInstalled(ctx context.Context, root string) (Installed, bool) {
	if strings.TrimSpace(root) == "" {
		return Installed{}, false
	}
	if in, err := ReadInstalled(root); err == nil {
		return in, true
	}
	bin := Nfqws2Path(root)
	if !fileExists(bin) {
		return Installed{}, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	out, err := exec.CommandContext(cctx, bin, "--version").CombinedOutput()
	ver, src, ok := ParseNfqws2VersionOutput(string(out))
	if !ok {
		if err != nil {
			return Installed{}, false
		}
		return Installed{}, false
	}
	return Installed{Version: ver, Source: src}, true
}

// ParseNfqws2VersionOutput извлекает версию и вероятный источник из вывода `nfqws2 --version`.
func ParseNfqws2VersionOutput(out string) (version string, source Source, ok bool) {
	if m := ghVersionRe.FindStringSubmatch(out); len(m) == 2 {
		ver := strings.TrimSpace(m[1])
		src := SourceOfficial
		if strings.Contains(ver, "-") {
			src = SourceFork
		}
		return ver, src, true
	}
	if m := selfBuiltRe.FindStringSubmatch(out); len(m) == 2 {
		return "self-built", "", true
	}
	return "", "", false
}

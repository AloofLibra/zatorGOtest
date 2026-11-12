package zapret

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/fetch"
)

// Source — источник сборки zapret2.
type Source string

const (
	// SourceOfficial — официальные релизы bol-van/zapret2.
	SourceOfficial Source = "official"
	// SourceFork — форк MarkinAlexander/zapret2 (официальный код + патч
	// TLS reasm для платформ с аппаратным fastpath).
	SourceFork Source = "fork"

	// DefaultSource — источник по умолчанию (как в z2r.sh: форк).
	DefaultSource = SourceFork
	// DefaultVer — запасная версия форка, если GitHub API недоступен.
	// Актуальные теги форка: v1.0.7-reasm-fix и новее (0.8.2 больше нет).
	DefaultVer = "1.0.7-reasm-fix"

	officialOwner = "bol-van"
	forkOwner     = "MarkinAlexander"
	repoName      = "zapret2"

	// Переменные окружения, совместимые с z2r.sh.
	EnvReleaseBase       = "ZAPRET2_RELEASE_BASE"
	EnvForkReleaseBase   = "ZAPRET2_FORK_RELEASE_BASE"
	EnvReleaseMirrorBase = "ZAPRET2_RELEASE_MIRROR_BASE"
)

// Variant — вариант архива релиза.
type Variant string

const (
	// VariantAuto — выбирается по платформе (OpenWRT → embedded).
	VariantAuto Variant = ""
	// VariantStandard — zapret2-v<ver>.tar.gz.
	VariantStandard Variant = "standard"
	// VariantOpenWRTEmbedded — zapret2-v<ver>-openwrt-embedded.tar.gz.
	VariantOpenWRTEmbedded Variant = "openwrt-embedded"
)

var (
	officialVerRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
	forkVerRe     = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*(-[A-Za-z0-9._-]+)?$`)
)

// Valid сообщает, известен ли источник.
func (s Source) Valid() bool { return s == SourceOfficial || s == SourceFork }

// ParseSource разбирает строку источника; пустая строка — DefaultSource.
func ParseSource(s string) (Source, error) {
	switch Source(strings.ToLower(strings.TrimSpace(s))) {
	case "":
		return DefaultSource, nil
	case SourceOfficial:
		return SourceOfficial, nil
	case SourceFork:
		return SourceFork, nil
	}
	return "", fmt.Errorf("zapret: неизвестный источник %q (official|fork)", s)
}

// Owner возвращает владельца репозитория GitHub.
func (s Source) Owner() string {
	if s == SourceOfficial {
		return officialOwner
	}
	return forkOwner
}

// DefaultVersion возвращает запасную версию для источника.
func DefaultVersion(s Source) string {
	if s == SourceOfficial {
		return "1.0.7"
	}
	return DefaultVer
}

// NormalizeVersion убирает префикс «v», подставляет DefaultVersion для пустой
// строки и проверяет формат для источника. Значение "latest" здесь не
// допускается — его обрабатывает ResolveVersion.
func NormalizeVersion(s Source, v string) (string, error) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		v = DefaultVersion(s)
	}
	re := officialVerRe
	if s == SourceFork {
		re = forkVerRe
	}
	if len(v) > 40 || !re.MatchString(v) {
		return "", fmt.Errorf("zapret: некорректная версия %q для источника %s", v, s)
	}
	return v, nil
}

// ResolveVersion возвращает версию: явная → (при "latest") GitHub API → DefaultVersion.
// Пустая строка = DefaultVersion без сети (удобно для тестов и офлайна).
func ResolveVersion(ctx context.Context, s Source, v string, client *http.Client) (string, error) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if strings.EqualFold(v, "latest") {
		if tag, err := LatestReleaseTag(ctx, s, client); err == nil && tag != "" {
			if n, nerr := NormalizeVersion(s, tag); nerr == nil {
				return n, nil
			}
		}
		return NormalizeVersion(s, DefaultVersion(s))
	}
	return NormalizeVersion(s, v)
}

// LatestReleaseTag читает последний tag_name из GitHub Releases API.
func LatestReleaseTag(ctx context.Context, s Source, client *http.Client) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", s.Owner(), repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "zator")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("zapret: GitHub API %s: HTTP %d", url, resp.StatusCode)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	tag := strings.TrimSpace(payload.TagName)
	if tag == "" {
		return "", fmt.Errorf("zapret: пустой tag_name")
	}
	return tag, nil
}

// AssetName — имя архива релиза.
func AssetName(version string, embedded bool) string {
	if embedded {
		return "zapret2-v" + version + "-openwrt-embedded.tar.gz"
	}
	return "zapret2-v" + version + ".tar.gz"
}

// ReleaseURL строит URL архива. base — необязательная база вида
// https://host/path (тогда URL = base/v<ver>/<asset>); пустая база означает
// GitHub релизы выбранного источника (с учётом переменных окружения
// ZAPRET2_RELEASE_BASE / ZAPRET2_FORK_RELEASE_BASE).
func ReleaseURL(s Source, base, version string, embedded bool) string {
	asset := AssetName(version, embedded)
	tag := "v" + version
	if base == "" {
		if s == SourceFork {
			base = os.Getenv(EnvForkReleaseBase)
		} else {
			base = os.Getenv(EnvReleaseBase)
		}
	}
	if base != "" {
		return fetch.ReleaseBaseURL(base, tag, asset)
	}
	return fetch.GitHubReleaseURL(s.Owner(), repoName, tag, asset)
}

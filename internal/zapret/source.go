package zapret

import (
	"fmt"
	"os"
	"regexp"
	"strings"

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
	// DefaultVer — версия, используемая, когда не задана иная
	// (DEFAULT_VER из z2r.sh).
	DefaultVer = "0.8.2"

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

// NormalizeVersion убирает префикс «v», подставляет DefaultVer для пустой
// строки и проверяет формат для источника.
func NormalizeVersion(s Source, v string) (string, error) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		v = DefaultVer
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

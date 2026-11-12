package fetch

import (
	"net/url"
	"strings"
)

const (
	// GitHubHost — хост релизов GitHub.
	GitHubHost = "github.com"
	// GitHubRawHost — хост raw-файлов GitHub.
	GitHubRawHost = "raw.githubusercontent.com"
	// PxMirrorBase — зеркало git.px.rkn.quest (формат из z2r.sh):
	// <base>/<repo>/plain/<path>?h=<ref>.
	PxMirrorBase = "https://git.px.rkn.quest"
)

// Mirror переписывает URL в адрес зеркала. Второе значение false означает,
// что зеркало к данному URL неприменимо.
type Mirror func(url string) (string, bool)

// GitHubReleaseURL — адрес ассета релиза:
// https://github.com/<owner>/<repo>/releases/download/<tag>/<asset>.
func GitHubReleaseURL(owner, repo, tag, asset string) string {
	return "https://" + GitHubHost + "/" + owner + "/" + repo + "/releases/download/" + tag + "/" + asset
}

// ReleaseBaseURL собирает <base>/<tag>/<asset> (формат ZAPRET2_RELEASE_BASE
// и ZAPRET2_RELEASE_MIRROR_BASE из z2r.sh).
func ReleaseBaseURL(base, tag, asset string) string {
	return strings.TrimRight(base, "/") + "/" + tag + "/" + asset
}

// GitHubRawURL — адрес файла в репозитории через raw.githubusercontent.com.
func GitHubRawURL(owner, repo, ref, path string) string {
	return "https://" + GitHubRawHost + "/" + owner + "/" + repo + "/" + ref + "/" + strings.TrimLeft(path, "/")
}

// PxRawURL — адрес файла на зеркале в стиле git.px.rkn.quest:
// <base>/plain/<path>?h=<ref>, где base включает репозиторий
// (например https://git.px.rkn.quest/zapret2).
func PxRawURL(repoBase, ref, path string) string {
	return strings.TrimRight(repoBase, "/") + "/plain/" + strings.TrimLeft(path, "/") + "?h=" + url.QueryEscape(ref)
}

// ParseGitHubRelease разбирает URL ассета релиза GitHub.
func ParseGitHubRelease(raw string) (owner, repo, tag, asset string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Host, GitHubHost) {
		return "", "", "", "", false
	}
	// /<owner>/<repo>/releases/download/<tag>/<asset>
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 6 || parts[2] != "releases" || parts[3] != "download" {
		return "", "", "", "", false
	}
	return parts[0], parts[1], parts[4], parts[5], true
}

// ParseGitHubRaw разбирает URL raw.githubusercontent.com.
func ParseGitHubRaw(raw string) (owner, repo, ref, path string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Host, GitHubRawHost) {
		return "", "", "", "", false
	}
	parts := strings.SplitN(strings.Trim(u.Path, "/"), "/", 4)
	if len(parts) != 4 {
		return "", "", "", "", false
	}
	return parts[0], parts[1], parts[2], parts[3], true
}

// ReleaseMirror возвращает зеркало, переписывающее ассеты релизов GitHub
// в <base>/<tag>/<asset>. Пустая база отключает зеркало.
func ReleaseMirror(base string) Mirror {
	return func(raw string) (string, bool) {
		if strings.TrimSpace(base) == "" {
			return "", false
		}
		_, _, tag, asset, ok := ParseGitHubRelease(raw)
		if !ok {
			return "", false
		}
		return ReleaseBaseURL(base, tag, asset), true
	}
}

// PxMirror возвращает зеркало в стиле git.px.rkn.quest для raw-файлов GitHub:
// raw.githubusercontent.com/<o>/<r>/<ref>/<path> → <repoBase>/plain/<path>?h=<ref>.
// Пустая база отключает зеркало.
func PxMirror(repoBase string) Mirror {
	return func(raw string) (string, bool) {
		if strings.TrimSpace(repoBase) == "" {
			return "", false
		}
		_, _, ref, path, ok := ParseGitHubRaw(raw)
		if !ok {
			return "", false
		}
		return PxRawURL(repoBase, ref, path), true
	}
}

// HostMirror заменяет схему и хост (например, github.com → зеркало-прокси),
// сохраняя путь и запрос. from сравнивается с хостом без учёта регистра.
func HostMirror(from, toBase string) Mirror {
	return func(raw string) (string, bool) {
		if strings.TrimSpace(toBase) == "" {
			return "", false
		}
		u, err := url.Parse(raw)
		if err != nil || !strings.EqualFold(u.Host, from) {
			return "", false
		}
		out := strings.TrimRight(toBase, "/") + u.EscapedPath()
		if u.RawQuery != "" {
			out += "?" + u.RawQuery
		}
		return out, true
	}
}

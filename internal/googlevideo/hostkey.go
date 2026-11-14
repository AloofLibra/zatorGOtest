package googlevideo

import (
	"regexp"
	"strings"
)

var (
	reRRGooglevideo = regexp.MustCompile(`(?i)^rr\d+---sn-([^.]+)\.googlevideo\.com$`)
	reSNGooglevideo = regexp.MustCompile(`(?i)^sn-([^.]+)\.googlevideo\.com$`)
)

// SNIHostKey нормализует media-узел googlevideo к ключу sn-<id>
// (общий для rr1/rr2/rr5 одного POP). Совпадает с slm_googlevideo_hostkey в Lua.
// Не-media хосты (redirector и т.п.) возвращают ("", false).
func SNIHostKey(hostname string) (string, bool) {
	h := strings.TrimSpace(strings.ToLower(hostname))
	h = strings.Trim(h, ".")
	if h == "" {
		return "", false
	}
	if m := reRRGooglevideo.FindStringSubmatch(h); len(m) == 2 && m[1] != "" {
		return "sn-" + m[1], true
	}
	if m := reSNGooglevideo.FindStringSubmatch(h); len(m) == 2 && m[1] != "" {
		return "sn-" + m[1], true
	}
	return "", false
}

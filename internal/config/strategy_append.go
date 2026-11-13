package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	luaDesyncPrefixRe = regexp.MustCompile(`^\s*--lua-desync=`)
	strategyTokenRe   = regexp.MustCompile(`:strategy=[0-9]+`)
)

// NormalizeDesyncLine приводит строку стратегии к виду с :strategy=N.
// Принимает одну или несколько частей `--lua-desync=…` (через пробел/строки)
// и/или «сырой» desync без префикса (fake:… / multisplit:…).
func NormalizeDesyncLine(raw string, strategy int) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("пустая строка стратегии")
	}
	if strategy < 1 {
		return "", fmt.Errorf("некорректный номер стратегии")
	}
	parts := splitDesyncParts(raw)
	if len(parts) == 0 {
		return "", fmt.Errorf("не найдено ни одного --lua-desync")
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "--lua-desync=") {
			p = "--lua-desync=" + p
		}
		body := strings.TrimPrefix(p, "--lua-desync=")
		// Запрет shell-metachar: NFQWS2_OPT="…" в config — кавычка/backtick/$ ломают quoting.
		if body == "" || strings.ContainsAny(body, "\n\r\"'`$;\\|&<>()") {
			return "", fmt.Errorf("некорректный фрагмент desync")
		}
		body = strategyTokenRe.ReplaceAllString(body, "")
		body = strings.TrimRight(body, ":")
		out = append(out, "--lua-desync="+body+":strategy="+strconv.Itoa(strategy))
	}
	if len(out) == 0 {
		return "", fmt.Errorf("некорректная строка стратегии")
	}
	return strings.Join(out, " "), nil
}

func splitDesyncParts(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	if strings.Contains(raw, "\n") {
		var parts []string
		for _, ln := range strings.Split(raw, "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" || strings.HasPrefix(ln, "#") {
				continue
			}
			parts = append(parts, ln)
		}
		return parts
	}
	if !strings.Contains(raw, "--lua-desync=") {
		return []string{raw}
	}
	idxs := make([]int, 0, 4)
	from := 0
	for {
		i := strings.Index(raw[from:], "--lua-desync=")
		if i < 0 {
			break
		}
		idxs = append(idxs, from+i)
		from += i + len("--lua-desync=")
	}
	parts := make([]string, 0, len(idxs))
	for i, start := range idxs {
		end := len(raw)
		if i+1 < len(idxs) {
			end = idxs[i+1]
		}
		parts = append(parts, strings.TrimSpace(raw[start:end]))
	}
	return parts
}

// AppendProfileStrategy добавляет строку desync в блок профиля как strategy=max+1.
// Возвращает назначенный номер стратегии.
func (c *Config) AppendProfileStrategy(id int, desync string) (int, error) {
	if !ValidProfile(id) {
		return 0, fmt.Errorf("некорректный профиль")
	}
	n := c.ProfileMaxStrategyOrDefault(id) + 1
	line, err := NormalizeDesyncLine(desync, n)
	if err != nil {
		return 0, err
	}
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrNotFound, OptVar)
	}
	next, err := insertStrategyLine(opt, id, line)
	if err != nil {
		return 0, err
	}
	if err := c.SetNFQWS2Opt(next); err != nil {
		return 0, err
	}
	return n, nil
}

// insertStrategyLine вставляет line в keyed/fallback-блок профиля внутри текста NFQWS2_OPT.
func insertStrategyLine(opt string, id int, line string) (string, error) {
	lines := strings.Split(opt, "\n")
	keyRe := regexp.MustCompile(`--lua-desync=(circular_locked|circular_quality|rst_guard_locked):key=` +
		strconv.Itoa(id) + `([^0-9]|$)`)

	var beginMark, endMark string
	switch id {
	case ProfileFallbackT:
		beginMark, endMark = "#Z2R_FALLBACK_BEGIN", "#Z2R_FALLBACK_END"
	case ProfileFallbackH:
		beginMark, endMark = "#Z2R_FALLBACK_HTTP_BEGIN", "#Z2R_FALLBACK_HTTP_END"
	}

	inTemplate := false
	inFallback := false
	keyActive := false
	insertAt := -1
	lastInBlock := -1

	for i, ln := range lines {
		if beginMark != "" && strings.Contains(ln, beginMark) {
			inFallback = true
		}
		if endMark != "" && strings.Contains(ln, endMark) {
			if inFallback && insertAt < 0 && lastInBlock >= 0 {
				insertAt = lastInBlock + 1
			}
			inFallback = false
			keyActive = false
			continue
		}

		if templateLineRe.MatchString(ln) {
			inTemplate = true
			keyActive = false
			continue
		}
		if newLineRe.MatchString(ln) {
			if keyActive && insertAt < 0 && lastInBlock >= 0 {
				insertAt = lastInBlock + 1
			}
			inTemplate = false
			keyActive = false
			continue
		}
		if inTemplate {
			continue
		}

		if keyRe.MatchString(ln) {
			keyActive = true
		}

		active := keyActive || (beginMark != "" && inFallback)
		if !active {
			continue
		}
		trim := strings.TrimSpace(ln)
		if luaDesyncPrefixRe.MatchString(trim) || importLineRe.MatchString(trim) || strategyRe.MatchString(trim) {
			lastInBlock = i
		}
	}
	if insertAt < 0 && lastInBlock >= 0 {
		insertAt = lastInBlock + 1
	}
	if insertAt < 0 {
		return "", fmt.Errorf("%w: блок профиля %d", ErrNotFound, id)
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insertAt]...)
	out = append(out, line)
	out = append(out, lines[insertAt:]...)
	return strings.Join(out, "\n"), nil
}

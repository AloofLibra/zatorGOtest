package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Маркеры пользовательских секций (паритет с zator 1.6.0).
const (
	CustomBlobsBegin = "#Z2R_CUSTOM_BLOBS_BEGIN"
	CustomBlobsEnd   = "#Z2R_CUSTOM_BLOBS_END"

	CustomTplBegin = "#Z2R_CUSTOM_TPL_BEGIN"
	CustomTplEnd   = "#Z2R_CUSTOM_TPL_END"
	CustomP4Begin  = "#Z2R_CUSTOM_P4_BEGIN"
	CustomP4End    = "#Z2R_CUSTOM_P4_END"
	CustomP5Begin  = "#Z2R_CUSTOM_P5_BEGIN"
	CustomP5End    = "#Z2R_CUSTOM_P5_END"
	CustomP6Begin  = "#Z2R_CUSTOM_P6_BEGIN"
	CustomP6End    = "#Z2R_CUSTOM_P6_END"
	CustomP7Begin  = "#Z2R_CUSTOM_P7_BEGIN"
	CustomP7End    = "#Z2R_CUSTOM_P7_END"
)

var (
	blobDeclNameRe = regexp.MustCompile(`^\s*--blob=([A-Za-z0-9_]+):`)
	tplRegionStart = regexp.MustCompile(`^--template=z2r_tcp_tls_common(\s|$)`)
	p4RegionStart  = regexp.MustCompile(`^#Z2R_AUTO_STANDARD_4_BEGIN\s*$`)
	p4RegionEnd    = regexp.MustCompile(`^#Z2R_AUTO_STANDARD_4_END\s*$`)
	p5RegionStart  = regexp.MustCompile(`^--lua-desync=(?:circular_locked|circular_quality|rst_guard_locked):key=5(?:[^0-9]|$)`)
	p6RegionStart  = regexp.MustCompile(`^--lua-desync=(?:circular_locked|circular_quality|rst_guard_locked):key=6(?:[^0-9]|$)`)
	p7RegionStart  = regexp.MustCompile(`^--lua-desync=(?:circular_locked|circular_quality|rst_guard_locked):key=7(?:[^0-9]|$)`)
)

// CustomBlockMarkers возвращает пару маркеров (#Z2R_CUSTOM_<BLK>_BEGIN/END) для блока.
func CustomBlockMarkers(block string) (begin, end string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(block)) {
	case "tpl":
		return CustomTplBegin, CustomTplEnd, true
	case "p4":
		return CustomP4Begin, CustomP4End, true
	case "p5":
		return CustomP5Begin, CustomP5End, true
	case "p6":
		return CustomP6Begin, CustomP6End, true
	case "p7":
		return CustomP7Begin, CustomP7End, true
	default:
		return "", "", false
	}
}

// ReadMarkerSection возвращает строки между beginMarker и endMarker (без самих маркеров).
func (c *Config) ReadMarkerSection(beginMarker, endMarker string) ([]string, bool) {
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return nil, false
	}
	lines := strings.Split(opt, "\n")
	start := -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if start < 0 {
			if t == beginMarker {
				start = i
			}
			continue
		}
		if t == endMarker {
			out := make([]string, 0, i-start-1)
			for _, item := range lines[start+1 : i] {
				if strings.TrimSpace(item) != "" {
					out = append(out, item)
				}
			}
			return out, true
		}
	}
	return nil, false
}

// ReplaceMarkerSection заменяет тело секции между beginMarker и endMarker.
func (c *Config) ReplaceMarkerSection(beginMarker, endMarker string, body []string) error {
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, OptVar)
	}
	lines := strings.Split(opt, "\n")
	bIdx, eIdx := -1, -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if bIdx < 0 {
			if t == beginMarker {
				bIdx = i
			}
			continue
		}
		if t == endMarker {
			eIdx = i
			break
		}
	}
	if bIdx < 0 || eIdx < 0 {
		return fmt.Errorf("%w: маркеры %s / %s", ErrNotFound, beginMarker, endMarker)
	}
	next := make([]string, 0, len(lines)-(eIdx-bIdx-1)+len(body))
	next = append(next, lines[:bIdx+1]...)
	next = append(next, body...)
	next = append(next, lines[eIdx:]...)
	return c.SetNFQWS2Opt(strings.Join(next, "\n"))
}

// EnsureCustomBlobMarkers добавляет #Z2R_CUSTOM_BLOBS_BEGIN/END после штатных --blob=
// (перед --template=z2r_tcp_tls_common), если в конфиге их ещё нет.
func (c *Config) EnsureCustomBlobMarkers() error {
	if _, ok := c.ReadMarkerSection(CustomBlobsBegin, CustomBlobsEnd); ok {
		return nil
	}
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, OptVar)
	}
	lines := strings.Split(opt, "\n")
	insertAt := -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "--blob=") {
			insertAt = i + 1
		}
		if tplRegionStart.MatchString(t) {
			if insertAt < 0 {
				insertAt = i
			}
			break
		}
	}
	if insertAt < 0 {
		for i, ln := range lines {
			if strings.HasPrefix(strings.TrimSpace(ln), "--lua-init=") {
				insertAt = i + 1
			}
		}
	}
	if insertAt < 0 {
		insertAt = 0
	}
	ins := []string{
		"# Пользовательские блобы (панель: «Блобы и SNI», /opt/zator/files/fake/custom_blob).",
		CustomBlobsBegin,
		CustomBlobsEnd,
	}
	next := make([]string, 0, len(lines)+len(ins))
	next = append(next, lines[:insertAt]...)
	next = append(next, ins...)
	next = append(next, lines[insertAt:]...)
	return c.SetNFQWS2Opt(strings.Join(next, "\n"))
}

func regionBoundsForBlock(lines []string, block string) (startRe, endRe *regexp.Regexp, ok bool) {
	switch block {
	case "tpl":
		return tplRegionStart, newLineRe, true
	case "p4":
		return p4RegionStart, p4RegionEnd, true
	case "p5":
		return p5RegionStart, newLineRe, true
	case "p6":
		return p6RegionStart, newLineRe, true
	case "p7":
		return p7RegionStart, newLineRe, true
	default:
		return nil, nil, false
	}
}

// EnsureCustomStrategyMarkers добавляет отсутствующие #Z2R_CUSTOM_{TPL,P4,P5,P6,P7}_BEGIN/END.
func (c *Config) EnsureCustomStrategyMarkers() error {
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, OptVar)
	}
	lines := strings.Split(opt, "\n")
	changed := false
	for _, block := range []string{"tpl", "p4", "p5", "p6", "p7"} {
		mb, me, _ := CustomBlockMarkers(block)
		hasBegin := false
		for _, ln := range lines {
			if strings.TrimSpace(ln) == mb {
				hasBegin = true
				break
			}
		}
		if hasBegin {
			continue
		}
		startRe, endRe, _ := regionBoundsForBlock(lines, block)
		inRegion := false
		insertAt := -1
		for i, ln := range lines {
			t := strings.TrimSpace(ln)
			if startRe.MatchString(t) {
				inRegion = true
				continue
			}
			if inRegion && endRe.MatchString(t) {
				insertAt = i
				break
			}
		}
		if insertAt < 0 && inRegion {
			insertAt = len(lines)
		}
		if insertAt < 0 {
			continue
		}
		next := make([]string, 0, len(lines)+2)
		next = append(next, lines[:insertAt]...)
		next = append(next, mb, me)
		next = append(next, lines[insertAt:]...)
		lines = next
		changed = true
	}
	if !changed {
		return nil
	}
	return c.SetNFQWS2Opt(strings.Join(lines, "\n"))
}

// BlockStockMaxStrategy возвращает максимум `:strategy=N` в штатной части блока
// (`tpl`, `p4`, `p5`, `p6`, `p7`), исключая содержимое `#Z2R_CUSTOM_<BLK>_BEGIN/END`.
func (c *Config) BlockStockMaxStrategy(block string) (int, error) {
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrNotFound, OptVar)
	}
	mb, me, valid := CustomBlockMarkers(block)
	if !valid {
		return 0, fmt.Errorf("%w: неизвестный блок %q", ErrInvalid, block)
	}
	lines := strings.Split(opt, "\n")
	startRe, endRe, _ := regionBoundsForBlock(lines, block)
	inRegion := false
	inCustom := false
	found := false
	var stock []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if !inRegion {
			if startRe.MatchString(t) {
				inRegion = true
				found = true
			}
			continue
		}
		if t == mb {
			inCustom = true
			continue
		}
		if t == me {
			inCustom = false
			continue
		}
		if endRe.MatchString(t) {
			break
		}
		if !inCustom {
			stock = append(stock, ln)
		}
	}
	if !found {
		return 0, fmt.Errorf("%w: блок %s не найден в конфиге", ErrNotFound, block)
	}
	return maxStrategyIn(stock), nil
}

// DeclaredBlobNames возвращает отсортированный список объявленных `--blob=ИМЯ:...` в NFQWS2_OPT.
// Если includeCustom == false, декларации между #Z2R_CUSTOM_BLOBS_BEGIN/END исключаются.
func (c *Config) DeclaredBlobNames(includeCustom bool) []string {
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return nil
	}
	lines := strings.Split(opt, "\n")
	inCustom := false
	seen := make(map[string]bool)
	var out []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == CustomBlobsBegin {
			inCustom = true
			continue
		}
		if t == CustomBlobsEnd {
			inCustom = false
			continue
		}
		if inCustom && !includeCustom {
			continue
		}
		if m := blobDeclNameRe.FindStringSubmatch(ln); m != nil {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	return out
}

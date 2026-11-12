package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Маркеры блоков стратегий в конфиге имеют вид `#Z2R_<NAME>_BEGIN` /
// `#Z2R_<NAME>_END` (допустимы ведущие пробелы), например:
//
//	#Z2R_AUTO_STANDARD_3_BEGIN … #Z2R_AUTO_STANDARD_3_END
//	#Z2R_FALLBACK_BEGIN … #Z2R_FALLBACK_END
const (
	markerPrefix   = "#Z2R_"
	markerBeginSfx = "_BEGIN"
	markerEndSfx   = "_END"
)

// Имена блоков, которые используют остальные части zator.
const (
	BlockAutoStandard  = "AUTO_STANDARD" // оболочка всех «стандартных» блоков
	BlockAuto          = "AUTO"          // оболочка блоков авторотации
	BlockTCP443        = "TCP443"
	BlockDNS           = "DNS"
	BlockFallbackTLS   = "FALLBACK"
	BlockFallbackHTTP  = "FALLBACK_HTTP"
	BlockWireGuard     = "WG"
	BlockQUIC443       = "QUIC443"
	autoModeOnMarker   = "#Z2R_AUTO_MODE=1"
	autoModeOffMarker  = "#Z2R_AUTO_MODE=0"
	autoFallbackWasKey = "#Z2R_AUTO_FALLBACK_WAS="
)

var (
	beginMarkerRe = regexp.MustCompile(`^#Z2R_([A-Za-z0-9_]+)_BEGIN$`)
	standardIDRe  = regexp.MustCompile(`^#Z2R_AUTO_STANDARD_([A-Za-z0-9]+)_BEGIN$`)
	autoIDRe      = regexp.MustCompile(`^#Z2R_AUTO_([A-Za-z0-9]+)_BEGIN$`)
	strategyRe    = regexp.MustCompile(`strategy=([0-9]+)`)
)

// Block — блок строк между маркерами BEGIN/END.
type Block struct {
	Name  string   // имя без префикса и суффиксов, напр. "AUTO_STANDARD_3"
	Begin int      // индекс строки с маркером BEGIN
	End   int      // индекс строки с маркером END
	Lines []string // строки между маркерами (без самих маркеров)
}

// Text возвращает тело блока одной строкой (строки соединены "\n").
func (b Block) Text() string { return strings.Join(b.Lines, "\n") }

// MaxStrategy возвращает максимальное N среди вхождений `strategy=N` в теле блока.
func (b Block) MaxStrategy() int { return maxStrategyIn(b.Lines) }

func maxStrategyIn(lines []string) int {
	best := 0
	for _, ln := range lines {
		for _, m := range strategyRe.FindAllStringSubmatch(ln, -1) {
			if n, err := strconv.Atoi(m[1]); err == nil && n > best {
				best = n
			}
		}
	}
	return best
}

func beginMarker(name string) string { return markerPrefix + name + markerBeginSfx }
func endMarker(name string) string   { return markerPrefix + name + markerEndSfx }

// FindBlock ищет блок по имени (`AUTO_STANDARD_3`, `FALLBACK`, `DNS`, …).
// Берётся первая пара BEGIN/END; если END отсутствует, блок не найден.
func (c *Config) FindBlock(name string) (Block, bool) {
	begin, end := beginMarker(name), endMarker(name)
	b := -1
	for i, ln := range c.lines {
		t := strings.TrimSpace(ln)
		if b < 0 {
			if t == begin {
				b = i
			}
			continue
		}
		if t == end {
			return Block{
				Name:  name,
				Begin: b,
				End:   i,
				Lines: append([]string(nil), c.lines[b+1:i]...),
			}, true
		}
	}
	return Block{}, false
}

// Blocks возвращает все корректные блоки в порядке их BEGIN-маркеров
// (вложенные блоки перечисляются отдельно).
func (c *Config) Blocks() []Block {
	var out []Block
	seen := map[string]bool{}
	for _, ln := range c.lines {
		m := beginMarkerRe.FindStringSubmatch(strings.TrimSpace(ln))
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		if b, ok := c.FindBlock(m[1]); ok {
			out = append(out, b)
		}
	}
	return out
}

// ReplaceBlockBody заменяет строки между маркерами блока name на body.
func (c *Config) ReplaceBlockBody(name string, body []string) error {
	b, ok := c.FindBlock(name)
	if !ok {
		return fmt.Errorf("%w: block %s", ErrNotFound, name)
	}
	c.spliceLines(b.Begin+1, b.End, body)
	return nil
}

// AutoStandardBlock возвращает блок `#Z2R_AUTO_STANDARD_<id>_BEGIN/END`.
func (c *Config) AutoStandardBlock(id string) (Block, bool) {
	return c.FindBlock("AUTO_STANDARD_" + id)
}

// AutoBlock возвращает блок авторотации `#Z2R_AUTO_<id>_BEGIN/END`.
func (c *Config) AutoBlock(id string) (Block, bool) {
	return c.FindBlock("AUTO_" + id)
}

// AutoStandardIDs возвращает id стандартных блоков в порядке появления
// (например 3, 1, 2, 4, 8, 3S, 9).
func (c *Config) AutoStandardIDs() []string { return c.collectIDs(standardIDRe, "") }

// AutoIDs возвращает id блоков авторотации (3, 4, 9).
func (c *Config) AutoIDs() []string { return c.collectIDs(autoIDRe, "STANDARD") }

func (c *Config) collectIDs(re *regexp.Regexp, skip string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, ln := range c.lines {
		m := re.FindStringSubmatch(strings.TrimSpace(ln))
		if m == nil || m[1] == skip || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		ids = append(ids, m[1])
	}
	return ids
}

// AutoModeFlag возвращает значение маркера `#Z2R_AUTO_MODE=0|1`;
// ok=false, если маркера нет.
func (c *Config) AutoModeFlag() (on bool, ok bool) {
	for _, ln := range c.lines {
		switch strings.TrimRight(ln, " \t") {
		case autoModeOnMarker:
			return true, true
		case autoModeOffMarker:
			return false, true
		}
	}
	return false, false
}

// SetAutoModeFlag переключает маркер `#Z2R_AUTO_MODE`.
func (c *Config) SetAutoModeFlag(on bool) error {
	val := "0"
	if on {
		val = "1"
	}
	for i, ln := range c.lines {
		t := strings.TrimRight(ln, " \t")
		if t == autoModeOnMarker || t == autoModeOffMarker {
			c.lines[i] = "#Z2R_AUTO_MODE=" + val
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrNotFound, "#Z2R_AUTO_MODE marker")
}

// Package firewall генерирует и применяет правила iptables/nftables для zator:
// client scopes (метки клиентов по source IP), wssize_ramp (метки Googlevideo
// и поздние NFQUEUE-правила) и изолированные транспортные цепочки SuperSweep.
//
// Все системные команды выполняются через интерфейс Runner, поэтому логика
// полностью тестируется без iptables/nft. Те же правила доступны в виде
// шаблонов custom.d-скриптов zapret2 (см. scripts.go).
package firewall

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Метки по умолчанию (совпадают с config.default и shell-реализацией).
const (
	// DefaultDesyncMark — DESYNC_MARK.
	DefaultDesyncMark uint32 = 0x40000000
	// DefaultDesyncMarkPostnat — DESYNC_MARK_POSTNAT.
	DefaultDesyncMarkPostnat uint32 = 0x20000000
	// DefaultWssizeMark — Z2R_WSSIZE_MARK.
	DefaultWssizeMark uint32 = 0x04000000
	// SuperSweepMark — зарезервированная метка изолированных проб SuperSweep.
	SuperSweepMark uint32 = 0x08000000

	// DefaultClientScopeMask — маска меток client scope (CLIENT_SCOPE_MARK_MASK).
	DefaultClientScopeMask uint32 = 0xff00
	// DefaultClientScopeShift — сдвиг идентификатора скоупа в метке.
	DefaultClientScopeShift uint = 8
	// DefaultClientScopeMax — максимальный идентификатор скоупа.
	DefaultClientScopeMax = 255
)

// Имена меток в сообщениях о коллизиях (совпадают с именами переменных config).
const (
	NameDesyncMark     = "DESYNC_MARK"
	NameDesyncPostnat  = "DESYNC_MARK_POSTNAT"
	NameFilterMark     = "FILTER_MARK"
	NameClientScopeMsk = "CLIENT_SCOPE_MARK_MASK"
	NameWssizeMark     = "Z2R_WSSIZE_MARK"
	NameSuperSweepMark = "Z2R_SUPERSWEEP_MARK"
)

// Ошибки проверки меток.
var (
	// ErrMarkCollision — метки/маски пересекаются по битам.
	ErrMarkCollision = errors.New("firewall: mark collision")
	// ErrInvalidMark — метка не разбирается или не помещается в 32 бита.
	ErrInvalidMark = errors.New("firewall: invalid mark")
	// ErrZeroMark — обязательная метка равна нулю.
	ErrZeroMark = errors.New("firewall: zero mark")
)

// Getter читает переменную конфигурации zapret2. Сигнатура совпадает с
// (*config.Config).Get, поэтому метод можно передавать напрямую.
type Getter func(name string) (string, bool)

// MarkSet — набор fwmark-полей, которые не должны пересекаться.
// Нулевое значение поля означает «не используется».
type MarkSet struct {
	Desync          uint32 // DESYNC_MARK
	DesyncPostnat   uint32 // DESYNC_MARK_POSTNAT
	Filter          uint32 // FILTER_MARK
	ClientScopeMask uint32 // CLIENT_SCOPE_MARK_MASK
	Wssize          uint32 // Z2R_WSSIZE_MARK
	SuperSweep      uint32 // Z2R_SUPERSWEEP_MARK (по умолчанию 0x08000000)
}

// DefaultMarks возвращает набор меток по умолчанию (FILTER и маска client
// scope выключены).
func DefaultMarks() MarkSet {
	return MarkSet{
		Desync:        DefaultDesyncMark,
		DesyncPostnat: DefaultDesyncMarkPostnat,
		Wssize:        DefaultWssizeMark,
		SuperSweep:    SuperSweepMark,
	}
}

// Collision описывает пару пересекающихся полей.
type Collision struct {
	A, B       string
	AMark      uint32
	BMark      uint32
	OverlapMsk uint32
}

func (c Collision) String() string {
	return fmt.Sprintf("%s=%s overlaps %s=%s (bits %s)",
		c.A, FormatMark(c.AMark), c.B, FormatMark(c.BMark), FormatMark(c.OverlapMsk))
}

// CollisionError — ошибка со списком всех найденных коллизий.
// errors.Is(err, ErrMarkCollision) возвращает true.
type CollisionError struct {
	Collisions []Collision
}

func (e *CollisionError) Error() string {
	parts := make([]string, len(e.Collisions))
	for i, c := range e.Collisions {
		parts[i] = c.String()
	}
	return "firewall: mark collision: " + strings.Join(parts, "; ")
}

// Is позволяет сравнивать с ErrMarkCollision.
func (e *CollisionError) Is(target error) bool { return target == ErrMarkCollision }

type namedMark struct {
	name string
	mark uint32
}

func (m MarkSet) fields() []namedMark {
	return []namedMark{
		{NameDesyncMark, m.Desync},
		{NameDesyncPostnat, m.DesyncPostnat},
		{NameFilterMark, m.Filter},
		{NameClientScopeMsk, m.ClientScopeMask},
		{NameWssizeMark, m.Wssize},
		{NameSuperSweepMark, m.SuperSweep},
	}
}

func collide(a, b namedMark) (Collision, bool) {
	if a.mark == 0 || b.mark == 0 || a.mark&b.mark == 0 {
		return Collision{}, false
	}
	return Collision{A: a.name, B: b.name, AMark: a.mark, BMark: b.mark, OverlapMsk: a.mark & b.mark}, true
}

// Collisions возвращает все пары полей с пересекающимися битами.
func (m MarkSet) Collisions() []Collision {
	f := m.fields()
	var out []Collision
	for i := 0; i < len(f); i++ {
		for j := i + 1; j < len(f); j++ {
			if c, ok := collide(f[i], f[j]); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

// Check проверяет попарное отсутствие пересечений между всеми полями.
func (m MarkSet) Check() error {
	if cs := m.Collisions(); len(cs) > 0 {
		return &CollisionError{Collisions: cs}
	}
	return nil
}

// checkAgainst проверяет поле subject против перечисленных остальных.
func (m MarkSet) checkAgainst(subject namedMark, others ...namedMark) error {
	var cs []Collision
	for _, o := range others {
		if c, ok := collide(subject, o); ok {
			cs = append(cs, c)
		}
	}
	if len(cs) > 0 {
		return &CollisionError{Collisions: cs}
	}
	return nil
}

// superSweep возвращает метку SuperSweep, подставляя значение по умолчанию.
func (m MarkSet) superSweep() uint32 {
	if m.SuperSweep == 0 {
		return SuperSweepMark
	}
	return m.SuperSweep
}

// CheckWssize проверяет Z2R_WSSIZE_MARK: он не должен быть нулевым и не должен
// пересекаться с DESYNC_MARK, DESYNC_MARK_POSTNAT, FILTER_MARK, маской client
// scope и меткой SuperSweep (как z2r_wssize_mark_validate).
func (m MarkSet) CheckWssize() error {
	if m.Wssize == 0 {
		return fmt.Errorf("%w: %s", ErrZeroMark, NameWssizeMark)
	}
	return m.checkAgainst(namedMark{NameWssizeMark, m.Wssize},
		namedMark{NameDesyncMark, m.Desync},
		namedMark{NameDesyncPostnat, m.DesyncPostnat},
		namedMark{NameFilterMark, m.Filter},
		namedMark{NameClientScopeMsk, m.ClientScopeMask},
		namedMark{NameSuperSweepMark, m.superSweep()},
	)
}

// CheckSuperSweep проверяет метку SuperSweep против DESYNC_MARK, POSTNAT,
// FILTER_MARK и маски client scope; DESYNC_MARK не должен быть нулевым.
func (m MarkSet) CheckSuperSweep() error {
	if m.Desync == 0 {
		return fmt.Errorf("%w: %s", ErrZeroMark, NameDesyncMark)
	}
	return m.checkAgainst(namedMark{NameSuperSweepMark, m.superSweep()},
		namedMark{NameDesyncMark, m.Desync},
		namedMark{NameDesyncPostnat, m.DesyncPostnat},
		namedMark{NameFilterMark, m.Filter},
		namedMark{NameClientScopeMsk, m.ClientScopeMask},
	)
}

// CheckClientScope проверяет маску client scope против всех остальных меток.
func (m MarkSet) CheckClientScope() error {
	return m.checkAgainst(namedMark{NameClientScopeMsk, m.ClientScopeMask},
		namedMark{NameDesyncMark, m.Desync},
		namedMark{NameDesyncPostnat, m.DesyncPostnat},
		namedMark{NameFilterMark, m.Filter},
		namedMark{NameWssizeMark, m.Wssize},
		namedMark{NameSuperSweepMark, m.SuperSweep},
	)
}

// ParseMark разбирает метку: десятичное число или 0x-шестнадцатеричное,
// не более 32 бит. Пустая строка даёт ErrInvalidMark.
func ParseMark(s string) (uint32, error) {
	t := strings.TrimSpace(s)
	t = strings.Trim(t, `"'`)
	if t == "" {
		return 0, fmt.Errorf("%w: empty value", ErrInvalidMark)
	}
	base := 10
	digits := t
	if strings.HasPrefix(t, "0x") || strings.HasPrefix(t, "0X") {
		base, digits = 16, t[2:]
	}
	if digits == "" {
		return 0, fmt.Errorf("%w: %q", ErrInvalidMark, s)
	}
	v, err := strconv.ParseUint(digits, base, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidMark, s)
	}
	return uint32(v), nil
}

// FormatMark форматирует метку как 0x%08x.
func FormatMark(m uint32) string { return fmt.Sprintf("0x%08x", m) }

// markPair форматирует «метка/маска» для iptables (--mark, --set-xmark).
func markPair(mark, mask uint32) string { return FormatMark(mark) + "/" + FormatMark(mask) }

func getTrim(get Getter, name string) string {
	if get == nil {
		return ""
	}
	v, ok := get(name)
	if !ok {
		return ""
	}
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(v), `"'`))
}

// markVar читает метку из конфигурации; пустое/отсутствующее значение даёт def.
func markVar(get Getter, name string, def uint32) (uint32, error) {
	raw := getTrim(get, name)
	if raw == "" {
		return def, nil
	}
	v, err := ParseMark(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return v, nil
}

// MarksFromGetter читает метки из конфигурации zapret2. Отсутствующие значения
// заменяются значениями по умолчанию (FILTER_MARK и маска client scope — нулём).
func MarksFromGetter(get Getter) (MarkSet, error) {
	def := DefaultMarks()
	var (
		m   MarkSet
		err error
	)
	if m.Desync, err = markVar(get, NameDesyncMark, def.Desync); err != nil {
		return MarkSet{}, err
	}
	if m.DesyncPostnat, err = markVar(get, NameDesyncPostnat, def.DesyncPostnat); err != nil {
		return MarkSet{}, err
	}
	if m.Filter, err = markVar(get, NameFilterMark, 0); err != nil {
		return MarkSet{}, err
	}
	if m.ClientScopeMask, err = markVar(get, NameClientScopeMsk, 0); err != nil {
		return MarkSet{}, err
	}
	if m.Wssize, err = markVar(get, NameWssizeMark, def.Wssize); err != nil {
		return MarkSet{}, err
	}
	if m.SuperSweep, err = markVar(get, NameSuperSweepMark, def.SuperSweep); err != nil {
		return MarkSet{}, err
	}
	return m, nil
}

// sortedUnique возвращает отсортированные строки без дубликатов.
func sortedUnique(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

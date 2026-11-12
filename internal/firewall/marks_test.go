package firewall

import (
	"errors"
	"strings"
	"testing"
)

func TestParseMark(t *testing.T) {
	tests := []struct {
		in      string
		want    uint32
		wantErr bool
	}{
		{"0x40000000", 0x40000000, false},
		{"0X08000000", 0x08000000, false},
		{"256", 256, false},
		{" 0xff00 ", 0xff00, false},
		{`"0x1"`, 1, false},
		{"0", 0, false},
		{"", 0, true},
		{"0x", 0, true},
		{"0x100000000", 0, true},
		{"4294967296", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseMark(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseMark(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if err != nil {
			if !errors.Is(err, ErrInvalidMark) {
				t.Errorf("ParseMark(%q) error %v is not ErrInvalidMark", tt.in, err)
			}
			continue
		}
		if got != tt.want {
			t.Errorf("ParseMark(%q) = %#x, want %#x", tt.in, got, tt.want)
		}
	}
}

func TestFormatMark(t *testing.T) {
	if got := FormatMark(0x4000000); got != "0x04000000" {
		t.Errorf("FormatMark = %q", got)
	}
	if got := markPair(0x40000000, 0x40000000); got != "0x40000000/0x40000000" {
		t.Errorf("markPair = %q", got)
	}
}

func TestDefaultMarksHaveNoCollisions(t *testing.T) {
	m := DefaultMarks()
	m.ClientScopeMask = DefaultClientScopeMask
	m.Filter = 0x10000000 // закомментированный пример из config.default
	if err := m.Check(); err != nil {
		t.Fatalf("default marks collide: %v", err)
	}
	for name, fn := range map[string]func() error{
		"wssize": m.CheckWssize, "supersweep": m.CheckSuperSweep, "clientscope": m.CheckClientScope,
	} {
		if err := fn(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMarkConstants(t *testing.T) {
	if SuperSweepMark != 0x08000000 || DefaultWssizeMark != 0x04000000 ||
		DefaultDesyncMark != 0x40000000 || DefaultDesyncMarkPostnat != 0x20000000 {
		t.Fatal("mark constants differ from the reference implementation")
	}
}

func TestMarkCollisions(t *testing.T) {
	base := func() MarkSet {
		m := DefaultMarks()
		m.ClientScopeMask = DefaultClientScopeMask
		m.Filter = 0x10000000
		return m
	}
	tests := []struct {
		name   string
		mutate func(*MarkSet)
		check  func(MarkSet) error
		want   []string // имена, которые должны попасть в сообщение
	}{
		{"wssize vs desync", func(m *MarkSet) { m.Wssize = m.Desync }, MarkSet.CheckWssize, []string{NameWssizeMark, NameDesyncMark}},
		{"wssize vs postnat", func(m *MarkSet) { m.Wssize = m.DesyncPostnat }, MarkSet.CheckWssize, []string{NameDesyncPostnat}},
		{"wssize vs filter", func(m *MarkSet) { m.Wssize = m.Filter }, MarkSet.CheckWssize, []string{NameFilterMark}},
		{"wssize vs client scope mask", func(m *MarkSet) { m.Wssize = 0x100 }, MarkSet.CheckWssize, []string{NameClientScopeMsk}},
		{"wssize vs supersweep", func(m *MarkSet) { m.Wssize = SuperSweepMark }, MarkSet.CheckWssize, []string{NameSuperSweepMark}},
		{"supersweep vs desync", func(m *MarkSet) { m.Desync = SuperSweepMark | 1 }, MarkSet.CheckSuperSweep, []string{NameDesyncMark}},
		{"supersweep vs postnat", func(m *MarkSet) { m.DesyncPostnat = SuperSweepMark }, MarkSet.CheckSuperSweep, []string{NameDesyncPostnat}},
		{"supersweep vs filter", func(m *MarkSet) { m.Filter = 0x0f000000 }, MarkSet.CheckSuperSweep, []string{NameFilterMark}},
		{"supersweep vs client scope", func(m *MarkSet) { m.ClientScopeMask = 0xff000000 }, MarkSet.CheckSuperSweep, []string{NameClientScopeMsk}},
		{"client scope vs desync", func(m *MarkSet) { m.ClientScopeMask = 0x40000000 }, MarkSet.CheckClientScope, []string{NameDesyncMark}},
		{"client scope vs wssize", func(m *MarkSet) { m.ClientScopeMask = 0xffff0000 }, MarkSet.CheckClientScope, []string{NameWssizeMark}},
		{"full check", func(m *MarkSet) { m.Filter = m.Desync }, MarkSet.Check, []string{NameFilterMark, NameDesyncMark}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base()
			tt.mutate(&m)
			err := tt.check(m)
			if err == nil {
				t.Fatal("expected collision error")
			}
			if !errors.Is(err, ErrMarkCollision) {
				t.Fatalf("errors.Is(ErrMarkCollision) = false for %v", err)
			}
			var ce *CollisionError
			if !errors.As(err, &ce) || len(ce.Collisions) == 0 {
				t.Fatalf("no CollisionError: %v", err)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %s", err, w)
				}
			}
		})
	}
}

func TestCheckWssizeZero(t *testing.T) {
	m := DefaultMarks()
	m.Wssize = 0
	if err := m.CheckWssize(); !errors.Is(err, ErrZeroMark) {
		t.Fatalf("err = %v, want ErrZeroMark", err)
	}
}

func TestCheckSuperSweepZeroDesync(t *testing.T) {
	m := DefaultMarks()
	m.Desync = 0
	if err := m.CheckSuperSweep(); !errors.Is(err, ErrZeroMark) {
		t.Fatalf("err = %v, want ErrZeroMark", err)
	}
}

func TestSuperSweepDefaultsWhenZero(t *testing.T) {
	m := MarkSet{Desync: DefaultDesyncMark, Wssize: SuperSweepMark}
	if err := m.CheckWssize(); !errors.Is(err, ErrMarkCollision) {
		t.Fatalf("zero SuperSweep must fall back to 0x08000000: %v", err)
	}
}

func TestZeroMarksAreIgnoredInCollisions(t *testing.T) {
	m := MarkSet{Desync: DefaultDesyncMark, Wssize: DefaultWssizeMark}
	if cs := m.Collisions(); len(cs) != 0 {
		t.Fatalf("unexpected collisions: %v", cs)
	}
}

func mapGetter(vars map[string]string) Getter {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

func TestMarksFromGetter(t *testing.T) {
	m, err := MarksFromGetter(mapGetter(map[string]string{
		"DESYNC_MARK":            "0x40000000",
		"DESYNC_MARK_POSTNAT":    "0x20000000",
		"FILTER_MARK":            "0x10000000",
		"CLIENT_SCOPE_MARK_MASK": "65280",
		"Z2R_WSSIZE_MARK":        `"0x04000000"`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := MarkSet{Desync: 0x40000000, DesyncPostnat: 0x20000000, Filter: 0x10000000,
		ClientScopeMask: 0xff00, Wssize: 0x04000000, SuperSweep: SuperSweepMark}
	if m != want {
		t.Errorf("got %+v, want %+v", m, want)
	}

	// Пустые значения (как CLIENT_SCOPE_MARK_MASK= в config.default) → умолчания.
	m, err = MarksFromGetter(mapGetter(map[string]string{"CLIENT_SCOPE_MARK_MASK": "", "FILTER_MARK": ""}))
	if err != nil {
		t.Fatal(err)
	}
	if m.ClientScopeMask != 0 || m.Filter != 0 || m.Desync != DefaultDesyncMark || m.Wssize != DefaultWssizeMark {
		t.Errorf("defaults not applied: %+v", m)
	}

	if _, err := MarksFromGetter(mapGetter(map[string]string{"DESYNC_MARK": "zzz"})); !errors.Is(err, ErrInvalidMark) {
		t.Errorf("err = %v, want ErrInvalidMark", err)
	}
	if _, err := MarksFromGetter(nil); err != nil {
		t.Errorf("nil getter: %v", err)
	}
}

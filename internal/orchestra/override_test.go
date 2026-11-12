package orchestra

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var allKinds = []OverrideKind{OverrideBlob, OverrideSNI, OverrideMode, OverrideCloneSize}

func TestOverrideKindMetadata(t *testing.T) {
	tests := []struct {
		kind OverrideKind
		file string
		name string
	}{
		{OverrideBlob, "blob_override.tsv", "blob"},
		{OverrideSNI, "sni_override.tsv", "sni"},
		{OverrideMode, "mode_override.tsv", "mode"},
		{OverrideCloneSize, "clonesize.tsv", "clonesize"},
		{OverrideKind(0), "", "unknown"},
		{OverrideKind(99), "", "unknown"},
	}
	s := NewStore("/x/orch")
	for _, tc := range tests {
		if got := tc.kind.FileName(); got != tc.file {
			t.Errorf("FileName(%d) = %q, want %q", tc.kind, got, tc.file)
		}
		if got := tc.kind.String(); got != tc.name {
			t.Errorf("String(%d) = %q, want %q", tc.kind, got, tc.name)
		}
		wantPath := ""
		if tc.file != "" {
			wantPath = filepath.Join(filepath.Clean("/x/orch"), tc.file)
		}
		if got := s.OverridePath(tc.kind); got != wantPath {
			t.Errorf("OverridePath(%d) = %q, want %q", tc.kind, got, wantPath)
		}
	}
}

func TestValidateOverrideValue(t *testing.T) {
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 60)
	tooLong := long + "." + strings.Repeat("e", 63)
	tests := []struct {
		kind  OverrideKind
		value string
		ok    bool
	}{
		// blob: [A-Za-z0-9_]+
		{OverrideBlob, "fake_default_tls", true},
		{OverrideBlob, "z2r_prof_2", true},
		{OverrideBlob, "tls_clienthello_5.bin", false},
		{OverrideBlob, "with space", false},
		{OverrideBlob, "", false},
		{OverrideBlob, "a\tb", false},
		// sni
		{OverrideSNI, "vk.ru", true},
		{OverrideSNI, "www.google.com", true},
		{OverrideSNI, "a", true},
		{OverrideSNI, "a-b.c-d.example", true},
		{OverrideSNI, "UPPER.com", false},
		{OverrideSNI, "-bad.com", false},
		{OverrideSNI, "bad-.com", false},
		{OverrideSNI, "a..b", false},
		{OverrideSNI, "a b.com", false},
		{OverrideSNI, "a.b.c.d.e.f.g", false}, // более 5 меток
		{OverrideSNI, long, true},
		{OverrideSNI, tooLong, false},
		{OverrideSNI, "", false},
		// mode
		{OverrideMode, "clone", true},
		{OverrideMode, "classic", true},
		{OverrideMode, "Clone", false},
		{OverrideMode, "", false},
		{OverrideMode, "other", false},
		// clonesize 64..1200
		{OverrideCloneSize, "64", true},
		{OverrideCloneSize, "196", true},
		{OverrideCloneSize, "964", true},
		{OverrideCloneSize, "1200", true},
		{OverrideCloneSize, "63", false},
		{OverrideCloneSize, "1201", false},
		{OverrideCloneSize, "0", false},
		{OverrideCloneSize, "-100", false},
		{OverrideCloneSize, "abc", false},
		{OverrideCloneSize, "12.5", false},
		{OverrideCloneSize, "", false},
		{OverrideCloneSize, "99999999999999999999", false},
	}
	for _, tc := range tests {
		err := tc.kind.ValidateValue(tc.value)
		if tc.ok && err != nil {
			t.Errorf("%s ValidateValue(%q) = %v, want ok", tc.kind, tc.value, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s ValidateValue(%q) = %v, want ErrInvalid", tc.kind, tc.value, err)
		}
	}
	if err := OverrideKind(0).ValidateValue("x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown kind err = %v", err)
	}
	for _, p := range ClonePresets {
		if err := OverrideCloneSize.ValidateValue(itoa(p)); err != nil {
			t.Errorf("preset %d invalid: %v", p, err)
		}
	}
}

func itoa(n int) string { return Strategy(n).String() }

// validValue возвращает допустимые значения каждого вида переопределений.
var validValue = map[OverrideKind][2]string{
	OverrideBlob:      {"z2r_prof_1", "fake_default_tls"},
	OverrideSNI:       {"vk.ru", "www.google.com"},
	OverrideMode:      {"clone", "classic"},
	OverrideCloneSize: {"964", "512"},
}

func TestOverrideSetGetClearAllKinds(t *testing.T) {
	for _, kind := range allKinds {
		t.Run(kind.String(), func(t *testing.T) {
			s := newTestStore(t)
			v := validValue[kind]

			if got, ok, err := s.GetOverride(kind, "1"); err != nil || ok || got != "" {
				t.Fatalf("initial Get = (%q,%v,%v)", got, ok, err)
			}
			if err := s.SetOverride(kind, "1", v[0]); err != nil {
				t.Fatal(err)
			}
			if err := s.SetOverride(kind, "3", v[1]); err != nil {
				t.Fatal(err)
			}
			if got, ok, err := s.GetOverride(kind, "1"); err != nil || !ok || got != v[0] {
				t.Fatalf("Get(1) = (%q,%v,%v), want %q", got, ok, err, v[0])
			}
			if got, want := readFile(t, s.OverridePath(kind)), "1\t"+v[0]+"\n3\t"+v[1]+"\n"; got != want {
				t.Fatalf("file = %q, want %q", got, want)
			}

			// Замена на месте.
			if err := s.SetOverride(kind, "1", v[1]); err != nil {
				t.Fatal(err)
			}
			if got, want := readFile(t, s.OverridePath(kind)), "1\t"+v[1]+"\n3\t"+v[1]+"\n"; got != want {
				t.Fatalf("after replace = %q, want %q", got, want)
			}

			m, err := s.OverrideMap(kind)
			if err != nil || !reflect.DeepEqual(m, map[string]string{"1": v[1], "3": v[1]}) {
				t.Fatalf("OverrideMap = (%v,%v)", m, err)
			}
			list, err := s.ListOverrides(kind)
			if err != nil || !reflect.DeepEqual(list, []OverrideEntry{{"1", v[1]}, {"3", v[1]}}) {
				t.Fatalf("ListOverrides = (%v,%v)", list, err)
			}

			if err := s.ClearOverride(kind, "1"); err != nil {
				t.Fatal(err)
			}
			if _, ok, _ := s.GetOverride(kind, "1"); ok {
				t.Error("profile 1 still present after clear")
			}
			if got, ok, _ := s.GetOverride(kind, "3"); !ok || got != v[1] {
				t.Errorf("profile 3 damaged: (%q,%v)", got, ok)
			}
			// Повторная очистка и очистка отсутствующего — без ошибок.
			if err := s.ClearOverride(kind, "1"); err != nil {
				t.Errorf("second clear: %v", err)
			}
			if err := s.ClearOverride(kind, "8"); err != nil {
				t.Errorf("clear missing: %v", err)
			}
		})
	}
}

func TestOverrideInvalidInput(t *testing.T) {
	s := newTestStore(t)
	tests := []struct {
		name         string
		kind         OverrideKind
		profile, val string
	}{
		{"non numeric profile", OverrideBlob, "abc", "x"},
		{"empty profile", OverrideBlob, "", "x"},
		{"negative profile", OverrideMode, "-1", "clone"},
		{"tab in profile", OverrideMode, "1\t2", "clone"},
		{"bad blob", OverrideBlob, "1", "bad name"},
		{"bad sni", OverrideSNI, "1", "Bad_SNI"},
		{"bad mode", OverrideMode, "1", "weird"},
		{"size too small", OverrideCloneSize, "1", "10"},
		{"size too big", OverrideCloneSize, "1", "5000"},
		{"unknown kind", OverrideKind(42), "1", "x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.SetOverride(tc.kind, tc.profile, tc.val); !errors.Is(err, ErrInvalid) {
				t.Fatalf("SetOverride err = %v, want ErrInvalid", err)
			}
		})
	}
	if _, _, err := s.GetOverride(OverrideBlob, "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("GetOverride bad profile err = %v", err)
	}
	if err := s.ClearOverride(OverrideBlob, "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("ClearOverride bad profile err = %v", err)
	}
	if err := s.ClearOverride(OverrideKind(42), "1"); !errors.Is(err, ErrInvalid) {
		t.Errorf("ClearOverride bad kind err = %v", err)
	}
	if _, err := s.ListOverrides(OverrideKind(42)); !errors.Is(err, ErrInvalid) {
		t.Errorf("ListOverrides bad kind err = %v", err)
	}
	// Ничего не записано.
	for _, kind := range allKinds {
		if fileExists(s.OverridePath(kind)) {
			t.Errorf("%s file created by rejected writes", kind)
		}
	}
}

func TestOverrideFileHandling(t *testing.T) {
	s := newTestStore(t)
	path := s.OverridePath(OverrideSNI)
	writeFile(t, path, "# comment\r\n1\tvk.ru\r\n2\tya.ru\textra\r\nbroken line\r\n1\tdup.ru\r\n\r\n")

	if got, _, _ := s.GetOverride(OverrideSNI, "1"); got != "vk.ru" {
		t.Errorf("first row must win, got %q", got)
	}
	if got, ok, _ := s.GetOverride(OverrideSNI, "2"); !ok || got != "ya.ru" {
		t.Errorf("extra columns must be ignored, got (%q,%v)", got, ok)
	}

	// Set удаляет дубликаты, сохраняет комментарии и мусор.
	if err := s.SetOverride(OverrideSNI, "1", "new.example"); err != nil {
		t.Fatal(err)
	}
	want := "# comment\n1\tnew.example\n2\tya.ru\textra\nbroken line\n\n"
	if got := readFile(t, path); got != want {
		t.Errorf("after set:\n%q\nwant\n%q", got, want)
	}

	if err := s.ClearOverride(OverrideSNI, "2"); err != nil {
		t.Fatal(err)
	}
	want = "# comment\n1\tnew.example\nbroken line\n\n"
	if got := readFile(t, path); got != want {
		t.Errorf("after clear:\n%q\nwant\n%q", got, want)
	}
}

func TestOverridesAreIndependentFiles(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetOverride(OverrideMode, "1", "clone"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOverride(OverrideSNI, "1", "vk.ru"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetOverride(OverrideBlob, "1"); ok {
		t.Error("blob override must be independent")
	}
	if got, _, _ := s.GetOverride(OverrideMode, "1"); got != "clone" {
		t.Errorf("mode = %q", got)
	}
	// Оверрайды не влияют на статус фиксаций.
	if s.StatusText() != "Нет" {
		t.Error("overrides must not affect lock status")
	}
}

func TestGetFakeModeAndCloneSize(t *testing.T) {
	s := newTestStore(t)
	if got, err := s.GetFakeMode("1"); err != nil || got != ModeClassic {
		t.Errorf("default mode = (%q,%v), want classic", got, err)
	}
	if err := s.SetOverride(OverrideMode, "1", ModeClone); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetFakeMode("1"); got != ModeClone {
		t.Errorf("mode = %q", got)
	}
	if _, err := s.GetFakeMode("x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad profile err = %v", err)
	}

	if n, ok, err := s.GetCloneSize("4"); err != nil || ok || n != 0 {
		t.Errorf("default clone size = (%d,%v,%v)", n, ok, err)
	}
	if err := s.SetOverride(OverrideCloneSize, "4", "964"); err != nil {
		t.Fatal(err)
	}
	if n, ok, err := s.GetCloneSize("4"); err != nil || !ok || n != 964 {
		t.Errorf("clone size = (%d,%v,%v)", n, ok, err)
	}
	// Испорченное значение в файле → ErrInvalid.
	writeFile(t, s.OverridePath(OverrideCloneSize), "4\tnot-a-number\n")
	if _, _, err := s.GetCloneSize("4"); !errors.Is(err, ErrInvalid) {
		t.Errorf("corrupt value err = %v", err)
	}
}

func TestOverrideProfilesConstants(t *testing.T) {
	if !reflect.DeepEqual(OverrideProfiles, []int{1, 2, 3, 4, 8}) {
		t.Errorf("OverrideProfiles = %v", OverrideProfiles)
	}
	if !reflect.DeepEqual(ClonePresets, []int{1200, 964, 512, 196}) {
		t.Errorf("ClonePresets = %v", ClonePresets)
	}
}

package orchestra

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/AloofLibra/zator/internal/config"
)

// newTestStore создаёт хранилище во временном каталоге; верхняя граница
// стратегий — штатные максимумы профилей.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(filepath.Join(t.TempDir(), "orchestra"))
	s.MaxStrategy = config.DefaultMaxStrategy
	return s
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mustGet(t *testing.T, s *Store, scope, profile, proto string) Strategy {
	t.Helper()
	got, err := s.GetLock(scope, profile, proto)
	if err != nil {
		t.Fatalf("GetLock(%s,%s,%s): %v", scope, profile, proto, err)
	}
	return got
}

func TestSetGetClearDefault(t *testing.T) {
	s := newTestStore(t)

	if got := mustGet(t, s, "default", "3", "tls"); !got.IsAuto() {
		t.Fatalf("initial = %v, want auto", got)
	}
	if err := s.SetLock("default", "3", "tls", 7); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, s, "", "3", "tls"); got != 7 {
		t.Fatalf("after set = %v, want 7", got)
	}
	if got, want := readFile(t, s.LockedPath()), "3\ttls\t7\n"; got != want {
		t.Fatalf("locked.tsv = %q, want %q", got, want)
	}

	// Перезапись на месте.
	if err := s.SetLock("default", "3", "tls", 12); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, s.LockedPath()), "3\ttls\t12\n"; got != want {
		t.Fatalf("locked.tsv = %q, want %q", got, want)
	}

	// Ноль = профиль выключен, а не auto.
	if err := s.SetLock("default", "3", "tls", StrategyOff); err != nil {
		t.Fatal(err)
	}
	got := mustGet(t, s, "default", "3", "tls")
	if got.IsAuto() || !got.IsOff() {
		t.Fatalf("zero lock read back as %v", got)
	}

	if err := s.ClearLock("default", "3", "tls"); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, s, "default", "3", "tls"); !got.IsAuto() {
		t.Fatalf("after clear = %v, want auto", got)
	}
	if readFile(t, s.LockedPath()) != "" {
		t.Errorf("locked.tsv not empty after clear: %q", readFile(t, s.LockedPath()))
	}
	// Повторная очистка — не ошибка.
	if err := s.ClearLock("default", "3", "tls"); err != nil {
		t.Fatalf("second clear: %v", err)
	}
	// Очистка в пустом хранилище не создаёт файлов.
	empty := newTestStore(t)
	if err := empty.ClearLock("default", "1", "tls"); err != nil {
		t.Fatal(err)
	}
	if fileExists(empty.LockedPath()) {
		t.Error("ClearLock created a file")
	}
}

func TestSetLockAutoClears(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetLock("default", "1", "tls", 5); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLock("default", "1", "tls", StrategyAuto); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, s, "default", "1", "tls"); !got.IsAuto() {
		t.Fatalf("got %v", got)
	}
}

func TestSetLockValidation(t *testing.T) {
	s := newTestStore(t)
	tests := []struct {
		name                  string
		scope, profile, proto string
		strategy              Strategy
		wantErr               bool
	}{
		{"ok tls profile 3", "default", "3", "tls", 43, false},
		{"ok http profile 1", "default", "1", "http", 1, false},
		{"ok udp profile 5", "default", "5", "udp", 32, false},
		{"ok profile 4 max", "default", "4", "tls", 28, false},
		{"ok fallback 9 http", "default", "9", "http", 8, false},
		{"ok mark scope", "mark:2", "3", "tls", 1, false},
		{"ok custom domain", "default", "example.com", "tls", 5, false},
		{"custom domain range = profile 3", "default", "example.com", "tls", 44, false},
		{"custom domain over range", "default", "example.com", "tls", 45, true},
		{"custom domain http", "default", "example.com", "http", 1, true},
		{"over max profile 4", "default", "4", "tls", 29, true},
		{"over max profile 9", "default", "9", "http", 9, true},
		{"proto not valid for profile", "default", "2", "http", 1, true},
		{"udp profile with tls", "default", "5", "tls", 1, true},
		{"unknown profile 11", "default", "11", "tls", 1, true},
		{"profile 0", "default", "0", "tls", 1, true},
		{"empty profile", "default", "", "tls", 1, true},
		{"junk profile", "default", "abc", "tls", 1, true},
		{"tab in profile", "default", "3\t4", "tls", 1, true},
		{"bad proto", "default", "3", "quic", 1, true},
		{"bad scope", "other", "3", "tls", 1, true},
		{"mark zero", "mark:0", "3", "tls", 1, true},
		{"negative strategy (not auto)", "default", "3", "tls", Strategy(-5), false}, // < 0 == auto == clear
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := s.SetLock(tc.scope, tc.profile, tc.proto, tc.strategy)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("err = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNoRangeCheckWithoutMaxStrategy(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.SetLock("default", "4", "tls", 999); err != nil {
		t.Fatalf("nil MaxStrategy must not limit: %v", err)
	}
	s.MaxStrategy = func(int) int { return 0 }
	if err := s.SetLock("default", "4", "tls", 1000); err != nil {
		t.Fatalf("MaxStrategy 0 must not limit: %v", err)
	}
}

func TestInvalidWriteLeavesNoFiles(t *testing.T) {
	s := newTestStore(t)
	_ = s.SetLock("default", "4", "tls", 999)
	if fileExists(s.LockedPath()) || fileExists(s.lockDir()) {
		t.Error("rejected SetLock left files behind")
	}
}

func TestGetLockReadsAllFormats(t *testing.T) {
	s := newTestStore(t)
	writeFile(t, s.LockedPath(), strings.Join([]string{
		"# comment",
		"",
		"1\t5",               // 2 колонки: tls
		"1\thttp\t2",         // 3 колонки
		"default\t5\tudp\t9", // 4 колонки, default
		"mark:3\t2\ttls\t11", // legacy mark
		"garbage line",
		"3\ttls\tnot-a-number",
	}, "\n")+"\n")

	tests := []struct {
		scope, profile, proto string
		want                  Strategy
	}{
		{"default", "1", "tls", 5},
		{"default", "1", "http", 2},
		{"default", "5", "udp", 9},
		{"default", "2", "tls", StrategyAuto}, // mark:3 не виден в default
		{"mark:3", "2", "tls", 11},            // legacy-строка видна в mark:3
		{"mark:4", "2", "tls", StrategyAuto},
		{"default", "3", "tls", StrategyAuto}, // битая строка игнорируется
	}
	for _, tc := range tests {
		got := mustGet(t, s, tc.scope, tc.profile, tc.proto)
		if got != tc.want {
			t.Errorf("GetLock(%s,%s,%s) = %v, want %v", tc.scope, tc.profile, tc.proto, got, tc.want)
		}
	}
}

func TestSetLockPreservesOtherRows(t *testing.T) {
	s := newTestStore(t)
	original := "# keep\n1\ttls\t5\ngarbage\n3\t9\n"
	writeFile(t, s.LockedPath(), original)

	// 2-колоночная строка профиля 3 (tls) заменяется 3-колоночной на месте.
	if err := s.SetLock("default", "3", "tls", 10); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, s.LockedPath()), "# keep\n1\ttls\t5\ngarbage\n3\ttls\t10\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// Новый ключ добавляется в конец.
	if err := s.SetLock("default", "4", "tls", 2); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, s.LockedPath()), "# keep\n1\ttls\t5\ngarbage\n3\ttls\t10\n4\ttls\t2\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// Очистка не трогает чужие строки.
	if err := s.ClearLock("default", "1", "tls"); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, s.LockedPath()), "# keep\ngarbage\n3\ttls\t10\n4\ttls\t2\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDuplicateRows(t *testing.T) {
	s := newTestStore(t)
	writeFile(t, s.LockedPath(), "3\ttls\t5\n3\ttls\t7\n1\ttls\t2\n1\ttls\t2\n")

	if _, err := s.GetLock("default", "3", "tls"); !errors.Is(err, ErrConflict) {
		t.Errorf("GetLock with differing dupes err = %v, want ErrConflict", err)
	}
	// Одинаковые дубликаты читаются без ошибки.
	if got := mustGet(t, s, "default", "1", "tls"); got != 2 {
		t.Errorf("identical duplicates = %v, want 2", got)
	}
	// SetLock отказывается писать поверх дубликатов (как rc=3 в shell).
	if err := s.SetLock("default", "3", "tls", 9); !errors.Is(err, ErrConflict) {
		t.Errorf("SetLock over dupes err = %v, want ErrConflict", err)
	}
	if got := readFile(t, s.LockedPath()); got != "3\ttls\t5\n3\ttls\t7\n1\ttls\t2\n1\ttls\t2\n" {
		t.Errorf("file modified despite conflict: %q", got)
	}
	src, err := s.LockSource("default", "3", "tls")
	if err != nil || src != SourceConflict {
		t.Errorf("LockSource = (%q,%v), want conflict", src, err)
	}
	// ClearLock удаляет все дубликаты — путь восстановления.
	if err := s.ClearLock("default", "3", "tls"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, s.LockedPath()); got != "1\ttls\t2\n1\ttls\t2\n" {
		t.Errorf("after clear: %q", got)
	}
	if err := s.SetLock("default", "3", "tls", 9); err != nil {
		t.Errorf("SetLock after clear: %v", err)
	}
}

func TestManualFileForFallbackProfiles(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetLock("default", "8", "tls", 20); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLock("default", "9", "http", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLock("default", "3", "tls", 4); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, s.ManualPath()), "8\ttls\t20\n9\thttp\t3\n"; got != want {
		t.Errorf("locked.manual.tsv = %q, want %q", got, want)
	}
	if got, want := readFile(t, s.LockedPath()), "3\ttls\t4\n"; got != want {
		t.Errorf("locked.tsv = %q, want %q", got, want)
	}
	if got := mustGet(t, s, "default", "8", "tls"); got != 20 {
		t.Errorf("GetLock 8 = %v", got)
	}

	// Устаревшая строка профиля 8 в locked.tsv читается и вычищается при записи.
	writeFile(t, s.LockedPath(), "8\ttls\t1\n3\ttls\t4\n")
	if err := s.SetLock("default", "8", "tls", 21); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, s.LockedPath()), "3\ttls\t4\n"; got != want {
		t.Errorf("stale row not removed: %q", got)
	}
	if got := mustGet(t, s, "default", "8", "tls"); got != 21 {
		t.Errorf("GetLock 8 after = %v", got)
	}

	// ClearLock для 8 чистит manual.
	if err := s.ClearLock("default", "8", "tls"); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, s.ManualPath()), "9\thttp\t3\n"; got != want {
		t.Errorf("manual after clear = %q, want %q", got, want)
	}
}

func TestScopedLocks(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetLock("mark:2", "3", "tls", 6); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLock("mark:2", "1", "http", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLock("mark:10", "8", "tls", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLock("default", "3", "tls", 9); err != nil {
		t.Fatal(err)
	}

	p2, _ := s.ScopePath("mark:2")
	if p2 != filepath.Join(s.Dir(), "scopes", "mark_2.tsv") {
		t.Errorf("ScopePath = %q", p2)
	}
	if got, want := readFile(t, p2), "3\ttls\t6\n1\thttp\t3\n"; got != want {
		t.Errorf("mark_2.tsv = %q, want %q", got, want)
	}
	// Фиксация 8/9 клиентского scope тоже лежит в per-mark файле.
	p10, _ := s.ScopePath("mark:10")
	if got, want := readFile(t, p10), "8\ttls\t1\n"; got != want {
		t.Errorf("mark_10.tsv = %q, want %q", got, want)
	}
	if fileExists(s.ManualPath()) {
		t.Error("scoped fallback lock leaked to locked.manual.tsv")
	}
	if got, want := readFile(t, s.LockedPath()), "3\ttls\t9\n"; got != want {
		t.Errorf("locked.tsv = %q, want %q", got, want)
	}

	// Значения изолированы по scope.
	if got := mustGet(t, s, "mark:2", "3", "tls"); got != 6 {
		t.Errorf("mark:2 = %v", got)
	}
	if got := mustGet(t, s, "default", "3", "tls"); got != 9 {
		t.Errorf("default = %v", got)
	}
	if got := mustGet(t, s, "mark:3", "3", "tls"); !got.IsAuto() {
		t.Errorf("mark:3 = %v, want auto", got)
	}

	// Очистка последней строки удаляет файл клиента.
	if err := s.ClearLock("mark:10", "8", "tls"); err != nil {
		t.Fatal(err)
	}
	if fileExists(p10) {
		t.Error("empty mark file must be removed")
	}
	if err := s.ClearLock("mark:2", "3", "tls"); err != nil {
		t.Fatal(err)
	}
	if !fileExists(p2) {
		t.Error("mark_2.tsv must remain (has another row)")
	}
}

func TestLockSource(t *testing.T) {
	s := newTestStore(t)
	check := func(scope, want string) {
		t.Helper()
		got, err := s.LockSource(scope, "3", "tls")
		if err != nil || got != want {
			t.Errorf("LockSource(%s) = (%q,%v), want %q", scope, got, err, want)
		}
	}
	check("default", SourceAuto)
	check("mark:2", SourceAuto)

	if err := s.SetLock("default", "3", "tls", 4); err != nil {
		t.Fatal(err)
	}
	check("default", SourceDefault)
	check("mark:2", SourceDefault)

	if err := s.SetLock("mark:2", "3", "tls", 5); err != nil {
		t.Fatal(err)
	}
	check("mark:2", SourceScoped)
	check("default", SourceDefault)
	check("mark:7", SourceDefault)

	if _, err := s.LockSource("bad", "3", "tls"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad scope err = %v", err)
	}
}

func TestMigrateLegacyScopes(t *testing.T) {
	s := newTestStore(t)
	writeFile(t, s.LockedPath(), strings.Join([]string{
		"1\ttls\t5",
		"mark:2\t3\ttls\t7",
		"mark:2\t1\thttp\t2",
		"mark:5\t2\ttls\t4",
		"default\t4\ttls\t1",
		"mark:0\t3\ttls\t1", // некорректный mark — остаётся на месте
	}, "\n")+"\n")
	// Строка per-mark файла приоритетнее legacy.
	writeFile(t, filepath.Join(s.ScopesDir(), "mark_2.tsv"), "3\ttls\t99\n")

	// Чтение до миграции видит legacy-строки.
	if got := mustGet(t, s, "mark:5", "2", "tls"); got != 4 {
		t.Errorf("pre-migration mark:5 = %v", got)
	}

	if err := s.MigrateLegacyScopes(); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, s.LockedPath()), "1\ttls\t5\ndefault\t4\ttls\t1\nmark:0\t3\ttls\t1\n"; got != want {
		t.Errorf("locked.tsv = %q, want %q", got, want)
	}
	if got, want := readFile(t, filepath.Join(s.ScopesDir(), "mark_2.tsv")), "3\ttls\t99\n1\thttp\t2\n"; got != want {
		t.Errorf("mark_2.tsv = %q, want %q", got, want)
	}
	if got, want := readFile(t, filepath.Join(s.ScopesDir(), "mark_5.tsv")), "2\ttls\t4\n"; got != want {
		t.Errorf("mark_5.tsv = %q, want %q", got, want)
	}
	if got := mustGet(t, s, "mark:2", "3", "tls"); got != 99 {
		t.Errorf("mark:2 = %v, want 99", got)
	}

	// Идемпотентность.
	before := readFile(t, s.LockedPath())
	if err := s.MigrateLegacyScopes(); err != nil {
		t.Fatal(err)
	}
	if readFile(t, s.LockedPath()) != before {
		t.Error("second migration changed locked.tsv")
	}
}

func TestSetLockMigratesLegacyFirst(t *testing.T) {
	s := newTestStore(t)
	writeFile(t, s.LockedPath(), "mark:2\t3\ttls\t7\nmark:2\t1\thttp\t2\n")
	if err := s.SetLock("mark:2", "3", "tls", 8); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, s.LockedPath()); got != "" {
		t.Errorf("legacy rows left in locked.tsv: %q", got)
	}
	if got, want := readFile(t, filepath.Join(s.ScopesDir(), "mark_2.tsv")), "3\ttls\t8\n1\thttp\t2\n"; got != want {
		t.Errorf("mark_2.tsv = %q, want %q", got, want)
	}
}

func TestListLocksAndScopes(t *testing.T) {
	s := newTestStore(t)
	writeFile(t, s.LockedPath(), "1\t5\n1\thttp\t2\nmark:9\t3\ttls\t1\n")
	writeFile(t, s.ManualPath(), "8\ttls\t4\n")
	writeFile(t, filepath.Join(s.ScopesDir(), "mark_2.tsv"), "3\ttls\t6\n")
	writeFile(t, filepath.Join(s.ScopesDir(), "mark_10.tsv"), "5\tudp\t1\n")
	writeFile(t, filepath.Join(s.ScopesDir(), "notes.txt"), "ignored")
	writeFile(t, filepath.Join(s.ScopesDir(), "mark_0.tsv"), "ignored")

	got, err := s.ListLocks("default")
	if err != nil {
		t.Fatal(err)
	}
	want := []Record{
		{ScopeDefault, "1", ProtoTLS, 5},
		{ScopeDefault, "1", ProtoHTTP, 2},
		{ScopeDefault, "8", ProtoTLS, 4},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListLocks(default) = %v, want %v", got, want)
	}

	got, err = s.ListLocks("mark:2")
	if err != nil || !reflect.DeepEqual(got, []Record{{"mark:2", "3", ProtoTLS, 6}}) {
		t.Errorf("ListLocks(mark:2) = (%v,%v)", got, err)
	}
	// Legacy-строка mark:9 видна в своём scope.
	got, _ = s.ListLocks("mark:9")
	if !reflect.DeepEqual(got, []Record{{"mark:9", "3", ProtoTLS, 1}}) {
		t.Errorf("ListLocks(mark:9) = %v", got)
	}
	if got, _ := s.ListLocks("mark:77"); len(got) != 0 {
		t.Errorf("ListLocks(mark:77) = %v, want empty", got)
	}
	if _, err := s.ListLocks("nope"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad scope err = %v", err)
	}

	scopes, err := s.ListScopes()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"default", "mark:2", "mark:9", "mark:10"}; !reflect.DeepEqual(scopes, want) {
		t.Errorf("ListScopes = %v, want %v", scopes, want)
	}

	all, err := s.ListAllLocks()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3+1+1+1 {
		t.Errorf("ListAllLocks len = %d (%v)", len(all), all)
	}
	if all[0].Scope != ScopeDefault || all[len(all)-1].Scope != "mark:10" {
		t.Errorf("ListAllLocks order: %v", all)
	}
}

func TestListEmptyStore(t *testing.T) {
	s := newTestStore(t)
	if recs, err := s.ListLocks("default"); err != nil || len(recs) != 0 {
		t.Errorf("ListLocks = (%v,%v)", recs, err)
	}
	scopes, err := s.ListScopes()
	if err != nil || !reflect.DeepEqual(scopes, []string{"default"}) {
		t.Errorf("ListScopes = (%v,%v)", scopes, err)
	}
}

func TestClearEverywhere(t *testing.T) {
	s := newTestStore(t)
	for _, sc := range []string{"default", "mark:2", "mark:3"} {
		if err := s.SetLock(sc, "3", "tls", 5); err != nil {
			t.Fatal(err)
		}
		if err := s.SetLock(sc, "1", "tls", 6); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ClearEverywhere("3", "tls"); err != nil {
		t.Fatal(err)
	}
	for _, sc := range []string{"default", "mark:2", "mark:3"} {
		if got := mustGet(t, s, sc, "3", "tls"); !got.IsAuto() {
			t.Errorf("%s/3 = %v after ClearEverywhere", sc, got)
		}
		if got := mustGet(t, s, sc, "1", "tls"); got != 6 {
			t.Errorf("%s/1 = %v, want untouched 6", sc, got)
		}
	}
	if err := s.ClearEverywhere("99", "tls"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad profile err = %v", err)
	}
}

func TestStatusText(t *testing.T) {
	s := newTestStore(t)
	if got := s.StatusText(); got != "Нет" {
		t.Errorf("empty store: %q", got)
	}
	if s.HasLocks() {
		t.Error("HasLocks on empty store")
	}
	writeFile(t, s.LockedPath(), "")
	if got := s.StatusText(); got != "Нет" {
		t.Errorf("empty locked.tsv: %q", got)
	}
	if err := s.SetLock("default", "3", "tls", 1); err != nil {
		t.Fatal(err)
	}
	if got := s.StatusText(); got != "Есть" {
		t.Errorf("with lock: %q", got)
	}
	if err := s.ClearLock("default", "3", "tls"); err != nil {
		t.Fatal(err)
	}
	if got := s.StatusText(); got != "Нет" {
		t.Errorf("after clear: %q", got)
	}
	// Только manual.
	if err := s.SetLock("default", "9", "http", 1); err != nil {
		t.Fatal(err)
	}
	if got := s.StatusText(); got != "Есть" {
		t.Errorf("manual only: %q", got)
	}
	// per-mark файлы тоже считаются (HasLocks смотрит scopes/).
	only := newTestStore(t)
	if err := only.SetLock("mark:2", "3", "tls", 1); err != nil {
		t.Fatal(err)
	}
	if got := only.StatusText(); got != "Есть" {
		t.Errorf("scoped only: %q", got)
	}
}

func TestCaseInsensitiveKeys(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetLock("DEFAULT", "Example.COM", "TLS", 3); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, s.LockedPath()), "example.com\ttls\t3\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := mustGet(t, s, "default", "EXAMPLE.com", "tls"); got != 3 {
		t.Errorf("got %v", got)
	}
}

func TestCRLFFilesAreHandled(t *testing.T) {
	s := newTestStore(t)
	writeFile(t, s.LockedPath(), "3\ttls\t5\r\n1\ttls\t2\r\n")
	if got := mustGet(t, s, "default", "3", "tls"); got != 5 {
		t.Errorf("got %v", got)
	}
	if err := s.SetLock("default", "1", "tls", 9); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, s.LockedPath()), "3\ttls\t5\n1\ttls\t9\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestConcurrentWriters(t *testing.T) {
	s := newTestStore(t)
	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)

	// Разные хранилища на один каталог — как несколько независимых писателей.
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := NewStore(s.Dir())
			errs <- w.SetLock("default", fmt.Sprintf("%d", (i%7)+1), "tls", Strategy(i))
		}(i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := NewStore(s.Dir())
			errs <- w.SetLock(MarkScope(i+1), "3", "tls", Strategy(i))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		// Профили 5..7 допускают только udp, поэтому ErrInvalid для tls ожидаем.
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Fatalf("concurrent SetLock: %v", err)
		}
	}
	for i := 1; i <= n; i++ {
		if got := mustGet(t, s, MarkScope(i+1), "3", "tls"); got != Strategy(i) {
			t.Errorf("mark:%d = %v, want %d", i+1, got, i)
		}
	}
	if fileExists(s.lockDir()) {
		t.Error("lock directory left behind")
	}
}

func TestConcurrentSameKeyLastWriterConsistent(t *testing.T) {
	s := newTestStore(t)
	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := NewStore(s.Dir()).SetLock("default", "3", "tls", Strategy(i)); err != nil {
				t.Errorf("SetLock: %v", err)
			}
		}(i)
	}
	wg.Wait()
	got, err := s.GetLock("default", "3", "tls")
	if err != nil {
		t.Fatalf("GetLock: %v (duplicate rows?)", err)
	}
	if got < 1 || got > 20 {
		t.Errorf("got %v", got)
	}
	if n := strings.Count(readFile(t, s.LockedPath()), "\n"); n != 1 {
		t.Errorf("expected exactly one row, file has %d", n)
	}
}

func TestNoTempFilesLeft(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5; i++ {
		if err := s.SetLock("default", "3", "tls", Strategy(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") || strings.HasSuffix(e.Name(), ".write-lock") {
			t.Errorf("leftover: %s", e.Name())
		}
	}
}

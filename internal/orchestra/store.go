package orchestra

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/AloofLibra/zator/internal/config"
)

// Имена файлов состояния orchestra.
const (
	LockedFileName       = "locked.tsv"
	LockedManualFileName = "locked.manual.tsv"
	ScopesDirName        = "scopes"
)

// Текст статуса «есть ли фиксации» (strategy_locks_status).
const (
	StatusPresent = "Есть"
	StatusAbsent  = "Нет"
)

// Источник эффективной фиксации (LockSource).
const (
	SourceScoped   = "scoped"
	SourceDefault  = "default"
	SourceAuto     = "auto"
	SourceConflict = "conflict"
)

var scopeFileRe = regexp.MustCompile(`^mark_([1-9][0-9]*)\.tsv$`)

// Store — файловое хранилище состояния orchestra в каталоге Dir.
//
// Чтение не блокируется (файлы заменяются атомарно); все записи сериализуются
// файловой блокировкой Dir/locked.tsv.write-lock и выполняются через
// tmp-файл + rename.
type Store struct {
	dir string

	// MaxStrategy возвращает максимальный номер стратегии профиля (для
	// кастомных доменов вызывается с профилем 3). nil или значение <= 0 —
	// без проверки верхней границы. Обычно: cfg.ProfileMaxStrategyOrDefault.
	MaxStrategy func(profile int) int
	// LockTimeout — сколько ждать блокировку записи (по умолчанию DefaultLockTimeout).
	LockTimeout time.Duration
	// StaleLockAge — возраст пустого каталога блокировки, после которого он
	// считается брошенным (по умолчанию DefaultStaleLockAge).
	StaleLockAge time.Duration
}

// NewStore создаёт хранилище в каталоге dir (создаётся при первой записи).
func NewStore(dir string) *Store { return &Store{dir: filepath.Clean(dir)} }

// Dir возвращает каталог хранилища.
func (s *Store) Dir() string { return s.dir }

// LockedPath — locked.tsv (default scope, профили 1-7 и 10).
func (s *Store) LockedPath() string { return filepath.Join(s.dir, LockedFileName) }

// ManualPath — locked.manual.tsv (default scope, профили 8/9).
func (s *Store) ManualPath() string { return filepath.Join(s.dir, LockedManualFileName) }

// ScopesDir — каталог per-mark файлов.
func (s *Store) ScopesDir() string { return filepath.Join(s.dir, ScopesDirName) }

// ScopePath возвращает путь per-mark файла для scope "mark:N".
func (s *Store) ScopePath(scope string) (string, error) {
	n, ok := MarkOf(scope)
	if !ok {
		return "", fmt.Errorf("%w: scope %q has no per-mark file", ErrInvalid, scope)
	}
	return filepath.Join(s.ScopesDir(), "mark_"+strconv.Itoa(n)+".tsv"), nil
}

// StatusText возвращает "Есть", если есть любая фиксация (default/manual/scopes),
// иначе "Нет" (strategy_locks_status_text).
func (s *Store) StatusText() string {
	if s.HasLocks() {
		return StatusPresent
	}
	return StatusAbsent
}

// HasLocks сообщает, что есть непустой locked.tsv, locked.manual.tsv
// или хотя бы один per-mark файл в scopes/.
func (s *Store) HasLocks() bool {
	for _, p := range []string{s.LockedPath(), s.ManualPath()} {
		if st, err := os.Stat(p); err == nil && st.Size() > 0 {
			return true
		}
	}
	entries, err := os.ReadDir(s.ScopesDir())
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if scopeFileRe.MatchString(e.Name()) {
			if info, err := e.Info(); err == nil && info.Size() > 0 {
				return true
			}
		}
	}
	return false
}

// --- проверка ключей ---

// key — нормализованный ключ фиксации.
type key struct {
	scope   string
	profile string
	proto   Proto
}

func (s *Store) key(scope, profile, proto string) (key, error) {
	sc, err := normScope(scope)
	if err != nil {
		return key{}, err
	}
	pr, err := normProfile(profile)
	if err != nil {
		return key{}, err
	}
	po, err := ParseProto(proto)
	if err != nil {
		return key{}, err
	}
	switch {
	case IsCustomDomain(pr):
		if po != ProtoTLS {
			return key{}, fmt.Errorf("%w: protocol %s is not valid for custom domain %s", ErrInvalid, po, pr)
		}
	case digitsRe.MatchString(pr):
		id, _ := strconv.Atoi(pr)
		if !config.ValidProfile(id) {
			return key{}, fmt.Errorf("%w: lock profile %q", ErrInvalid, profile)
		}
		if !config.ProfileHasProto(id, string(po)) {
			return key{}, fmt.Errorf("%w: protocol %s is not valid for profile %d", ErrInvalid, po, id)
		}
	default:
		return key{}, fmt.Errorf("%w: lock profile %q", ErrInvalid, profile)
	}
	return key{scope: sc, profile: pr, proto: po}, nil
}

func (s *Store) checkRange(k key, st Strategy) error {
	if st <= 0 || s.MaxStrategy == nil {
		return nil
	}
	id := 3 // подбор кастомных доменов идёт в диапазоне профиля 3
	if !IsCustomDomain(k.profile) {
		id, _ = strconv.Atoi(k.profile)
	}
	if limit := s.MaxStrategy(id); limit > 0 && int(st) > limit {
		return fmt.Errorf("%w: strategy %d is outside profile %s range (max %d)", ErrInvalid, st, k.profile, limit)
	}
	return nil
}

// --- источники строк ---

// sources возвращает файл, в который пишутся фиксации ключа, и все файлы,
// которые на него влияют (первый — основной).
func (s *Store) sources(scope, profile string) (primary string, all []string) {
	if n, ok := MarkOf(scope); ok {
		p := filepath.Join(s.ScopesDir(), "mark_"+strconv.Itoa(n)+".tsv")
		return p, []string{p, s.LockedPath()}
	}
	if profile == "8" || profile == "9" {
		return s.ManualPath(), []string{s.ManualPath(), s.LockedPath()}
	}
	return s.LockedPath(), []string{s.LockedPath()}
}

// read читает файл с учётом того, что per-mark файлы лежат в ScopesDir.
func (s *Store) read(path string) (*tsvFile, error) {
	if filepath.Dir(path) == s.ScopesDir() {
		m := scopeFileRe.FindStringSubmatch(filepath.Base(path))
		if m == nil {
			return nil, fmt.Errorf("%w: unexpected scope file %s", ErrInvalid, path)
		}
		return readLockFile(path, "mark:"+m[1])
	}
	return readLockFile(path, "")
}

// --- чтение ---

// GetLock возвращает фиксацию ключа; при отсутствии — StrategyAuto.
// ErrConflict, если в файлах несколько строк с разными стратегиями.
func (s *Store) GetLock(scope, profile, proto string) (Strategy, error) {
	k, err := s.key(scope, profile, proto)
	if err != nil {
		return 0, err
	}
	_, all := s.sources(k.scope, k.profile)
	found := Strategy(StrategyAuto)
	have := false
	for _, path := range all {
		f, err := s.read(path)
		if err != nil {
			return 0, err
		}
		for _, i := range f.matches(k.scope, k.profile, k.proto) {
			st := f.rows[i].rec.Strategy
			if have && st != found {
				return 0, fmt.Errorf("%w: %s/%s/%s", ErrConflict, k.scope, k.profile, k.proto)
			}
			found, have = st, true
		}
	}
	return found, nil
}

// ListLocks возвращает все фиксации scope в порядке следования в файлах
// (default: locked.tsv, затем locked.manual.tsv). Точные дубликаты схлопываются.
func (s *Store) ListLocks(scope string) ([]Record, error) {
	sc, err := normScope(scope)
	if err != nil {
		return nil, err
	}
	var paths []string
	if _, ok := MarkOf(sc); ok {
		p, _ := s.ScopePath(sc)
		paths = []string{p, s.LockedPath()} // второй — legacy 4-колоночные строки
	} else {
		paths = []string{s.LockedPath(), s.ManualPath()}
	}
	var out []Record
	seen := map[Record]bool{}
	for _, path := range paths {
		f, err := s.read(path)
		if err != nil {
			return nil, err
		}
		for _, r := range f.rows {
			if r.ok && r.rec.Scope == sc && !seen[r.rec] {
				seen[r.rec] = true
				out = append(out, r.rec)
			}
		}
	}
	return out, nil
}

// ListScopes возвращает "default" и все scope mark:N, у которых есть
// per-mark файл или legacy-строки в locked.tsv, по возрастанию N.
func (s *Store) ListScopes() ([]string, error) {
	marks := map[int]bool{}

	f, err := s.read(s.LockedPath())
	if err != nil {
		return nil, err
	}
	for _, r := range f.rows {
		if r.ok {
			if n, ok := MarkOf(r.rec.Scope); ok {
				marks[n] = true
			}
		}
	}
	entries, err := os.ReadDir(s.ScopesDir())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if m := scopeFileRe.FindStringSubmatch(e.Name()); m != nil {
			n, _ := strconv.Atoi(m[1])
			marks[n] = true
		}
	}
	nums := make([]int, 0, len(marks))
	for n := range marks {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	out := []string{ScopeDefault}
	for _, n := range nums {
		out = append(out, MarkScope(n))
	}
	return out, nil
}

// ListAllLocks возвращает фиксации всех scope (default первым).
func (s *Store) ListAllLocks() ([]Record, error) {
	scopes, err := s.ListScopes()
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, sc := range scopes {
		recs, err := s.ListLocks(sc)
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	return out, nil
}

// LockSource поясняет, откуда берётся эффективная фиксация: "scoped" (строка
// самого scope), "default" (наследуется от default), "auto" (нет фиксации) или
// "conflict" (дубликаты строк).
func (s *Store) LockSource(scope, profile, proto string) (string, error) {
	k, err := s.key(scope, profile, proto)
	if err != nil {
		return "", err
	}
	if k.scope != ScopeDefault {
		n, err := s.count(k.scope, k.profile, k.proto)
		if err != nil {
			return "", err
		}
		switch {
		case n > 1:
			return SourceConflict, nil
		case n == 1:
			return SourceScoped, nil
		}
	}
	n, err := s.count(ScopeDefault, k.profile, k.proto)
	if err != nil {
		return "", err
	}
	switch {
	case n > 1:
		return SourceConflict, nil
	case n == 1:
		return SourceDefault, nil
	}
	return SourceAuto, nil
}

func (s *Store) count(scope, profile string, proto Proto) (int, error) {
	_, all := s.sources(scope, profile)
	total := 0
	for _, path := range all {
		f, err := s.read(path)
		if err != nil {
			return 0, err
		}
		total += len(f.matches(scope, profile, proto))
	}
	return total, nil
}

// --- запись ---

// SetLock фиксирует стратегию. StrategyAuto равнозначно ClearLock; 0 выключает
// профиль. Профили 8/9 default-scope пишутся в locked.manual.tsv, mark:N — в
// scopes/mark_N.tsv. Строка заменяется на месте, остальное содержимое файла
// сохраняется. ErrConflict, если для ключа уже несколько строк.
func (s *Store) SetLock(scope, profile, proto string, strategy Strategy) error {
	k, err := s.key(scope, profile, proto)
	if err != nil {
		return err
	}
	if strategy.IsAuto() {
		return s.clearKey(k)
	}
	if err := s.checkRange(k, strategy); err != nil {
		return err
	}
	return s.withLock(func() error {
		if _, ok := MarkOf(k.scope); ok {
			if err := s.migrateLocked(); err != nil {
				return err
			}
		}
		primary, all := s.sources(k.scope, k.profile)
		pf, err := s.read(primary)
		if err != nil {
			return err
		}
		idx := pf.matches(k.scope, k.profile, k.proto)
		if len(idx) > 1 {
			return fmt.Errorf("%w: %s/%s/%s", ErrConflict, k.scope, k.profile, k.proto)
		}
		pf.upsert(idx, k.scope, k.profile, k.proto, strategy)
		if err := writeLines(primary, pf.lines()); err != nil {
			return err
		}
		// Устаревшие строки того же ключа в других файлах только создавали бы конфликт.
		for _, path := range all[1:] {
			of, err := s.read(path)
			if err != nil {
				return err
			}
			if m := of.matches(k.scope, k.profile, k.proto); len(m) > 0 {
				of.remove(m)
				if err := writeLines(path, of.lines()); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// ClearLock снимает фиксацию (возврат к "auto"): удаляет все строки ключа.
// Пустой per-mark файл удаляется.
func (s *Store) ClearLock(scope, profile, proto string) error {
	k, err := s.key(scope, profile, proto)
	if err != nil {
		return err
	}
	return s.clearKey(k)
}

func (s *Store) clearKey(k key) error {
	return s.withLock(func() error { return s.clearKeyLocked(k) })
}

func (s *Store) clearKeyLocked(k key) error {
	_, all := s.sources(k.scope, k.profile)
	for _, path := range all {
		f, err := s.read(path)
		if err != nil {
			return err
		}
		m := f.matches(k.scope, k.profile, k.proto)
		if len(m) == 0 {
			continue
		}
		f.remove(m)
		if len(f.rows) == 0 && filepath.Dir(path) == s.ScopesDir() {
			// «Клиент целиком» = нет файла.
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		if err := writeLines(path, f.lines()); err != nil {
			return err
		}
	}
	return nil
}

// ClearEverywhere снимает фиксацию (profile, proto) во всех scope.
func (s *Store) ClearEverywhere(profile, proto string) error {
	if _, err := s.key(ScopeDefault, profile, proto); err != nil {
		return err
	}
	return s.withLock(func() error {
		scopes, err := s.ListScopes()
		if err != nil {
			return err
		}
		for _, sc := range scopes {
			k, err := s.key(sc, profile, proto)
			if err != nil {
				return err
			}
			if err := s.clearKeyLocked(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// MigrateLegacyScopes переносит 4-колоночные строки `mark:N\tprofile\tproto\tN`
// из locked.tsv в scopes/mark_N.tsv. Идемпотентна; существующая строка
// per-mark файла приоритетнее legacy.
func (s *Store) MigrateLegacyScopes() error {
	return s.withLock(s.migrateLocked)
}

// migrateLocked выполняет миграцию; блокировка записи уже должна быть взята.
func (s *Store) migrateLocked() error {
	lf, err := s.read(s.LockedPath())
	if err != nil {
		return err
	}
	var moved []int
	var order []string
	byMark := map[string][]Record{}
	for i, r := range lf.rows {
		if !r.ok || r.cols < 4 {
			continue
		}
		if _, ok := MarkOf(r.rec.Scope); !ok {
			continue
		}
		moved = append(moved, i)
		if _, seen := byMark[r.rec.Scope]; !seen {
			order = append(order, r.rec.Scope)
		}
		byMark[r.rec.Scope] = append(byMark[r.rec.Scope], r.rec)
	}
	if len(moved) == 0 {
		return nil
	}
	for _, sc := range order {
		path, _ := s.ScopePath(sc)
		mf, err := s.read(path)
		if err != nil {
			return err
		}
		for _, rec := range byMark[sc] {
			if len(mf.matches(sc, rec.Profile, rec.Proto)) > 0 {
				continue
			}
			mf.upsert(nil, sc, rec.Profile, rec.Proto, rec.Strategy)
		}
		if err := writeLines(path, mf.lines()); err != nil {
			return err
		}
	}
	lf.remove(moved)
	return writeLines(s.LockedPath(), lf.lines())
}

package customstrats

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/customblobs"
	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/orchestra"
)

const tsvHeader = "# zator custom strategies: id, block, spec (без strategy=), enabled, comment, assigned\n"

// StrategyEntry описывает строку custom_strategies.tsv.
type StrategyEntry struct {
	ID       int    `json:"id"`
	Block    string `json:"block"`
	Spec     string `json:"spec"`
	Enabled  bool   `json:"enabled"`
	Comment  string `json:"comment"`
	Assigned int    `json:"assigned"`
}

// Store управляет custom_strategies.tsv и синхронизацией локов при инъекте.
type Store struct {
	tsvPath         string
	customBlobsTSV  string
	orch            *orchestra.Store
	autoLockedPaths []string
}

// NewStore создаёт хранилище пользовательских стратегий.
func NewStore(tsvPath, customBlobsTSV string, orch *orchestra.Store, autoLockedPaths ...string) *Store {
	return &Store{
		tsvPath:         tsvPath,
		customBlobsTSV:  customBlobsTSV,
		orch:            orch,
		autoLockedPaths: append([]string(nil), autoLockedPaths...),
	}
}

// TSVPath возвращает путь к custom_strategies.tsv.
func (s *Store) TSVPath() string { return s.tsvPath }

// List возвращает записи из TSV (опционально фильтруя по block).
func (s *Store) List(filterBlock string) ([]StrategyEntry, error) {
	entries, err := s.readUnlocked()
	if err != nil || filterBlock == "" {
		return entries, err
	}
	filterBlock = strings.ToLower(strings.TrimSpace(filterBlock))
	out := make([]StrategyEntry, 0, len(entries))
	for _, e := range entries {
		if e.Block == filterBlock {
			out = append(out, e)
		}
	}
	return out, nil
}

// Get возвращает запись по ID.
func (s *Store) Get(id int) (StrategyEntry, bool, error) {
	entries, err := s.readUnlocked()
	if err != nil {
		return StrategyEntry{}, false, err
	}
	for _, e := range entries {
		if e.ID == id {
			return e, true, nil
		}
	}
	return StrategyEntry{}, false, nil
}

// Add нормализует, валидирует и добавляет новую стратегию (без автоматической подмены неизвестных блобов).
func (s *Store) Add(block, rawSpec, comment string, cfg *config.Config) (StrategyEntry, error) {
	block = strings.ToLower(strings.TrimSpace(block))
	spec, err := NormalizeSpec(rawSpec)
	if err != nil {
		return StrategyEntry{}, err
	}
	known := KnownBlobNames(cfg, s.customBlobsTSV)
	if err := Validate(block, spec, known); err != nil {
		return StrategyEntry{}, err
	}

	var added StrategyEntry
	err = fsutil.WithDirLock(s.tsvPath, 0, 0, func() error {
		entries, err := s.readUnlocked()
		if err != nil {
			return err
		}
		if len(entries) >= MaxEntries {
			return ErrLimitReached
		}
		added = StrategyEntry{
			ID:      nextStratID(entries),
			Block:   block,
			Spec:    spec,
			Enabled: true,
			Comment: customblobs.SanitizeComment(comment),
		}
		entries = append(entries, added)
		return s.writeUnlocked(entries)
	})
	return added, err
}

// Edit заменяет параметры существующей стратегии и сбрасывает assigned в 0 (нужен Apply).
func (s *Store) Edit(id int, block, rawSpec string, enabled bool, comment string, cfg *config.Config) (StrategyEntry, error) {
	block = strings.ToLower(strings.TrimSpace(block))
	spec, err := NormalizeSpec(rawSpec)
	if err != nil {
		return StrategyEntry{}, err
	}
	known := KnownBlobNames(cfg, s.customBlobsTSV)
	if err := Validate(block, spec, known); err != nil {
		return StrategyEntry{}, err
	}

	var updated StrategyEntry
	err = fsutil.WithDirLock(s.tsvPath, 0, 0, func() error {
		entries, err := s.readUnlocked()
		if err != nil {
			return err
		}
		idx := -1
		for i, e := range entries {
			if e.ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: запись %d", ErrNotFound, id)
		}
		updated = StrategyEntry{
			ID:       id,
			Block:    block,
			Spec:     spec,
			Enabled:  enabled,
			Comment:  customblobs.SanitizeComment(comment),
			Assigned: 0,
		}
		entries[idx] = updated
		return s.writeUnlocked(entries)
	})
	return updated, err
}

// SetEnabled переключает флаг enabled у записи с указанным ID.
func (s *Store) SetEnabled(id int, enabled bool) error {
	return fsutil.WithDirLock(s.tsvPath, 0, 0, func() error {
		entries, err := s.readUnlocked()
		if err != nil {
			return err
		}
		idx := -1
		for i, e := range entries {
			if e.ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: запись %d", ErrNotFound, id)
		}
		entries[idx].Enabled = enabled
		return s.writeUnlocked(entries)
	})
}

// Delete удаляет запись по ID и уплотняет идентификаторы оставшихся записей (1..N).
func (s *Store) Delete(id int) error {
	return fsutil.WithDirLock(s.tsvPath, 0, 0, func() error {
		entries, err := s.readUnlocked()
		if err != nil {
			return err
		}
		next := make([]StrategyEntry, 0, len(entries))
		found := false
		for _, e := range entries {
			if e.ID == id {
				found = true
				continue
			}
			e.ID = len(next) + 1
			next = append(next, e)
		}
		if !found {
			return fmt.Errorf("%w: запись %d", ErrNotFound, id)
		}
		return s.writeUnlocked(next)
	})
}

// CountBlobRefs возвращает число пользовательских стратегий, ссылающихся на блоб name (токен-точно).
func (s *Store) CountBlobRefs(name string) (int, error) {
	if name == "" {
		return 0, nil
	}
	entries, err := s.readUnlocked()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if specReferencesBlob(e.Spec, name) {
			count++
		}
	}
	return count, nil
}

// RenameBlob атомарно заменяет ссылки на блоб oldName -> newName во всех записях TSV.
func (s *Store) RenameBlob(oldName, newName string) (int, error) {
	if oldName == "" || newName == "" || oldName == newName {
		return 0, nil
	}
	changed := 0
	err := fsutil.WithDirLock(s.tsvPath, 0, 0, func() error {
		entries, err := s.readUnlocked()
		if err != nil {
			return err
		}
		for i := range entries {
			nextSpec, modified := renameBlobInSpec(entries[i].Spec, oldName, newName)
			if modified {
				entries[i].Spec = nextSpec
				changed++
			}
		}
		if changed == 0 {
			return nil
		}
		return s.writeUnlocked(entries)
	})
	return changed, err
}

func specReferencesBlob(spec, name string) bool {
	for _, g := range strings.Split(spec, " --lua-desync=") {
		for _, tok := range strings.Split(g, ":") {
			key, val, ok := strings.Cut(tok, "=")
			if ok && blobRefKeys[key] && val == name {
				return true
			}
		}
	}
	return false
}

func renameBlobInSpec(spec, oldName, newName string) (string, bool) {
	groups := strings.Split(spec, " --lua-desync=")
	modified := false
	for gi, g := range groups {
		toks := strings.Split(g, ":")
		for ti, tok := range toks {
			key, val, ok := strings.Cut(tok, "=")
			if ok && blobRefKeys[key] && val == oldName {
				toks[ti] = key + "=" + newName
				modified = true
			}
		}
		groups[gi] = strings.Join(toks, ":")
	}
	if !modified {
		return spec, false
	}
	return strings.Join(groups, " --lua-desync="), true
}

func nextStratID(entries []StrategyEntry) int {
	maxID := 0
	for _, e := range entries {
		if e.ID > maxID {
			maxID = e.ID
		}
	}
	return maxID + 1
}

func (s *Store) readUnlocked() ([]StrategyEntry, error) {
	raw, err := os.ReadFile(s.tsvPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	var out []StrategyEntry
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		fields := strings.Split(ln, "\t")
		if len(fields) < 5 {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil || id <= 0 {
			continue
		}
		enabled := strings.TrimSpace(fields[3]) != "0"
		assigned := 0
		if len(fields) >= 6 {
			if v, aErr := strconv.Atoi(strings.TrimSpace(fields[5])); aErr == nil && v > 0 {
				assigned = v
			}
		}
		out = append(out, StrategyEntry{
			ID:       id,
			Block:    strings.ToLower(strings.TrimSpace(fields[1])),
			Spec:     strings.TrimSpace(fields[2]),
			Enabled:  enabled,
			Comment:  strings.TrimRight(fields[4], "\r"),
			Assigned: assigned,
		})
	}
	return out, nil
}

func (s *Store) writeUnlocked(entries []StrategyEntry) error {
	var b strings.Builder
	b.WriteString(tsvHeader)
	for _, e := range entries {
		en := "1"
		if !e.Enabled {
			en = "0"
		}
		asg := ""
		if e.Assigned > 0 {
			asg = strconv.Itoa(e.Assigned)
		}
		fmt.Fprintf(&b, "%d\t%s\t%s\t%s\t%s\t%s\n", e.ID, e.Block, e.Spec, en, customblobs.SanitizeComment(e.Comment), asg)
	}
	return fsutil.AtomicWriteFile(s.tsvPath, []byte(b.String()), fsutil.DefaultFileMode)
}

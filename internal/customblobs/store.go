package customblobs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/fsutil"
)

const tsvHeader = "# zator custom blobs: id, name, file, comment\n"

// BlobEntry описывает строку пользовательского блоба в custom_blobs.tsv.
type BlobEntry struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	File    string `json:"file"`
	Comment string `json:"comment"`
}

// RefTracker связывает custom_blobs с хранилищем пользовательских стратегий.
type RefTracker interface {
	CountBlobRefs(name string) (int, error)
	RenameBlob(oldName, newName string) (int, error)
}

// Store управляет файлами в custom_blob/ и декларациями в custom_blobs.tsv.
type Store struct {
	tsvPath string
	blobDir string
}

// NewStore создаёт менеджер пользовательских блобов.
func NewStore(tsvPath, blobDir string) *Store {
	return &Store{tsvPath: tsvPath, blobDir: blobDir}
}

// TSVPath возвращает путь к custom_blobs.tsv.
func (s *Store) TSVPath() string { return s.tsvPath }

// BlobDir возвращает каталог custom_blob/.
func (s *Store) BlobDir() string { return s.blobDir }

// List возвращает все декларации из custom_blobs.tsv.
func (s *Store) List() ([]BlobEntry, error) {
	return s.readUnlocked()
}

// Get возвращает декларацию по ID.
func (s *Store) Get(id int) (BlobEntry, bool, error) {
	entries, err := s.readUnlocked()
	if err != nil {
		return BlobEntry{}, false, err
	}
	for _, e := range entries {
		if e.ID == id {
			return e, true, nil
		}
	}
	return BlobEntry{}, false, nil
}

// FilesList возвращает отсортированный список валидных файлов в каталоге custom_blob/.
func (s *Store) FilesList() ([]string, error) {
	ents, err := os.ReadDir(s.blobDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if ValidateBlobFileName(e.Name()) == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// AllKnownNames возвращает отсортированный список всех известных имён блобов
// (штатных/объявленных в конфиге, встроенных и пользовательских из TSV).
func (s *Store) AllKnownNames(cfg *config.Config) ([]string, error) {
	entries, err := s.readUnlocked()
	if err != nil {
		return nil, err
	}
	return sortedKeys(collectTakenNames(cfg, entries, true)), nil
}

// Upload сохраняет бинарный файл блоба (1 B .. 256 KiB) и при отсутствии привязки/коллизии
// автоматически создаёт декларацию в custom_blobs.tsv.
func (s *Store) Upload(fileName string, data []byte, cfg *config.Config) (*BlobEntry, error) {
	if err := ValidateBlobFileName(fileName); err != nil {
		return nil, err
	}
	if len(data) < MinBlobSize || len(data) > MaxBlobSize {
		return nil, fmt.Errorf("%w: Размер файла 1 Б…256 КиБ.", ErrTooLarge)
	}

	var created *BlobEntry
	err := fsutil.WithDirLock(s.tsvPath, 0, 0, func() error {
		dst := filepath.Join(s.blobDir, fileName)
		if err := fsutil.AtomicWriteFile(dst, data, fsutil.DefaultFileMode); err != nil {
			return err
		}
		entries, err := s.readUnlocked()
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.File == fileName {
				return nil
			}
		}
		auto := DefaultBlobNameFromFile(fileName)
		if ValidateBlobName(auto) != nil {
			return nil
		}
		if collectTakenNames(cfg, entries, false)[auto] {
			return nil
		}
		next := BlobEntry{
			ID:      nextEntryID(entries),
			Name:    auto,
			File:    fileName,
			Comment: "загружен из панели",
		}
		entries = append(entries, next)
		if err := s.writeUnlocked(entries); err != nil {
			return err
		}
		created = &next
		return nil
	})
	return created, err
}

// Add добавляет новую декларацию `--blob=name:@custom_blob/file`.
func (s *Store) Add(name, file, comment string, cfg *config.Config) (BlobEntry, error) {
	name = strings.TrimSpace(name)
	file = strings.TrimSpace(file)
	if err := ValidateBlobName(name); err != nil {
		return BlobEntry{}, err
	}
	if err := ValidateBlobFileName(file); err != nil {
		return BlobEntry{}, err
	}
	if !s.blobFileExists(file) {
		return BlobEntry{}, fmt.Errorf("%w: Файла %s нет в %s.", ErrNotFound, file, s.blobDir)
	}

	var added BlobEntry
	err := fsutil.WithDirLock(s.tsvPath, 0, 0, func() error {
		entries, err := s.readUnlocked()
		if err != nil {
			return err
		}
		if collectTakenNames(cfg, entries, false)[name] {
			return fmt.Errorf("%w: Имя «%s» уже занято (штатный или другой пользовательский блоб).", ErrNameTaken, name)
		}
		added = BlobEntry{
			ID:      nextEntryID(entries),
			Name:    name,
			File:    file,
			Comment: SanitizeComment(comment),
		}
		entries = append(entries, added)
		return s.writeUnlocked(entries)
	})
	return added, err
}

// Edit обновляет существующую декларацию; при переименовании каскадно обновляет ссылки
// в пользовательских стратегиях с откатом при ошибке записи TSV.
func (s *Store) Edit(id int, name, file, comment string, cfg *config.Config, tracker RefTracker) (oldName string, renamed bool, err error) {
	name = strings.TrimSpace(name)
	file = strings.TrimSpace(file)
	if err := ValidateBlobFileName(file); err != nil {
		return "", false, err
	}
	if !s.blobFileExists(file) {
		return "", false, fmt.Errorf("%w: Файла %s нет в %s.", ErrNotFound, file, s.blobDir)
	}

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
		oldName = entries[idx].Name
		if name != oldName {
			if err := ValidateBlobName(name); err != nil {
				return err
			}
			if collectTakenNames(cfg, entries, false)[name] {
				return fmt.Errorf("%w: Имя «%s» уже занято (штатный или другой пользовательский блоб).", ErrNameTaken, name)
			}
		}
		entries[idx].Name = name
		entries[idx].File = file
		entries[idx].Comment = SanitizeComment(comment)

		if name != oldName && tracker != nil {
			if _, err := tracker.RenameBlob(oldName, name); err != nil {
				return fmt.Errorf("обновление ссылок в стратегиях: %w", err)
			}
			renamed = true
		}
		if err := s.writeUnlocked(entries); err != nil {
			if renamed && tracker != nil {
				_, _ = tracker.RenameBlob(name, oldName)
				renamed = false
			}
			return fmt.Errorf("запись TSV блобов: %w", err)
		}
		return nil
	})
	return oldName, renamed, err
}

// Delete удаляет декларацию (если на имя нет ссылок в пользовательских стратегиях) и удаляет
// физический файл из custom_blob/, если на него больше не ссылаются другие декларации.
func (s *Store) Delete(id int, tracker RefTracker) error {
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
		target := entries[idx]
		if tracker != nil {
			refs, err := tracker.CountBlobRefs(target.Name)
			if err != nil {
				return err
			}
			if refs > 0 {
				return fmt.Errorf("%w: Блоб «%s» используется в пользовательских стратегиях (%d шт.) — сначала уберите ссылки.", ErrBlobInUse, target.Name, refs)
			}
		}
		next := make([]BlobEntry, 0, len(entries)-1)
		stillUsed := false
		for i, e := range entries {
			if i == idx {
				continue
			}
			if e.File == target.File {
				stillUsed = true
			}
			next = append(next, e)
		}
		if err := s.writeUnlocked(next); err != nil {
			return err
		}
		if target.File != "" && !stillUsed {
			_ = os.Remove(filepath.Join(s.blobDir, target.File))
		}
		return nil
	})
}

func (s *Store) blobFileExists(file string) bool {
	st, err := os.Stat(filepath.Join(s.blobDir, file))
	return err == nil && !st.IsDir()
}

func nextEntryID(entries []BlobEntry) int {
	maxID := 0
	for _, e := range entries {
		if e.ID > maxID {
			maxID = e.ID
		}
	}
	return maxID + 1
}

func (s *Store) readUnlocked() ([]BlobEntry, error) {
	raw, err := os.ReadFile(s.tsvPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	var out []BlobEntry
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		fields := strings.SplitN(ln, "\t", 4)
		if len(fields) < 3 {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil || id <= 0 {
			continue
		}
		var comment string
		if len(fields) == 4 {
			comment = strings.TrimRight(fields[3], "\r")
		}
		out = append(out, BlobEntry{
			ID:      id,
			Name:    strings.TrimSpace(fields[1]),
			File:    strings.TrimSpace(fields[2]),
			Comment: comment,
		})
	}
	return out, nil
}

func (s *Store) writeUnlocked(entries []BlobEntry) error {
	var b strings.Builder
	b.WriteString(tsvHeader)
	for _, e := range entries {
		fmt.Fprintf(&b, "%d\t%s\t%s\t%s\n", e.ID, e.Name, e.File, SanitizeComment(e.Comment))
	}
	return fsutil.AtomicWriteFile(s.tsvPath, []byte(b.String()), fsutil.DefaultFileMode)
}

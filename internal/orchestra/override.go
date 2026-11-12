package orchestra

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// OverrideKind — вид per-profile переопределения (TSV profile\tvalue).
type OverrideKind int

const (
	// OverrideBlob — blob_override.tsv: имя TLS-блоба профиля.
	OverrideBlob OverrideKind = iota + 1
	// OverrideSNI — sni_override.tsv: SNI клон-стратегий профиля.
	OverrideSNI
	// OverrideMode — mode_override.tsv: режим фейков clone|classic.
	OverrideMode
	// OverrideCloneSize — clonesize.tsv: лимит размера клон-пакета, байты.
	OverrideCloneSize
)

// Допустимые значения режима фейков и границы размера клона.
const (
	ModeClone   = "clone"
	ModeClassic = "classic"

	CloneSizeMin = 64
	CloneSizeMax = 1200

	// MaxSNILength — максимальная длина SNI.
	MaxSNILength = 254
)

// Имена файлов переопределений.
const (
	BlobOverrideFileName = "blob_override.tsv"
	SNIOverrideFileName  = "sni_override.tsv"
	ModeOverrideFileName = "mode_override.tsv"
	CloneSizeFileName    = "clonesize.tsv"
)

// OverrideProfiles — профили, для которых CLI/WebUI предлагают переопределения
// (Z2R_BLOB_PROFILES и аналоги). Хранилище не ограничивает номер профиля.
var OverrideProfiles = []int{1, 2, 3, 4, 8}

// ClonePresets — пресеты лимита клонов в меню (Z2R_CLONE_SIZE_PRESETS).
var ClonePresets = []int{1200, 964, 512, 196}

var (
	blobNameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	sniRe      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?){0,4}$`)
)

func (k OverrideKind) String() string {
	switch k {
	case OverrideBlob:
		return "blob"
	case OverrideSNI:
		return "sni"
	case OverrideMode:
		return "mode"
	case OverrideCloneSize:
		return "clonesize"
	}
	return "unknown"
}

// FileName возвращает имя TSV-файла вида переопределения.
func (k OverrideKind) FileName() string {
	switch k {
	case OverrideBlob:
		return BlobOverrideFileName
	case OverrideSNI:
		return SNIOverrideFileName
	case OverrideMode:
		return ModeOverrideFileName
	case OverrideCloneSize:
		return CloneSizeFileName
	}
	return ""
}

// ValidateValue проверяет значение переопределения (blob_override_set,
// sni_override_valid, mode_override_valid, clone_size_valid).
func (k OverrideKind) ValidateValue(v string) error {
	ok := false
	switch k {
	case OverrideBlob:
		ok = blobNameRe.MatchString(v)
	case OverrideSNI:
		ok = len(v) <= MaxSNILength && sniRe.MatchString(v)
	case OverrideMode:
		ok = v == ModeClone || v == ModeClassic
	case OverrideCloneSize:
		if digitsRe.MatchString(v) {
			n, err := strconv.Atoi(v)
			ok = err == nil && n >= CloneSizeMin && n <= CloneSizeMax
		}
	default:
		return fmt.Errorf("%w: override kind %d", ErrInvalid, int(k))
	}
	if !ok {
		return fmt.Errorf("%w: %s override value %q", ErrInvalid, k, v)
	}
	return nil
}

// OverridePath возвращает путь TSV-файла переопределения.
func (s *Store) OverridePath(kind OverrideKind) string {
	if name := kind.FileName(); name != "" {
		return filepath.Join(s.dir, name)
	}
	return ""
}

// OverrideEntry — строка переопределения.
type OverrideEntry struct {
	Profile string
	Value   string
}

func checkOverrideProfile(profile string) error {
	if !digitsRe.MatchString(profile) {
		return fmt.Errorf("%w: override profile %q", ErrInvalid, profile)
	}
	return nil
}

// parseOverrideLine разбирает `profile\tvalue` (лишние колонки игнорируются, как в awk $1/$2).
func parseOverrideLine(line string) (OverrideEntry, bool) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
		return OverrideEntry{}, false
	}
	f := strings.Split(line, "\t")
	if len(f) < 2 {
		return OverrideEntry{}, false
	}
	e := OverrideEntry{Profile: strings.TrimSpace(f[0]), Value: strings.TrimSpace(f[1])}
	if e.Profile == "" || e.Value == "" {
		return OverrideEntry{}, false
	}
	return e, true
}

// ListOverrides возвращает все строки файла переопределения в порядке файла.
func (s *Store) ListOverrides(kind OverrideKind) ([]OverrideEntry, error) {
	path := s.OverridePath(kind)
	if path == "" {
		return nil, fmt.Errorf("%w: override kind %d", ErrInvalid, int(kind))
	}
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	var out []OverrideEntry
	for _, ln := range lines {
		if e, ok := parseOverrideLine(ln); ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// GetOverride возвращает значение профиля (первая строка файла); ok=false,
// если строки нет (= глобальное значение / classic / без лимита).
func (s *Store) GetOverride(kind OverrideKind, profile string) (value string, ok bool, err error) {
	profile = strings.TrimSpace(profile)
	if err := checkOverrideProfile(profile); err != nil {
		return "", false, err
	}
	entries, err := s.ListOverrides(kind)
	if err != nil {
		return "", false, err
	}
	for _, e := range entries {
		if e.Profile == profile {
			return e.Value, true, nil
		}
	}
	return "", false, nil
}

// OverrideMap возвращает карту profile -> value (при дубликатах — первая строка).
func (s *Store) OverrideMap(kind OverrideKind) (map[string]string, error) {
	entries, err := s.ListOverrides(kind)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		if _, dup := m[e.Profile]; !dup {
			m[e.Profile] = e.Value
		}
	}
	return m, nil
}

// SetOverride записывает значение профиля: первая строка профиля заменяется,
// дубликаты удаляются, при отсутствии строка добавляется в конец.
// Остальное содержимое файла сохраняется.
func (s *Store) SetOverride(kind OverrideKind, profile, value string) error {
	profile, value = strings.TrimSpace(profile), strings.TrimSpace(value)
	if err := checkOverrideProfile(profile); err != nil {
		return err
	}
	if err := kind.ValidateValue(value); err != nil {
		return err
	}
	path := s.OverridePath(kind)
	return s.withLock(func() error {
		lines, err := readLines(path)
		if err != nil {
			return err
		}
		out := make([]string, 0, len(lines)+1)
		replaced := false
		for _, ln := range lines {
			if e, ok := parseOverrideLine(ln); ok && e.Profile == profile {
				if !replaced {
					out = append(out, profile+"\t"+value)
					replaced = true
				}
				continue
			}
			out = append(out, ln)
		}
		if !replaced {
			out = append(out, profile+"\t"+value)
		}
		return writeLines(path, out)
	})
}

// ClearOverride удаляет все строки профиля; отсутствие файла/строки — не ошибка.
func (s *Store) ClearOverride(kind OverrideKind, profile string) error {
	profile = strings.TrimSpace(profile)
	if err := checkOverrideProfile(profile); err != nil {
		return err
	}
	path := s.OverridePath(kind)
	if path == "" {
		return fmt.Errorf("%w: override kind %d", ErrInvalid, int(kind))
	}
	return s.withLock(func() error {
		lines, err := readLines(path)
		if err != nil {
			return err
		}
		out := make([]string, 0, len(lines))
		removed := false
		for _, ln := range lines {
			if e, ok := parseOverrideLine(ln); ok && e.Profile == profile {
				removed = true
				continue
			}
			out = append(out, ln)
		}
		if !removed {
			return nil
		}
		return writeLines(path, out)
	})
}

// GetCloneSize возвращает лимит клона профиля в байтах; ok=false — лимита нет.
func (s *Store) GetCloneSize(profile string) (size int, ok bool, err error) {
	v, ok, err := s.GetOverride(OverrideCloneSize, profile)
	if err != nil || !ok {
		return 0, false, err
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false, fmt.Errorf("%w: clonesize value %q", ErrInvalid, v)
	}
	return n, true, nil
}

// GetFakeMode возвращает режим фейков профиля: "clone" или "classic"
// (classic — когда строки нет).
func (s *Store) GetFakeMode(profile string) (string, error) {
	v, ok, err := s.GetOverride(OverrideMode, profile)
	if err != nil {
		return "", err
	}
	if !ok {
		return ModeClassic, nil
	}
	return v, nil
}

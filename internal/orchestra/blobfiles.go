package orchestra

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// BlobFilesFileName — TSV-файл персиста выбранных файлов блобов (maxru, z2r_prof_N).
const BlobFilesFileName = "blob_files.tsv"

var (
	blobSlotKeyRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	blobFileValRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

// BlobFilesPath возвращает путь к blob_files.tsv.
func (s *Store) BlobFilesPath() string {
	return filepath.Join(s.dir, BlobFilesFileName)
}

// BlobFilesMap возвращает карту `slot -> filename` (первая строка слота).
func (s *Store) BlobFilesMap() (map[string]string, error) {
	lines, err := readLines(s.BlobFilesPath())
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for _, ln := range lines {
		e, ok := parseOverrideLine(ln)
		if !ok {
			continue
		}
		if _, dup := out[e.Profile]; !dup {
			out[e.Profile] = e.Value
		}
	}
	return out, nil
}

// GetBlobFile возвращает сохранённый файл для слота (`maxru` или `z2r_prof_N`).
func (s *Store) GetBlobFile(slot string) (string, bool, error) {
	slot = strings.TrimSpace(slot)
	if !blobSlotKeyRe.MatchString(slot) {
		return "", false, fmt.Errorf("%w: blob slot %q", ErrInvalid, slot)
	}
	m, err := s.BlobFilesMap()
	if err != nil {
		return "", false, err
	}
	v, ok := m[slot]
	return v, ok, nil
}

// SetBlobFile атомарно сохраняет выбор файла для слота `maxru` или `z2r_prof_N`.
func (s *Store) SetBlobFile(slot, file string) error {
	slot, file = strings.TrimSpace(slot), strings.TrimSpace(file)
	if !blobSlotKeyRe.MatchString(slot) {
		return fmt.Errorf("%w: blob slot %q", ErrInvalid, slot)
	}
	if !blobFileValRe.MatchString(file) || strings.Contains(file, "..") {
		return fmt.Errorf("%w: blob file %q", ErrInvalid, file)
	}
	path := s.BlobFilesPath()
	return s.withLock(func() error {
		lines, err := readLines(path)
		if err != nil {
			return err
		}
		out := make([]string, 0, len(lines)+1)
		replaced := false
		for _, ln := range lines {
			if e, ok := parseOverrideLine(ln); ok && e.Profile == slot {
				if !replaced {
					out = append(out, slot+"\t"+file)
					replaced = true
				}
				continue
			}
			out = append(out, ln)
		}
		if !replaced {
			out = append(out, slot+"\t"+file)
		}
		return writeLines(path, out)
	})
}

// ClearBlobFile удаляет строку слота из blob_files.tsv.
func (s *Store) ClearBlobFile(slot string) error {
	slot = strings.TrimSpace(slot)
	if !blobSlotKeyRe.MatchString(slot) {
		return fmt.Errorf("%w: blob slot %q", ErrInvalid, slot)
	}
	path := s.BlobFilesPath()
	return s.withLock(func() error {
		lines, err := readLines(path)
		if err != nil {
			return err
		}
		out := make([]string, 0, len(lines))
		removed := false
		for _, ln := range lines {
			if e, ok := parseOverrideLine(ln); ok && e.Profile == slot {
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

// MigrateLegacyBlobOverrides переводит старые записи `blob_override.tsv`, где
// вместо имени слота `z2r_prof_N` было записано имя файла без `.bin` (версия 10a70bc),
// в канонический формат `z2r_prof_N` с переносом имени файла в `blob_files.tsv`.
func (s *Store) MigrateLegacyBlobOverrides(slotFile func(slot string) string, fileExists func(file string) bool) (int, error) {
	entries, err := s.ListOverrides(OverrideBlob)
	if err != nil {
		return 0, err
	}
	type migration struct {
		profile string
		slot    string
		file    string
	}
	var todo []migration
	for _, e := range entries {
		if e.Value == "fake_default_tls" || strings.HasPrefix(e.Value, BlobSlotPrefix) {
			continue
		}
		if !IsBlobOverrideProfile(e.Profile) {
			continue
		}
		cand := e.Value + ".bin"
		if fileExists != nil && !fileExists(cand) {
			continue
		}
		slot := BlobSlotName(e.Profile)
		if slotFile != nil && slotFile(slot) == "" {
			continue
		}
		todo = append(todo, migration{profile: e.Profile, slot: slot, file: cand})
	}
	if len(todo) == 0 {
		return 0, nil
	}
	for _, m := range todo {
		if err := s.SetBlobFile(m.slot, m.file); err != nil {
			return 0, err
		}
		if err := s.SetOverride(OverrideBlob, m.profile, m.slot); err != nil {
			return 0, err
		}
	}
	return len(todo), nil
}

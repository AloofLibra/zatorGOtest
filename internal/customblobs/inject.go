package customblobs

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/fsutil"
)

func (s *Store) expectedLines(entries []BlobEntry) []string {
	var lines []string
	for _, e := range entries {
		if ValidateBlobName(e.Name) != nil || ValidateBlobFileName(e.File) != nil {
			continue
		}
		path := filepath.ToSlash(filepath.Join(s.blobDir, e.File))
		lines = append(lines, fmt.Sprintf("--blob=%s:@%s", e.Name, path))
	}
	return lines
}

// Apply идемпотентно записывает декларации `--blob=NAME:@custom_blob/file` в секцию
// `#Z2R_CUSTOM_BLOBS_BEGIN/END` (при необходимости досоздавая маркеры).
func (s *Store) Apply(cfg *config.Config) (bool, error) {
	if cfg == nil {
		return false, fmt.Errorf("%w: config is nil", ErrInvalid)
	}
	entries, err := s.List()
	if err != nil {
		return false, err
	}
	if err := cfg.EnsureCustomBlobMarkers(); err != nil {
		return false, err
	}
	before := cfg.String()
	if err := cfg.ReplaceMarkerSection(config.CustomBlobsBegin, config.CustomBlobsEnd, s.expectedLines(entries)); err != nil {
		return false, err
	}
	return cfg.String() != before, nil
}

// ApplyFile загружает файл конфига под блокировкой каталога, применяет блобы и сохраняет при изменениях.
func (s *Store) ApplyFile(cfgPath string) (bool, error) {
	var changed bool
	err := fsutil.WithDirLock(cfgPath, 0, 0, func() error {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		changed, err = s.Apply(cfg)
		if err != nil || !changed {
			return err
		}
		if err := fsutil.AtomicWriteFile(cfgPath, []byte(cfg.String()), 0); err != nil {
			return fmt.Errorf("config: save %s: %w", cfgPath, err)
		}
		return nil
	})
	return changed, err
}

// IsDirty сообщает, отличается ли секция #Z2R_CUSTOM_BLOBS_BEGIN/END в конфиге от custom_blobs.tsv.
func (s *Store) IsDirty(cfg *config.Config) (bool, error) {
	entries, err := s.List()
	if err != nil {
		return false, err
	}
	expected := s.expectedLines(entries)
	if cfg == nil {
		return len(expected) > 0, nil
	}
	sec, ok := cfg.ReadMarkerSection(config.CustomBlobsBegin, config.CustomBlobsEnd)
	if !ok {
		return len(expected) > 0, nil
	}
	var actual []string
	for _, ln := range sec {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "--blob=") {
			actual = append(actual, t)
		}
	}
	if len(actual) != len(expected) {
		return true, nil
	}
	for i := range expected {
		if actual[i] != expected[i] {
			return true, nil
		}
	}
	return false, nil
}

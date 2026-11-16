package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// RuntimeTempDir returns a tempfs directory scoped to one Zator installation.
func RuntimeTempDir(zatorRoot string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(zatorRoot)))
	return filepath.Join(os.TempDir(), "zator", hex.EncodeToString(sum[:8]))
}

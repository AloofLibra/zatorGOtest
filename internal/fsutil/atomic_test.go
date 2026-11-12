package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "file.txt")

	if err := AtomicWriteFile(path, []byte("one"), 0); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := AtomicWriteFile(path, []byte("two"), 0); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "two" {
		t.Fatalf("content = %q, want %q", got, "two")
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestAtomicWriteFileEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty")
	if err := AtomicWriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 0 {
		t.Fatalf("size = %d, want 0", st.Size())
	}
}

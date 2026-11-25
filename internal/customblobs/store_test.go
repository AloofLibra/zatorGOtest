package customblobs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/config"
)

type stubTracker struct {
	refs   map[string]int
	renamed [][2]string
	fail   error
}

func (st *stubTracker) CountBlobRefs(name string) (int, error) {
	return st.refs[name], nil
}

func (st *stubTracker) RenameBlob(oldName, newName string) (int, error) {
	if st.fail != nil {
		return 0, st.fail
	}
	st.renamed = append(st.renamed, [2]string{oldName, newName})
	return 1, nil
}

func newTestConfig(t *testing.T, withMarkers bool) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")
	markers := ""
	if withMarkers {
		markers = "#Z2R_CUSTOM_BLOBS_BEGIN\n#Z2R_CUSTOM_BLOBS_END\n"
	}
	raw := "NFQWS2_OPT=\"\n--lua-init=@/opt/zator/lua/locked.lua\n--blob=maxru:@/opt/zator/files/fake/tls_clienthello_max_ru.bin\n" + markers + "--template=z2r_tcp_tls_common\n\"\n"
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, cfgPath
}

func TestValidationAndDefaults(t *testing.T) {
	for _, bad := range []string{"", "1abc", "0xdead", "bad-name", strings.Repeat("a", 33)} {
		if ValidateBlobName(bad) == nil {
			t.Errorf("ValidateBlobName(%q) should fail", bad)
		}
	}
	for _, good := range []string{"my_fake", "_priv1", "QUIC_Custom_2"} {
		if err := ValidateBlobName(good); err != nil {
			t.Errorf("ValidateBlobName(%q) failed: %v", good, err)
		}
	}
	for _, badFile := range []string{"", "../x.bin", "sub/a.bin", `sub\a.bin`, ".hidden.bin", "with space.bin", strings.Repeat("x", 65)} {
		if ValidateBlobFileName(badFile) == nil {
			t.Errorf("ValidateBlobFileName(%q) should fail", badFile)
		}
	}
	if got := DefaultBlobNameFromFile("123-my.fake.bin"); got != "blob_123_my_fake" {
		t.Errorf("DefaultBlobNameFromFile=%q", got)
	}
	if got := DefaultBlobNameFromFile("_bad.bin"); got != "blob_1" {
		t.Errorf("DefaultBlobNameFromFile(_bad)=%q", got)
	}
}

func TestUploadLimitsAndCollisions(t *testing.T) {
	cfg, _ := newTestConfig(t, true)
	dir := t.TempDir()
	st := NewStore(filepath.Join(dir, "custom_blobs.tsv"), filepath.Join(dir, "custom_blob"))

	if _, err := st.Upload("empty.bin", nil, cfg); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("empty upload: want ErrTooLarge, got %v", err)
	}
	if _, err := st.Upload("huge.bin", make([]byte, MaxBlobSize+1), cfg); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize upload: want ErrTooLarge, got %v", err)
	}

	created, err := st.Upload("my-fake.bin", []byte{1, 2, 3}, cfg)
	if err != nil || created == nil || created.Name != "my_fake" {
		t.Fatalf("upload: created=%+v err=%v", created, err)
	}

	// Uploading file whose auto-name collides with stock blob stores file without auto-adding TSV entry.
	created2, err := st.Upload("maxru.bin", []byte{4, 5}, cfg)
	if err != nil || created2 != nil {
		t.Fatalf("collision upload: created=%+v err=%v", created2, err)
	}
	if _, err := st.Add("maxru", "maxru.bin", "dup", cfg); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("Add stock name: want ErrNameTaken, got %v", err)
	}
	if _, err := st.Add("clone_vk", "maxru.bin", "dup", cfg); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("Add builtin clone name: want ErrNameTaken, got %v", err)
	}
}

func TestCRUDApplyAndDirty(t *testing.T) {
	cfg, cfgPath := newTestConfig(t, false)
	dir := t.TempDir()
	st := NewStore(filepath.Join(dir, "custom_blobs.tsv"), filepath.Join(dir, "custom_blob"))

	created, err := st.Upload("first.bin", []byte("ABC"), cfg)
	if err != nil || created == nil {
		t.Fatalf("Upload: %v", err)
	}
	// Shared file under two names.
	second, err := st.Add("second_alias", "first.bin", "second", cfg)
	if err != nil || second.ID != 2 {
		t.Fatalf("Add: %+v err=%v", second, err)
	}

	dirty, err := st.IsDirty(cfg)
	if err != nil || !dirty {
		t.Fatalf("IsDirty before apply: dirty=%v err=%v", dirty, err)
	}
	changed, err := st.ApplyFile(cfgPath)
	if err != nil || !changed {
		t.Fatalf("ApplyFile initial: changed=%v err=%v", changed, err)
	}
	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if dirty, _ := st.IsDirty(cfg); dirty {
		t.Fatalf("expected clean after Apply")
	}
	// Idempotent second Apply.
	if changed2, err := st.ApplyFile(cfgPath); err != nil || changed2 {
		t.Fatalf("ApplyFile idempotent: changed=%v err=%v", changed2, err)
	}

	tracker := &stubTracker{refs: map[string]int{"first": 1}}
	if err := st.Delete(created.ID, tracker); !errors.Is(err, ErrBlobInUse) {
		t.Fatalf("Delete in-use: want ErrBlobInUse, got %v", err)
	}
	tracker.refs["first"] = 0
	if _, renamed, err := st.Edit(created.ID, "first_new", "first.bin", "upd\tline", cfg, tracker); err != nil || !renamed {
		t.Fatalf("Edit rename: renamed=%v err=%v", renamed, err)
	}
	if len(tracker.renamed) != 1 || tracker.renamed[0] != [2]string{"first", "first_new"} {
		t.Fatalf("tracker.renamed=%+v", tracker.renamed)
	}

	// Deleting one alias leaves physical file intact while second alias still references it.
	if err := st.Delete(created.ID, tracker); err != nil {
		t.Fatalf("Delete alias 1: %v", err)
	}
	if _, err := os.Stat(filepath.Join(st.BlobDir(), "first.bin")); err != nil {
		t.Fatalf("shared file should still exist: %v", err)
	}
	// Reload config and verify IsDirty catches remaining unapplied delete.
	cfg2, _ := config.Load(cfgPath)
	if dirty, _ := st.IsDirty(cfg2); !dirty {
		t.Fatalf("expected dirty after deleting applied entry")
	}

	// Deleting last entry using first.bin removes file too.
	if err := st.Delete(second.ID, tracker); err != nil {
		t.Fatalf("Delete alias 2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(st.BlobDir(), "first.bin")); !os.IsNotExist(err) {
		t.Fatalf("last alias deleted: expected file removed, err=%v", err)
	}
	if changed, err := st.Apply(cfg2); err != nil || !changed {
		t.Fatalf("Apply cleanup: changed=%v err=%v", changed, err)
	}
	if dirty, _ := st.IsDirty(cfg2); dirty {
		t.Fatalf("expected clean after applying empty list")
	}
}

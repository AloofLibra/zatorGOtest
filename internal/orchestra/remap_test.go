package orchestra

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemapLocksSinglePass(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := os.MkdirAll(s.ScopesDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.LockedPath(), []byte("1\ttls\t45\n2\ttls\t46\n5\tudp\t33\n3\ttls\t99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.ManualPath(), []byte("8\ttls\t45\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.ScopesDir(), "mark_7.tsv"), []byte("1\ttls\t45\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	autoPath := filepath.Join(dir, "auto_locked.tsv")
	if err := os.WriteFile(autoPath, []byte("tls\tdefault\tsn-1\t45\tlearned\ntls\tdefault\tsn-2\t46\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rules := []LockRemapRule{
		{Profile: 1, Proto: ProtoTLS, OldStrategy: 45, NewStrategy: 46},
		{Profile: 2, Proto: ProtoTLS, OldStrategy: 46, NewStrategy: 47},
		{Profile: 8, Proto: ProtoTLS, OldStrategy: 45, NewStrategy: 46},
		{Profile: 5, Proto: ProtoUDP, OldStrategy: 33, NewStrategy: 0}, // disabled -> dropped
	}
	maxRules := []LockMaxRule{
		{Profile: 3, Proto: ProtoTLS, MaxStrategy: 50}, // 99 > 50 -> dropped
	}
	autoRules := []AutoLockRemapRule{
		{Key: "tls", OldStrategy: 45, NewStrategy: 46},
		{Key: "tls", OldStrategy: 46, NewStrategy: 47},
	}

	moved, dropped, err := s.RemapLocks(rules, maxRules, autoRules, nil, autoPath)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 6 || dropped != 2 {
		t.Fatalf("moved=%d dropped=%d, want moved=6 dropped=2", moved, dropped)
	}

	got1, _ := s.GetLock(ScopeDefault, "1", "tls")
	got2, _ := s.GetLock(ScopeDefault, "2", "tls")
	got3, _ := s.GetLock(ScopeDefault, "3", "tls")
	got5, _ := s.GetLock(ScopeDefault, "5", "udp")
	got8, _ := s.GetLock(ScopeDefault, "8", "tls")
	gotSc, _ := s.GetLock("mark:7", "1", "tls")
	if got1 != 46 || got2 != 47 || !got3.IsAuto() || !got5.IsAuto() || got8 != 46 || gotSc != 46 {
		t.Fatalf("unexpected locks after remap: 1=%v 2=%v 3=%v 5=%v 8=%v sc=%v", got1, got2, got3, got5, got8, gotSc)
	}
	rawAuto, _ := os.ReadFile(autoPath)
	wantAuto := "tls\tdefault\tsn-1\t46\tlearned\ntls\tdefault\tsn-2\t47\n"
	if string(rawAuto) != wantAuto {
		t.Fatalf("auto_locked = %q, want %q", string(rawAuto), wantAuto)
	}
}

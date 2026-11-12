package orchestra

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestPidAlive(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("current process must be alive")
	}
	// Очень большой PID практически гарантированно не существует.
	if pidAlive(0x7ffffff0) {
		t.Error("bogus pid reported alive")
	}
}

func TestLockReleasedAfterOperation(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetLock("default", "3", "tls", 1); err != nil {
		t.Fatal(err)
	}
	if fileExists(s.lockDir()) {
		t.Error("lock directory must be removed after the operation")
	}
}

func TestLockReleasedOnError(t *testing.T) {
	s := newTestStore(t)
	boom := errors.New("boom")
	if err := s.withLock(func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if fileExists(s.lockDir()) {
		t.Error("lock directory leaked after fn error")
	}
	// Блокировку можно взять снова.
	if err := s.withLock(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestStaleLockOfDeadProcessIsReaped(t *testing.T) {
	s := newTestStore(t)
	if err := os.MkdirAll(s.lockDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	// Владелец — несуществующий процесс.
	writeFile(t, filepath.Join(s.lockDir(), "owner."+strconv.Itoa(0x7ffffff0)), "")

	s.LockTimeout = 5 * time.Second
	if err := s.SetLock("default", "3", "tls", 2); err != nil {
		t.Fatalf("SetLock behind stale lock: %v", err)
	}
	if got := mustGet(t, s, "default", "3", "tls"); got != 2 {
		t.Errorf("got %v", got)
	}
}

func TestOrphanEmptyLockDirIsReapedAfterAge(t *testing.T) {
	s := newTestStore(t)
	s.StaleLockAge = time.Millisecond
	s.LockTimeout = 5 * time.Second
	if err := os.MkdirAll(s.lockDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(s.lockDir(), old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLock("default", "3", "tls", 2); err != nil {
		t.Fatalf("SetLock behind orphan lock: %v", err)
	}
}

func TestLiveLockTimesOut(t *testing.T) {
	s := newTestStore(t)
	s.LockTimeout = 150 * time.Millisecond
	if err := os.MkdirAll(s.lockDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	// Владелец — этот же (живой) процесс, но блокировка взята «снаружи».
	owner := filepath.Join(s.lockDir(), "owner."+strconv.Itoa(os.Getpid()))
	writeFile(t, owner, "")

	start := time.Now()
	err := s.SetLock("default", "3", "tls", 2)
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("err = %v, want ErrLockTimeout", err)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Error("returned before the timeout elapsed")
	}
	if fileExists(s.LockedPath()) {
		t.Error("locked.tsv written without the lock")
	}
	// Чужая живая блокировка не должна быть снята.
	if !fileExists(owner) {
		t.Error("live owner marker was removed")
	}
}

func TestReadsDoNotNeedLock(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetLock("default", "3", "tls", 4); err != nil {
		t.Fatal(err)
	}
	// Заблокированное хранилище по-прежнему читается.
	if err := os.MkdirAll(s.lockDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(s.lockDir(), "owner."+strconv.Itoa(os.Getpid())), "")
	if got := mustGet(t, s, "default", "3", "tls"); got != 4 {
		t.Errorf("got %v", got)
	}
	if s.StatusText() != "Есть" {
		t.Error("status must be readable under lock")
	}
}

func TestLockDirNameMatchesShell(t *testing.T) {
	s := NewStore(filepath.Join("a", "orch"))
	if got, want := filepath.Base(s.lockDir()), "locked.tsv.write-lock"; got != want {
		t.Errorf("lock dir = %q, want %q", got, want)
	}
}

package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Межпроцессная mkdir-блокировка (как orchestra write-lock).
const (
	DefaultLockTimeout  = 10 * time.Second
	DefaultStaleLockAge = 30 * time.Second
	lockPollInterval    = 5 * time.Millisecond
	ownerPrefix         = "owner."
	LockDirSuffix       = ".write-lock"
)

var procLocks sync.Map // lock dir -> *sync.Mutex

func procMutex(key string) *sync.Mutex {
	m, _ := procLocks.LoadOrStore(key, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// WithDirLock выполняет fn под эксклюзивной блокировкой каталога path+LockDirSuffix.
// Блокировка мёртвого PID и пустого каталога старше staleAge снимается.
func WithDirLock(path string, timeout, staleAge time.Duration, fn func() error) error {
	return WithDirLockCause(path, timeout, staleAge, nil, fn)
}

// WithDirLockCause is WithDirLock with a caller-specific timeout error for errors.Is.
func WithDirLockCause(path string, timeout, staleAge time.Duration, timeoutCause error, fn func() error) error {
	if path == "" {
		return fn()
	}
	dir := path + LockDirSuffix
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mu := procMutex(dir)
	mu.Lock()
	defer mu.Unlock()

	if timeout <= 0 {
		timeout = DefaultLockTimeout
	}
	if staleAge <= 0 {
		staleAge = DefaultStaleLockAge
	}
	deadline := time.Now().Add(timeout)
	for {
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			break
		}
		if !os.IsExist(err) && !os.IsPermission(err) {
			return err
		}
		reapStaleLock(dir, staleAge)
		if time.Now().After(deadline) {
			if timeoutCause != nil {
				return fmt.Errorf("%w (%s): если все операции остановлены, удалите каталог блокировки", timeoutCause, dir)
			}
			return fmt.Errorf("fsutil: timeout lock %s (удалите каталог, если операции остановлены)", dir)
		}
		time.Sleep(lockPollInterval)
	}
	owner := filepath.Join(dir, ownerPrefix+strconv.Itoa(os.Getpid()))
	defer func() {
		_ = os.Remove(owner)
		_ = os.Remove(dir)
	}()
	f, err := os.Create(owner)
	if err != nil {
		return err
	}
	_ = f.Close()
	return fn()
}

func reapStaleLock(dir string, staleAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	owners := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, ownerPrefix) {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimPrefix(name, ownerPrefix))
		if err != nil || pid <= 0 {
			continue
		}
		owners++
		if !pidAlive(pid) {
			if os.Remove(filepath.Join(dir, name)) == nil {
				_ = os.Remove(dir)
			}
		}
	}
	if owners > 0 {
		return
	}
	if st, err := os.Stat(dir); err == nil && time.Since(st.ModTime()) > staleAge {
		_ = os.Remove(dir)
	}
}

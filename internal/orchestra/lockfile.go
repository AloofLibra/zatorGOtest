package orchestra

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Значения по умолчанию для файловой блокировки записи.
const (
	DefaultLockTimeout  = 10 * time.Second
	DefaultStaleLockAge = 30 * time.Second

	lockPollInterval = 5 * time.Millisecond
	ownerPrefix      = "owner."
	lockDirSuffix    = ".write-lock"
)

// procLocks сериализует писателей внутри одного процесса (PID у них общий,
// поэтому файловая блокировка сама по себе их не различает).
var procLocks sync.Map // lock dir -> *sync.Mutex

func procMutex(key string) *sync.Mutex {
	m, _ := procLocks.LoadOrStore(key, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// lockDir — каталог-блокировка; совпадает с lib/orchestra_state.sh
// ("${ORCH_LOCK_FILE}.write-lock"), поэтому shell- и Go-писатели исключают друг друга.
func (s *Store) lockDir() string { return s.LockedPath() + lockDirSuffix }

// withLock выполняет fn под эксклюзивной блокировкой записи.
//
// Блокировка — каталог <locked.tsv>.write-lock (mkdir атомарен на любой ОС)
// с маркером owner.<PID> внутри. Блокировка мёртвого процесса (PID не жив) и
// «осиротевшего» пустого каталога старше StaleLockAge снимается автоматически.
func (s *Store) withLock(fn func() error) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	dir := s.lockDir()

	mu := procMutex(dir)
	mu.Lock()
	defer mu.Unlock()

	timeout := s.LockTimeout
	if timeout <= 0 {
		timeout = DefaultLockTimeout
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
		s.reapStaleLock(dir)
		if time.Now().After(deadline) {
			return fmt.Errorf("%w (%s): если все операции остановлены, удалите каталог блокировки", ErrLockTimeout, dir)
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

// reapStaleLock снимает блокировку, если её владельцы мертвы либо каталог
// давно пуст.
func (s *Store) reapStaleLock(dir string) {
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
			// Только один конкурент может удалить маркер и затем пустой каталог.
			if os.Remove(filepath.Join(dir, name)) == nil {
				_ = os.Remove(dir)
			}
		}
	}
	if owners > 0 {
		return
	}
	age := s.StaleLockAge
	if age <= 0 {
		age = DefaultStaleLockAge
	}
	if st, err := os.Stat(dir); err == nil && time.Since(st.ModTime()) > age {
		_ = os.Remove(dir)
	}
}

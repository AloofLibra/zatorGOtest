package orchestra

import "github.com/AloofLibra/zator/internal/fsutil"

// Значения по умолчанию для файловой блокировки записи.
const (
	DefaultLockTimeout  = fsutil.DefaultLockTimeout
	DefaultStaleLockAge = fsutil.DefaultStaleLockAge
)

// lockDir совпадает с lib/orchestra_state.sh, чтобы Go- и shell-писатели
// исключали друг друга.
func (s *Store) lockDir() string { return s.LockedPath() + fsutil.LockDirSuffix }

func (s *Store) withLock(fn func() error) error {
	return fsutil.WithDirLockCause(s.LockedPath(), s.LockTimeout, s.StaleLockAge, ErrLockTimeout, fn)
}

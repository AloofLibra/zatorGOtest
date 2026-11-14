package fsutil

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWithDirLockSerializes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	_ = os.WriteFile(path, []byte("a"), 0o644)

	var order []int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			err := WithDirLock(path, time.Second, time.Second, func() error {
				mu.Lock()
				order = append(order, n)
				mu.Unlock()
				time.Sleep(20 * time.Millisecond)
				return nil
			})
			if err != nil {
				t.Errorf("lock: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if len(order) != 3 {
		t.Fatalf("order=%v", order)
	}
}

func TestWithDirLockEmptyPath(t *testing.T) {
	called := false
	if err := WithDirLock("", 0, 0, func() error {
		called = true
		return nil
	}); err != nil || !called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

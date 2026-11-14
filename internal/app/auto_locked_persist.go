package app

import (
	"os"
	"path/filepath"

	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/paths"
)

// SyncAutoLockedPersist восстанавливает /tmp/auto_locked.tsv из StateDir
// после reboot (если live пуст) и копирует live → state, если live новее/непуст.
func (a *App) SyncAutoLockedPersist() {
	live := paths.LiveAutoLockedTSV()
	state := a.Paths.AutoLockedTSV
	if state == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(state), 0o755)

	liveData, liveErr := os.ReadFile(live)
	stateData, stateErr := os.ReadFile(state)

	liveOK := liveErr == nil && len(liveData) > 0
	stateOK := stateErr == nil && len(stateData) > 0

	switch {
	case liveOK:
		_ = fsutil.AtomicWriteFile(state, liveData, 0o644)
	case stateOK && !liveOK:
		_ = os.MkdirAll(filepath.Dir(live), 0o755)
		_ = fsutil.AtomicWriteFile(live, stateData, 0o644)
	}
}

// PersistAutoLocked копирует live → state (вызывать на stop).
func (a *App) PersistAutoLocked() {
	live := paths.LiveAutoLockedTSV()
	state := a.Paths.AutoLockedTSV
	if state == "" {
		return
	}
	data, err := os.ReadFile(live)
	if err != nil || len(data) == 0 {
		return
	}
	_ = os.MkdirAll(filepath.Dir(state), 0o755)
	_ = fsutil.AtomicWriteFile(state, data, 0o644)
}

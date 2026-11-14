package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AloofLibra/zator/internal/backup"
	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/deploy"
)

// CreatePreUpdateBackup снимает product-backup перед update (config+lists+orchestra).
func (a *App) CreatePreUpdateBackup() (backup.Info, error) {
	mgr := backup.NewIn(a.Paths, a.Paths.BackupDir)
	info, err := mgr.Create(backup.CreateOptions{IncludeLists: true})
	if err != nil {
		return backup.Info{}, err
	}
	a.RememberPreUpdateBackup(info.Name)
	return info, nil
}

// VerifyAfterUpdate — проверка после Apply: bin, config, непустые валидные args.
func (a *App) VerifyAfterUpdate() error {
	var errs []string
	if _, err := os.Stat(a.Paths.ZapretConfig); err != nil {
		errs = append(errs, "нет zapret2/config")
	}
	argsPath := filepath.Join(a.Paths.ZapretRoot, "nfqws2.args")
	if st, err := os.Stat(argsPath); err != nil || st.Size() == 0 {
		errs = append(errs, "нет или пуст nfqws2.args")
	} else if raw, err := os.ReadFile(argsPath); err != nil {
		errs = append(errs, "не читается nfqws2.args")
	} else {
		var toks []string
		for _, ln := range strings.Split(string(raw), "\n") {
			ln = strings.TrimSpace(ln)
			if ln != "" && !strings.HasPrefix(ln, "#") {
				toks = append(toks, ln)
			}
		}
		if err := config.ValidateNfqws2Args(toks); err != nil {
			errs = append(errs, "nfqws2.args: "+err.Error())
		}
	}
	if _, err := deploy.ReadVersion(a.Paths); err != nil {
		_ = err // version.env может появиться позже — мягко
	}
	bin := filepath.Join(a.Paths.BinDir, "zator")
	if _, err := os.Stat(bin); err != nil {
		errs = append(errs, "нет бинарника zator")
	}
	if len(errs) > 0 {
		msg := strings.Join(errs, "; ")
		a.WritePostUpdateVerify(false, msg)
		return fmt.Errorf("post-update verify: %s", msg)
	}
	a.WritePostUpdateVerify(true, "ok")
	return nil
}

// RollbackPreUpdateBackup восстанавливает smart из LastPreUpdateBackup и
// повторяет VerifyAfterUpdate.
func (a *App) RollbackPreUpdateBackup() error {
	name := a.LastPreUpdateBackup()
	if name == "" {
		return fmt.Errorf("нет pre-update backup для отката")
	}
	mgr := backup.NewIn(a.Paths, a.Paths.BackupDir)
	if _, err := mgr.Restore(name, backup.RestoreOptions{Mode: backup.ModeSmart}); err != nil {
		return fmt.Errorf("rollback %s: %w", name, err)
	}
	a.ClearBlockFullRestore()
	if err := a.VerifyAfterUpdate(); err != nil {
		a.WriteServiceWarnings([]string{"rollback выполнен, verify всё ещё fail: " + err.Error()})
		return err
	}
	a.WriteServiceWarnings([]string{"выполнен smart-откат из pre-update backup " + name})
	return nil
}

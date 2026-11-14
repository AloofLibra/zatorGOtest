package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/AloofLibra/zator/internal/backup"
	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/deploy"
	"github.com/AloofLibra/zator/internal/fsutil"
)

const preUpdateBinRel = "pre_update_bin/zator"

// CreatePreUpdateBackup снимает product-backup перед update (config+lists+orchestra)
// и снимок бинарника bin/zator (ModeSmart его не откатывает).
func (a *App) CreatePreUpdateBackup() (backup.Info, error) {
	mgr := backup.NewIn(a.Paths, a.Paths.BackupDir)
	info, err := mgr.Create(backup.CreateOptions{IncludeLists: true})
	if err != nil {
		return backup.Info{}, err
	}
	a.RememberPreUpdateBackup(info.Name)
	_ = a.snapshotPreUpdateBinary()
	return info, nil
}

func (a *App) preUpdateBinaryPath() string {
	return filepath.Join(a.Paths.StateDir, filepath.FromSlash(preUpdateBinRel))
}

func (a *App) snapshotPreUpdateBinary() error {
	src := filepath.Join(a.Paths.BinDir, "zator")
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	dst := a.preUpdateBinaryPath()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	_ = os.Remove(dst)
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (a *App) restorePreUpdateBinary() error {
	src := a.preUpdateBinaryPath()
	st, err := os.Stat(src)
	if err != nil || st.Size() == 0 {
		return fmt.Errorf("нет снимка bin/zator")
	}
	dst := filepath.Join(a.Paths.BinDir, "zator")
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	_ = os.MkdirAll(a.Paths.BinDir, 0o755)
	return fsutil.AtomicWriteFile(dst, data, 0o755)
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

// RollbackPreUpdateBackup: smart restore архива + откат bin/zator из снимка.
func (a *App) RollbackPreUpdateBackup() error {
	name := a.LastPreUpdateBackup()
	if name == "" {
		return fmt.Errorf("нет pre-update backup для отката")
	}
	mgr := backup.NewIn(a.Paths, a.Paths.BackupDir)
	if _, err := mgr.Restore(name, backup.RestoreOptions{Mode: backup.ModeSmart}); err != nil {
		return fmt.Errorf("rollback %s: %w", name, err)
	}
	binMsg := ""
	if err := a.restorePreUpdateBinary(); err != nil {
		binMsg = "; bin: " + err.Error()
	}
	a.ClearBlockFullRestore()
	if err := a.VerifyAfterUpdate(); err != nil {
		a.WriteServiceWarnings([]string{"rollback выполнен, verify всё ещё fail: " + err.Error() + binMsg})
		return err
	}
	a.WriteServiceWarnings([]string{"выполнен smart-откат из pre-update backup " + name + binMsg})
	return nil
}

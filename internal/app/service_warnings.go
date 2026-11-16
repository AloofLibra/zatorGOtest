package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/paths"
)

const (
	serviceWarningsFile  = "service_warnings.txt"
	postUpdateBackupFile = "pre_update_backup.name"
	postUpdateVerifyFile = "post_update_verify.txt"
	blockFullRestoreFile = "block_full_restore.flag"
)

func updateStatusPath(p paths.Paths, name string) string {
	return filepath.Join(updateTempRoot(p), name)
}

// WriteServiceWarnings сохраняет soft-ошибки install/start для UI Overview.
func (a *App) WriteServiceWarnings(msgs []string) {
	path := updateStatusPath(a.Paths, serviceWarningsFile)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if len(msgs) == 0 {
		_ = os.Remove(path)
		return
	}
	_ = fsutil.AtomicWriteFile(path, []byte(strings.Join(msgs, "\n")+"\n"), 0o644)
}

// ClearServiceWarnings снимает marker после успешного старта zapret2.
func (a *App) ClearServiceWarnings() {
	_ = os.Remove(updateStatusPath(a.Paths, serviceWarningsFile))
}

// ServiceWarnings читает сохранённые предупреждения служб.
func (a *App) ServiceWarnings() []string {
	b, err := os.ReadFile(updateStatusPath(a.Paths, serviceWarningsFile))
	if err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

// RememberPreUpdateBackup запоминает имя архива до update (для отката UI).
func (a *App) RememberPreUpdateBackup(name string) {
	_ = os.MkdirAll(updateTempRoot(a.Paths), 0o755)
	_ = fsutil.AtomicWriteFile(updateStatusPath(a.Paths, postUpdateBackupFile), []byte(name+"\n"), 0o644)
	_ = fsutil.AtomicWriteFile(updateStatusPath(a.Paths, blockFullRestoreFile), []byte("1\n"), 0o644)
}

// LastPreUpdateBackup возвращает имя последнего pre-update бэкапа.
func (a *App) LastPreUpdateBackup() string {
	b, err := os.ReadFile(updateStatusPath(a.Paths, postUpdateBackupFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// BlockFullRestore — после update ModeFull из старого архива запрещён.
func (a *App) BlockFullRestore() bool {
	_, err := os.Stat(updateStatusPath(a.Paths, blockFullRestoreFile))
	return err == nil
}

// ClearBlockFullRestore снимает запрет (ручной clear или успешный smart restore).
func (a *App) ClearBlockFullRestore() {
	_ = os.Remove(updateStatusPath(a.Paths, blockFullRestoreFile))
}

// WritePostUpdateVerify сохраняет итог post-update проверки (ok|fail).
func (a *App) WritePostUpdateVerify(ok bool, detail string) {
	status := "ok"
	if !ok {
		status = "fail"
	}
	a.WritePostUpdateVerifyStatus(status, detail)
}

// WritePostUpdateVerifyStatus пишет произвольный статус (ok|fail|warn).
func (a *App) WritePostUpdateVerifyStatus(status, detail string) {
	_ = os.MkdirAll(updateTempRoot(a.Paths), 0o755)
	status = strings.TrimSpace(status)
	if status == "" {
		status = "fail"
	}
	_ = fsutil.AtomicWriteFile(updateStatusPath(a.Paths, postUpdateVerifyFile),
		[]byte(status+"\n"+strings.TrimSpace(detail)+"\n"), 0o644)
}

// PostUpdateVerifyStatus — "ok"|"fail"|"warn"|"" .
func (a *App) PostUpdateVerifyStatus() (status, detail string) {
	b, err := os.ReadFile(updateStatusPath(a.Paths, postUpdateVerifyFile))
	if err != nil {
		return "", ""
	}
	lines := strings.SplitN(string(b), "\n", 2)
	status = strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		detail = strings.TrimSpace(lines[1])
	}
	return status, detail
}

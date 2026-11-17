package webapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/backup"
)

var backupNameRe = regexp.MustCompile(`^z2r_backup_(\d{8})_(\d{6})\.tar$`)

const backupTimeLayout = "20060102_150405"

type backupItem struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Date string `json:"date"`
}

func backupDate(name string) string {
	m := backupNameRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	t, err := time.Parse(backupTimeLayout, m[1]+"_"+m[2])
	if err != nil {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

func (s *Server) backupsList() (map[string]any, error) {
	infos, err := backup.NewIn(s.paths, s.paths.BackupDir).List()
	if err != nil {
		return nil, err
	}
	items := make([]backupItem, 0, len(infos))
	for _, item := range infos {
		items = append(items, backupItem{Name: item.Name, Size: item.Size, Date: backupDate(item.Name)})
	}
	return map[string]any{"items": items}, nil
}

// resolveBackup проверяет имя по шаблону и наличие файла в каталоге бэкапов
// (path traversal исключён).
func (s *Server) resolveBackup(name string) (string, bool) {
	p, err := backup.NewIn(s.paths, s.paths.BackupDir).Resolve(name)
	return p, err == nil
}

func (s *Server) createBackup() (string, error) {
	mgr := backup.NewIn(s.paths, s.paths.BackupDir)
	mgr.Now = s.now
	info, err := mgr.Create(backup.CreateOptions{IncludeLists: true, IncludeExtraStrats: true, IncludeOrchestraState: true})
	return info.Name, err
}

func (s *Server) importBackup(original string, data []byte) (string, error) {
	tmp, err := os.CreateTemp("", "zator-backup-upload-*.tar")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	mgr := backup.NewIn(s.paths, s.paths.BackupDir)
	mgr.Now = s.now
	return mgr.Import(name, filepath.Base(original))
}

// GET|POST /cgi-bin/backups.cgi
func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request, p params) {
	if !requireMethod(w, r, http.MethodGet, http.MethodPost) {
		return
	}
	action := p.Get("action")
	if r.Method == http.MethodGet {
		if action == "download" {
			s.backupDownload(w, p.Get("name"))
			return
		}
		v, err := s.backupsList()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Не удалось прочитать список бэкапов")
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	}
	switch action {
	case "create":
		name, err := s.createBackup()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Не удалось создать бэкап")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
	case "delete":
		name := p.Get("name")
		if name == "" {
			writeError(w, http.StatusBadRequest, "Не указано имя файла")
			return
		}
		if err := backup.NewIn(s.paths, s.paths.BackupDir).Delete(name); err != nil {
			writeError(w, http.StatusNotFound, "Бэкап не найден")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
	case "restore":
		name := p.Get("name")
		if name == "" {
			writeError(w, http.StatusBadRequest, "Не указано имя файла")
			return
		}
		path, ok := s.resolveBackup(name)
		if !ok {
			writeError(w, http.StatusNotFound, "Бэкап не найден")
			return
		}
		mode := strings.ToLower(strings.TrimSpace(p.Get("mode")))
		if mode == "" {
			mode = "smart"
		}
		n, err := s.restoreBackup(path, mode)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Не удалось восстановить бэкап: "+err.Error())
			return
		}
		restarted := s.maybeRestart(r.Context(), p)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "name": name, "mode": mode, "files": n, "restarted": restarted,
		})
	case "upload":
		s.backupUpload(w, r, p.Get("name"))
	default:
		writeError(w, http.StatusBadRequest, "Неизвестное действие")
	}
}

// restoreBackup восстанавливает архивы WebUI и internal/backup единым механизмом.
func (s *Server) restoreBackup(archivePath, mode string) (int, error) {
	bm, err := backup.ParseMode(mode)
	if err != nil {
		return 0, err
	}
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	blockFull := s.app != nil && s.app.BlockFullRestore()
	res, err := backup.NewIn(s.paths, s.paths.BackupDir).RestoreFile(archivePath, backup.RestoreOptions{Mode: bm, BlockFull: blockFull})
	if err != nil {
		return 0, err
	}
	return len(res.Files), nil
}

func (s *Server) backupDownload(w http.ResponseWriter, name string) {
	if name == "" {
		writeError(w, http.StatusBadRequest, "Не указано имя файла")
		return
	}
	path, ok := s.resolveBackup(name)
	if !ok {
		writeError(w, http.StatusNotFound, "Бэкап не найден")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "Бэкап не найден")
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	if st != nil {
		w.Header().Set("Content-Length", fmt.Sprint(st.Size()))
	}
	_, _ = io.Copy(w, f)
}

func (s *Server) backupUpload(w http.ResponseWriter, r *http.Request, original string) {
	if r.ContentLength > maxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Файл слишком большой (максимум 32 МБ)")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Файл получен не полностью")
		return
	}
	switch {
	case len(data) == 0:
		writeError(w, http.StatusBadRequest, "Пустой файл")
		return
	case len(data) > maxUploadBytes:
		writeError(w, http.StatusRequestEntityTooLarge, "Файл слишком большой (максимум 32 МБ)")
		return
	case r.ContentLength > 0 && int64(len(data)) < r.ContentLength:
		writeError(w, http.StatusBadRequest, "Файл получен не полностью")
		return
	}
	name, err := s.importBackup(original, data)
	if err != nil {
		if errors.Is(err, backup.ErrNotBackup) {
			writeError(w, http.StatusBadRequest, "Файл не является архивом бэкапа")
			return
		}
		writeError(w, http.StatusInternalServerError, "Не удалось сохранить бэкап")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
}

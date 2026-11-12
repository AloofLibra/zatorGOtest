package webapi

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/fsutil"
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
	entries, err := os.ReadDir(s.paths.BackupDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	items := []backupItem{}
	for _, e := range entries {
		if e.IsDir() || !backupNameRe.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, backupItem{Name: e.Name(), Size: info.Size(), Date: backupDate(e.Name())})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name > items[j].Name })
	return map[string]any{"items": items}, nil
}

// resolveBackup проверяет имя по шаблону и наличие файла в каталоге бэкапов
// (path traversal исключён).
func (s *Server) resolveBackup(name string) (string, bool) {
	if !backupNameRe.MatchString(name) {
		return "", false
	}
	p := filepath.Join(s.paths.BackupDir, name)
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return "", false
	}
	return p, true
}

// uniqueBackupName подбирает свободное имя z2r_backup_<ts>[_N].tar.
func (s *Server) uniqueBackupName() string {
	base := "z2r_backup_" + s.now().Format(backupTimeLayout)
	name := base + ".tar"
	for i := 1; ; i++ {
		if _, err := os.Stat(filepath.Join(s.paths.BackupDir, name)); errors.Is(err, fs.ErrNotExist) {
			return name
		}
		name = fmt.Sprintf("%s_%d.tar", base, i)
	}
}

// backupSources — что попадает в архив: arc-имя -> путь на диске.
func (s *Server) backupSources() map[string]string {
	return map[string]string{
		"config":          s.paths.ZapretConfig,
		"lists":           s.paths.ListsDir,
		"extra_strats":    s.paths.ExtraStratsDir,
		"orchestra_state": s.paths.StateOrchestraDir(),
	}
}

func addFileToTar(tw *tar.Writer, arcName, path string, info fs.FileInfo) error {
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(arcName)
	hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

func (s *Server) createBackup() (string, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	names := make([]string, 0, 4)
	src := s.backupSources()
	for n := range src {
		names = append(names, n)
	}
	sort.Strings(names)
	count := 0
	for _, arc := range names {
		root := src[arc]
		st, err := os.Stat(root)
		if err != nil {
			continue
		}
		if !st.IsDir() {
			if err := addFileToTar(tw, arc, root, st); err != nil {
				return "", err
			}
			count++
			continue
		}
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			info, err := d.Info()
			if err != nil || !info.Mode().IsRegular() {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			count++
			return addFileToTar(tw, arc+"/"+filepath.ToSlash(rel), p, info)
		})
		if err != nil {
			return "", err
		}
	}
	if count == 0 {
		return "", errors.New("нет данных для бэкапа")
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.paths.BackupDir, 0o755); err != nil {
		return "", err
	}
	name := s.uniqueBackupName()
	if err := fsutil.AtomicWriteFile(filepath.Join(s.paths.BackupDir, name), buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	return name, nil
}

// validBackupArchive проверяет, что data — tar с config либо файлами lists/ / extra_strats/.
func validBackupArchive(data []byte) bool {
	tr := tar.NewReader(bytes.NewReader(data))
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return found
		}
		if err != nil {
			return false
		}
		n := strings.TrimPrefix(filepath.ToSlash(hdr.Name), "./")
		if n == "config" || strings.HasPrefix(n, "lists/") || strings.HasPrefix(n, "extra_strats/") {
			found = true
		}
	}
}

func (s *Server) importBackup(original string, data []byte) (string, error) {
	if err := os.MkdirAll(s.paths.BackupDir, 0o755); err != nil {
		return "", err
	}
	name := filepath.Base(original)
	if !backupNameRe.MatchString(name) {
		name = s.uniqueBackupName()
	} else if _, err := os.Stat(filepath.Join(s.paths.BackupDir, name)); err == nil {
		name = s.uniqueBackupName()
	}
	if err := fsutil.AtomicWriteFile(filepath.Join(s.paths.BackupDir, name), data, 0o644); err != nil {
		return "", err
	}
	return name, nil
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
		path, ok := s.resolveBackup(name)
		if !ok {
			writeError(w, http.StatusNotFound, "Бэкап не найден")
			return
		}
		if err := os.Remove(path); err != nil {
			writeError(w, http.StatusInternalServerError, "Не удалось удалить бэкап")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
	case "upload":
		s.backupUpload(w, r, p.Get("name"))
	default:
		writeError(w, http.StatusBadRequest, "Неизвестное действие")
	}
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
	case !validBackupArchive(data):
		writeError(w, http.StatusBadRequest, "Файл не является архивом бэкапа")
		return
	}
	name, err := s.importBackup(original, data)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Не удалось сохранить бэкап")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
}

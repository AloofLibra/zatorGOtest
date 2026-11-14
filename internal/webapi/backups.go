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
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/backup"
	"github.com/AloofLibra/zator/internal/fetch"
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

// restoreBackup восстанавливает архив Web UI (config/lists/extra_strats/orchestra_state)
// или формат internal/backup (config/lists/orchestra). mode: full|smart|lists.
func (s *Server) restoreBackup(archivePath, mode string) (int, error) {
	switch mode {
	case "full", "smart", "lists":
	default:
		return 0, fmt.Errorf("неизвестный режим: %s", mode)
	}
	layout, err := detectBackupLayout(archivePath)
	if err != nil {
		return 0, err
	}
	// Package C: сериализация с editConfigs + BlockFull после update.
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	blockFull := s.app != nil && s.app.BlockFullRestore()
	if layout == "manager" {
		mgr := backup.NewIn(s.paths, s.paths.BackupDir)
		bm, err := backup.ParseMode(mode)
		if err != nil {
			return 0, err
		}
		res, err := mgr.RestoreFile(archivePath, backup.RestoreOptions{Mode: bm, BlockFull: blockFull})
		if err != nil {
			return 0, err
		}
		return len(res.Files), nil
	}
	tmp, err := os.MkdirTemp("", "zator-restore-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	if err := extractPlainTar(archivePath, tmp); err != nil {
		return 0, err
	}
	if mode == "full" && blockFull {
		return 0, backup.ErrFullBlocked
	}
	snapRoot, err := os.MkdirTemp("", "zator-restore-snap-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(snapRoot)
	type snap struct {
		dst, snap string
		missing   bool
	}
	var undo []snap
	rollback := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			e := undo[i]
			if e.missing {
				_ = os.Remove(e.dst)
				continue
			}
			data, err := os.ReadFile(e.snap)
			if err != nil {
				continue
			}
			_ = fsutil.AtomicWriteFile(e.dst, data, 0o644)
		}
	}
	count := 0
	err = filepath.WalkDir(tmp, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		rel, err := filepath.Rel(tmp, p)
		if err != nil {
			return err
		}
		clean, err := fetch.CleanEntryName(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		dst := s.restoreDest(clean, mode)
		if dst == "" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		se := snap{dst: dst}
		if old, err := os.ReadFile(dst); err == nil {
			se.snap = filepath.Join(snapRoot, fmt.Sprintf("%d.bin", len(undo)))
			if err := os.WriteFile(se.snap, old, 0o644); err != nil {
				return err
			}
		} else if errors.Is(err, os.ErrNotExist) {
			se.missing = true
		} else {
			return err
		}
		undo = append(undo, se)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := fsutil.AtomicWriteFile(dst, data, 0o644); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		rollback()
		return count, err
	}
	return count, nil
}

// detectBackupLayout: "manager" (orchestra/|lists/ без orchestra_state) или "webui".
func detectBackupLayout(archivePath string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	webui, manager := false, false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		n := strings.TrimPrefix(filepath.ToSlash(hdr.Name), "./")
		switch {
		case strings.HasPrefix(n, "orchestra_state/") || strings.HasPrefix(n, "extra_strats/"):
			webui = true
		case strings.HasPrefix(n, "orchestra/") || n == "lists/TCP_Custom.txt":
			manager = true
		}
	}
	if webui {
		return "webui", nil
	}
	if manager {
		return "manager", nil
	}
	return "webui", nil
}

func (s *Server) restoreDest(name, mode string) string {
	name = path.Clean(name)
	switch {
	case name == "config":
		// smart/lists в webui-раскладке не затирают живой config.
		if mode == "lists" || mode == "smart" {
			return ""
		}
		return s.paths.ZapretConfig
	case strings.HasPrefix(name, "lists/"):
		return filepath.Join(s.paths.ListsDir, filepath.FromSlash(strings.TrimPrefix(name, "lists/")))
	case strings.HasPrefix(name, "extra_strats/"):
		return filepath.Join(s.paths.ExtraStratsDir, filepath.FromSlash(strings.TrimPrefix(name, "extra_strats/")))
	case strings.HasPrefix(name, "orchestra_state/"):
		return filepath.Join(s.paths.StateOrchestraDir(), filepath.FromSlash(strings.TrimPrefix(name, "orchestra_state/")))
	case strings.HasPrefix(name, "orchestra/"):
		return filepath.Join(s.paths.StateOrchestraDir(), filepath.FromSlash(strings.TrimPrefix(name, "orchestra/")))
	}
	return ""
}

func extractPlainTar(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := fetch.CleanEntryName(hdr.Name)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if hdr.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, tr)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
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

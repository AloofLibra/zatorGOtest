package webapi

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/customblobs"
	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/orchestra"
)

type customBlobDTO struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	File    string `json:"file"`
	Comment string `json:"comment"`
	Refs    int    `json:"refs"`
}

func (s *Server) customBlobsStore() *customblobs.Store {
	return customblobs.NewStore(s.paths.CustomBlobsTSV(), s.paths.CustomBlobDir())
}

func (s *Server) handleBlobs(w http.ResponseWriter, r *http.Request, p params) {
	if !requireMethod(w, r, http.MethodGet, http.MethodPost) {
		return
	}
	if r.Method == http.MethodGet {
		s.blobsGet(w)
		return
	}
	if strings.EqualFold(r.URL.Query().Get("action"), "upload") {
		out, err := s.blobsUpload(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	out, err := s.blobsAction(r, p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) blobsListPayload() (map[string]any, error) {
	cfg, _ := s.readConfig()
	cb := s.customBlobsStore()
	cs := s.customStratsStore()

	rows, err := cb.List()
	if err != nil {
		return nil, err
	}
	blobs := make([]customBlobDTO, len(rows))
	for i, row := range rows {
		refs, _ := cs.CountBlobRefs(row.Name)
		blobs[i] = customBlobDTO{
			ID:      row.ID,
			Name:    row.Name,
			File:    row.File,
			Comment: row.Comment,
			Refs:    refs,
		}
	}
	files, err := cb.FilesList()
	if err != nil || files == nil {
		files = []string{}
	}
	stock, err := cb.AllKnownNames(cfg)
	if err != nil || stock == nil {
		stock = []string{}
	}
	dirty, _ := cb.IsDirty(cfg)

	var sni any
	sniTitles := map[string]string{
		"1": "TCP 443 (YouTube)",
		"2": "TCP 443 (Googlevideo)",
		"3": "TCP 443 (RKN)",
		"4": "TCP 443 (Discord)",
		"8": "Fallback TLS",
	}
	if payload, sErr := s.store.BuildSNIPayload(s.paths.SNIZatorTxt(), s.paths.SNIUserTxt(), sniTitles); sErr == nil {
		sni = payload
	}

	return map[string]any{
		"dir":          filepath.ToSlash(cb.BlobDir()),
		"blobs":        blobs,
		"files":        files,
		"stock":        stock,
		"config_dirty": dirty,
		"sni":          sni,
	}, nil
}

func (s *Server) blobsGet(w http.ResponseWriter) {
	out, err := s.blobsListPayload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) blobsUpload(r *http.Request) (map[string]any, error) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if err := customblobs.ValidateBlobFileName(name); err != nil {
		return nil, errf(http.StatusBadRequest, err.Error())
	}
	if r.Body == nil {
		return nil, errf(http.StatusBadRequest, "Нет данных загрузки.")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, customblobs.MaxBlobSize+1))
	if err != nil {
		return nil, errf(http.StatusBadRequest, "Ошибка чтения загрузки: "+err.Error())
	}
	if len(data) > customblobs.MaxBlobSize {
		return nil, errf(http.StatusRequestEntityTooLarge, "Размер файла 1 Б…256 КиБ.")
	}
	if len(data) < customblobs.MinBlobSize {
		return nil, errf(http.StatusBadRequest, "Размер файла 1 Б…256 КиБ.")
	}

	cfg, _ := s.readConfig()
	cb := s.customBlobsStore()
	created, err := cb.Upload(name, data, cfg)
	if err != nil {
		return nil, mapCustomBlobsErr(err)
	}
	var createdID any
	if created != nil {
		createdID = created.ID
	}
	s.notifyTelemetry()
	return map[string]any{
		"ok":             true,
		"file":           name,
		"created":        createdID,
		"apply_required": true,
	}, nil
}

func (s *Server) blobsAction(r *http.Request, p params) (map[string]any, error) {
	action := strings.ToLower(p.Get("action"))
	cfg, _ := s.readConfig()
	cb := s.customBlobsStore()
	cs := s.customStratsStore()

	switch action {
	case "add":
		name := p.Get("value")
		if name == "" {
			name = p.Get("name")
		}
		entry, err := cb.Add(name, p.Get("file"), p.Get("comment"), cfg)
		if err != nil {
			return nil, mapCustomBlobsErr(err)
		}
		s.notifyTelemetry()
		return map[string]any{"ok": true, "id": entry.ID, "apply_required": true}, nil

	case "edit":
		id, err := strconv.Atoi(p.Get("id"))
		if err != nil || id <= 0 {
			return nil, errf(http.StatusBadRequest, "Некорректный ID")
		}
		cur, ok, err := cb.Get(id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errf(http.StatusBadRequest, "Нет записи "+strconv.Itoa(id))
		}
		name := p.Get("value")
		if name == "" {
			name = p.Get("name")
		}
		if name == "" {
			name = cur.Name
		}
		file := p.Get("file")
		if file == "" {
			file = cur.File
		}
		comment := cur.Comment
		if p.Has("comment") {
			comment = p.Get("comment")
		}
		_, renamed, err := cb.Edit(id, name, file, comment, cfg, cs)
		if err != nil {
			return nil, mapCustomBlobsErr(err)
		}
		s.notifyTelemetry()
		return map[string]any{"ok": true, "renamed": renamed, "apply_required": true}, nil

	case "delete":
		id, err := strconv.Atoi(p.Get("id"))
		if err != nil || id <= 0 {
			return nil, errf(http.StatusBadRequest, "Некорректный ID")
		}
		if err := cb.Delete(id, cs); err != nil {
			return nil, mapCustomBlobsErr(err)
		}
		s.notifyTelemetry()
		return map[string]any{"ok": true, "apply_required": true}, nil

	case "apply":
		var changed bool
		s.cfgMu.Lock()
		err := fsutil.WithDirLock(s.paths.ZapretConfig, 0, 0, func() error {
			live, err := config.Load(s.paths.ZapretConfig)
			if err != nil {
				return err
			}
			c1, err := cb.Apply(live)
			if err != nil {
				return err
			}
			res2, err := cs.Apply(live)
			if err != nil {
				return err
			}
			changed = c1 || res2.Changed
			if !changed {
				return nil
			}
			return fsutil.AtomicWriteFile(s.paths.ZapretConfig, []byte(live.String()), 0)
		})
		s.cfgMu.Unlock()
		if err != nil {
			return nil, errf(http.StatusInternalServerError, "Не удалось применить блобы к конфигу: "+err.Error())
		}
		restarted := s.maybeRestart(r.Context(), p)
		s.notifyTelemetry()
		return map[string]any{
			"ok":               true,
			"apply_required":   false,
			"restarted":        restarted,
			"restart_required": !restarted && changed,
		}, nil

	case "sni_pin", "sni_pool_assign", "sni_pool_custom", "sni_pool_clear", "sni_pool_policy", "sni_set_write", "sni_set_delete":
		return s.handleSNIAction(action, p)

	default:
		return nil, errf(http.StatusBadRequest, "Неизвестное действие: "+action)
	}
}

func (s *Server) handleSNIAction(action string, p params) (map[string]any, error) {
	requireProf := func() (string, error) {
		prof := p.Get("profile")
		if !orchestra.IsCloneProfile(prof) {
			return "", errf(http.StatusBadRequest, "Некорректный профиль: "+prof)
		}
		return prof, nil
	}

	switch action {
	case "sni_pin":
		prof, err := requireProf()
		if err != nil {
			return nil, err
		}
		val := strings.ToLower(p.Get("value"))
		if val == "" {
			if err := s.store.ClearOverride(orchestra.OverrideSNI, prof); err != nil {
				return nil, err
			}
		} else {
			if !orchestra.ValidSNIDomain(val) {
				return nil, errf(http.StatusBadRequest, "Некорректный домен: строчные буквы/цифры/точки/дефисы, до 254 символов.")
			}
			if err := s.store.SetOverride(orchestra.OverrideSNI, prof, val); err != nil {
				return nil, err
			}
		}

	case "sni_pool_assign":
		prof, err := requireProf()
		if err != nil {
			return nil, err
		}
		if err := s.store.AssignSNIPoolSet(prof, p.Get("value"), s.paths.SNIZatorTxt(), s.paths.SNIUserTxt()); err != nil {
			return nil, errf(http.StatusBadRequest, "Набор не найден или некорректен: "+err.Error())
		}

	case "sni_pool_custom":
		prof, err := requireProf()
		if err != nil {
			return nil, err
		}
		domains := strings.Fields(p.Raw("list"))
		if len(domains) == 0 {
			return nil, errf(http.StatusBadRequest, "Список пуст.")
		}
		if err := s.store.SetSNIPoolCustom(prof, domains); err != nil {
			return nil, errf(http.StatusBadRequest, "Некорректный домен в списке или больше 32 имён.")
		}

	case "sni_pool_clear":
		prof, err := requireProf()
		if err != nil {
			return nil, err
		}
		if err := s.store.ClearSNIPool(prof); err != nil {
			return nil, err
		}

	case "sni_pool_policy":
		prof, err := requireProf()
		if err != nil {
			return nil, err
		}
		if err := s.store.SetSNIPoolPolicy(prof, strings.ToLower(p.Get("value"))); err != nil {
			return nil, errf(http.StatusBadRequest, "Политика: rr (по кругу) или rand (случайно без повторов).")
		}

	case "sni_set_write":
		domains := strings.Fields(p.Raw("list"))
		if len(domains) == 0 {
			return nil, errf(http.StatusBadRequest, "Список пуст.")
		}
		if err := s.store.WriteUserSNISet(s.paths.SNIUserTxt(), p.Get("name"), domains); err != nil {
			return nil, errf(http.StatusBadRequest, "Некорректное имя набора или домен в списке (до 32 имён).")
		}

	case "sni_set_delete":
		if err := s.store.DeleteUserSNISet(s.paths.SNIZatorTxt(), s.paths.SNIUserTxt(), p.Get("name")); err != nil {
			switch {
			case errors.Is(err, orchestra.ErrSNISetProtected), errors.Is(err, orchestra.ErrSNISetInUse), errors.Is(err, orchestra.ErrSNISetNotFound):
				return nil, errf(http.StatusBadRequest, err.Error())
			default:
				return nil, err
			}
		}
	}

	s.notifyTelemetry()
	return map[string]any{"ok": true, "restarted": false, "restart_required": false}, nil
}

func mapCustomBlobsErr(err error) error {
	switch {
	case errors.Is(err, customblobs.ErrTooLarge):
		return errf(http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, customblobs.ErrInvalid),
		errors.Is(err, customblobs.ErrNotFound),
		errors.Is(err, customblobs.ErrNameTaken),
		errors.Is(err, customblobs.ErrBlobInUse):
		return errf(http.StatusBadRequest, err.Error())
	default:
		return err
	}
}

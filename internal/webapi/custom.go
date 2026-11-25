package webapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/customstrats"
)

type customBlockDTO struct {
	Block     string `json:"block"`
	Label     string `json:"label"`
	Profiles  string `json:"profiles"`
	Transport string `json:"transport"`
}

type customStrategyDTO struct {
	ID       int    `json:"id"`
	Block    string `json:"block"`
	Spec     string `json:"spec"`
	Enabled  bool   `json:"enabled"`
	Comment  string `json:"comment"`
	Assigned string `json:"assigned"`
}

func (s *Server) customStratsStore() *customstrats.Store {
	return customstrats.NewStore(
		s.paths.CustomStrategiesTSV(),
		s.paths.CustomBlobsTSV(),
		s.store,
		s.paths.AutoLockedTSV,
	)
}

func joinIntSlice(nums []int) string {
	if len(nums) == 0 {
		return ""
	}
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, " ")
}

func (s *Server) handleCustom(w http.ResponseWriter, r *http.Request, p params) {
	if !requireMethod(w, r, http.MethodGet, http.MethodPost) {
		return
	}
	if r.Method == http.MethodGet {
		s.customListGet(w, p)
		return
	}
	out, err := s.customPost(r, p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) customListPayload(filter string) (map[string]any, error) {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter != "" {
		if _, ok := customstrats.LookupBlock(filter); !ok {
			return nil, errf(http.StatusBadRequest, "Некорректный блок: "+filter)
		}
	}
	st := s.customStratsStore()
	rows, err := st.List(filter)
	if err != nil {
		return nil, err
	}
	rawBlocks := customstrats.Blocks()
	blocks := make([]customBlockDTO, len(rawBlocks))
	for i, b := range rawBlocks {
		blocks[i] = customBlockDTO{
			Block:     b.Block,
			Label:     b.Label,
			Profiles:  joinIntSlice(b.Profiles),
			Transport: b.Transport,
		}
	}
	strats := make([]customStrategyDTO, len(rows))
	for i, e := range rows {
		asg := ""
		if e.Assigned > 0 {
			asg = strconv.Itoa(e.Assigned)
		}
		strats[i] = customStrategyDTO{
			ID:       e.ID,
			Block:    e.Block,
			Spec:     e.Spec,
			Enabled:  e.Enabled,
			Comment:  e.Comment,
			Assigned: asg,
		}
	}
	return map[string]any{
		"blocks":     blocks,
		"strategies": strats,
	}, nil
}

func (s *Server) customListGet(w http.ResponseWriter, p params) {
	out, err := s.customListPayload(p.Get("block"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func extractSingleSpecInput(raw string) string {
	joined := strings.TrimSpace(customstrats.JoinWraps(raw))
	if joined == "" {
		return strings.TrimSpace(raw)
	}
	// Берём первую непустую запись, если пользователь вставил вывод blockcheck2 / команду nfqws2.
	for _, ln := range strings.Split(joined, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if _, args, ok := strings.Cut(t, " : nfqws2 "); ok {
			t = args
		} else if strings.HasPrefix(t, "nfqws2 ") {
			t = strings.TrimPrefix(t, "nfqws2 ")
		}
		if strings.Contains(t, "--lua-desync=") {
			var groups []string
			for _, tok := range strings.Fields(t) {
				if strings.HasPrefix(tok, "--lua-desync=") {
					groups = append(groups, tok)
				}
			}
			if len(groups) > 0 {
				return strings.Join(groups, " ")
			}
		}
		return t
	}
	return strings.TrimSpace(raw)
}

func (s *Server) customPost(r *http.Request, p params) (map[string]any, error) {
	action := strings.ToLower(p.Get("action"))
	st := s.customStratsStore()
	cfg, _ := s.readConfig()

	switch action {
	case "add":
		block := strings.ToLower(p.Get("block"))
		if _, ok := customstrats.LookupBlock(block); !ok {
			return nil, errf(http.StatusBadRequest, "Некорректный блок: "+block)
		}
		rawVal := p.Raw("value")
		if strings.TrimSpace(rawVal) == "" {
			rawVal = p.Raw("spec")
		}
		entry, err := st.Add(block, extractSingleSpecInput(rawVal), p.Get("comment"), cfg)
		if err != nil {
			return nil, mapCustomStratsErr(err)
		}
		s.notifyTelemetry()
		return map[string]any{"ok": true, "id": entry.ID, "apply_required": true}, nil

	case "edit":
		block := strings.ToLower(p.Get("block"))
		if _, ok := customstrats.LookupBlock(block); !ok {
			return nil, errf(http.StatusBadRequest, "Некорректный блок: "+block)
		}
		id, err := strconv.Atoi(p.Get("id"))
		if err != nil || id <= 0 {
			return nil, errf(http.StatusBadRequest, "Некорректный ID")
		}
		cur, ok, err := st.Get(id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errf(http.StatusBadRequest, "Нет записи "+strconv.Itoa(id))
		}
		enabled := cur.Enabled
		if ev := p.Get("enabled"); ev != "" {
			enabled = ev == "1" || strings.EqualFold(ev, "true")
		}
		rawVal := p.Raw("value")
		if strings.TrimSpace(rawVal) == "" {
			rawVal = p.Raw("spec")
		}
		comment := cur.Comment
		if p.Has("comment") {
			comment = p.Get("comment")
		}
		if _, err := st.Edit(id, block, extractSingleSpecInput(rawVal), enabled, comment, cfg); err != nil {
			return nil, mapCustomStratsErr(err)
		}
		s.notifyTelemetry()
		return map[string]any{"ok": true, "apply_required": true}, nil

	case "enable":
		id, err := strconv.Atoi(p.Get("id"))
		if err != nil || id <= 0 {
			return nil, errf(http.StatusBadRequest, "Некорректный ID")
		}
		ev := strings.ToLower(p.Get("value"))
		if ev == "" {
			ev = strings.ToLower(p.Get("enabled"))
		}
		var enabled bool
		switch ev {
		case "1", "true":
			enabled = true
		case "0", "false":
			enabled = false
		default:
			return nil, errf(http.StatusBadRequest, "enabled = 0 или 1")
		}
		if err := st.SetEnabled(id, enabled); err != nil {
			return nil, mapCustomStratsErr(err)
		}
		s.notifyTelemetry()
		return map[string]any{"ok": true, "apply_required": true}, nil

	case "delete":
		id, err := strconv.Atoi(p.Get("id"))
		if err != nil || id <= 0 {
			return nil, errf(http.StatusBadRequest, "Некорректный ID")
		}
		if err := st.Delete(id); err != nil {
			return nil, mapCustomStratsErr(err)
		}
		s.notifyTelemetry()
		return map[string]any{"ok": true, "apply_required": true}, nil

	case "import":
		block := strings.ToLower(p.Get("block"))
		if _, ok := customstrats.LookupBlock(block); !ok {
			return nil, errf(http.StatusBadRequest, "Некорректный блок: "+block)
		}
		text := p.Raw("text")
		if strings.TrimSpace(text) == "" {
			text = p.Raw("value")
		}
		if strings.TrimSpace(text) == "" {
			return nil, errf(http.StatusBadRequest, "Пустой импорт")
		}
		lines := strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
		total := len(lines)
		if len(lines) > customstrats.MaxEntries {
			lines = lines[:customstrats.MaxEntries]
		}
		res, err := st.Import(block, strings.Join(lines, "\n"), cfg)
		if err != nil {
			return nil, mapCustomStratsErr(err)
		}
		s.notifyTelemetry()
		return map[string]any{
			"ok":             true,
			"added":          res.Added,
			"dups":           res.Dups,
			"skipped":        res.Dups + res.Failed,
			"failed":         res.Failed,
			"errors":         res.Errors,
			"notes":          res.Notes,
			"total":          total,
			"limit":          customstrats.MaxEntries,
			"apply_required": res.Added > 0,
		}, nil

	case "apply":
		s.cfgMu.Lock()
		res, err := st.ApplyFile(s.paths.ZapretConfig)
		s.cfgMu.Unlock()
		if err != nil {
			if errors.Is(err, config.ErrNotFound) {
				return nil, errf(http.StatusInternalServerError, "Не найден конфиг zapret2")
			}
			return nil, errf(http.StatusInternalServerError, "Не удалось применить стратегии: "+err.Error())
		}
		restarted := s.maybeRestart(r.Context(), p)
		s.notifyTelemetry()
		return map[string]any{
			"ok":               true,
			"apply_required":   false,
			"restarted":        restarted,
			"restart_required": !restarted && res.Changed,
			"moved":            res.Moved,
			"dropped":          res.Dropped,
		}, nil

	default:
		return nil, errf(http.StatusBadRequest, "Неизвестное действие: "+action)
	}
}

func mapCustomStratsErr(err error) error {
	switch {
	case errors.Is(err, customstrats.ErrInvalid),
		errors.Is(err, customstrats.ErrNotFound),
		errors.Is(err, customstrats.ErrLimitReached):
		return errf(http.StatusBadRequest, err.Error())
	default:
		return err
	}
}

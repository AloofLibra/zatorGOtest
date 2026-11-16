package webapi

import (
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator/internal/firewall"
	"github.com/AloofLibra/zator/internal/fsutil"
)

type clientScopeMapping struct {
	Scope string `json:"scope"`
	IP    string `json:"ip"`
}

func (s *Server) handleClientScopes(w http.ResponseWriter, r *http.Request, p params) {
	if r.Method == http.MethodGet {
		cfg, installed := s.readConfig()
		cc, err := firewall.ClientScopeConfigFromGetter(cfg.Get, s.paths.ZatorRoot)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Некорректная конфигурация client scopes: "+err.Error())
			return
		}
		if !installed {
			writeJSON(w, http.StatusOK, map[string]any{"mappings": []clientScopeMapping{}, "max_scope": cc.Max})
			return
		}
		mapPath, err := clientScopeMapPath(cc.MapFile, s.paths.ZatorRoot)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		f, err := os.Open(mapPath)
		if err != nil && !os.IsNotExist(err) {
			writeError(w, http.StatusInternalServerError, "Не удалось прочитать client_scope.tsv: "+err.Error())
			return
		}
		var mappings []clientScopeMapping
		if err == nil {
			defer f.Close()
			parseConfig := cc
			if parseConfig.Mask == 0 {
				parseConfig.Mask = ^uint32(0)
			}
			entries, _, parseErr := parseConfig.ParseTSV(f)
			if parseErr != nil {
				writeError(w, http.StatusInternalServerError, "Не удалось разобрать client_scope.tsv: "+parseErr.Error())
				return
			}
			for _, entry := range entries {
				mappings = append(mappings, clientScopeMapping{Scope: fmt.Sprintf("mark:%d", entry.ID), IP: entry.Addr.String()})
			}
		}
		if mappings == nil {
			mappings = []clientScopeMapping{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"mappings": mappings, "max_scope": cc.Max})
		return
	}
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if p.Get("action") != "add" && p.Get("action") != "remove" {
		writeError(w, http.StatusBadRequest, "Неизвестное действие client scopes")
		return
	}
	cfg, installed := s.readConfig()
	if !installed {
		writeError(w, http.StatusServiceUnavailable, "zapret2 не установлен")
		return
	}
	cc, err := firewall.ClientScopeConfigFromGetter(cfg.Get, s.paths.ZatorRoot)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Некорректная конфигурация client scopes: "+err.Error())
		return
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(p.Get("ip")))
	if err != nil || addr.Zone() != "" {
		writeError(w, http.StatusBadRequest, "Некорректный IP-адрес")
		return
	}
	addr = addr.Unmap()
	path, err := clientScopeMapPath(cc.MapFile, s.paths.ZatorRoot)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if p.Get("action") == "add" {
		n, err := strconv.ParseUint(strings.TrimPrefix(p.Get("scope"), "mark:"), 10, 32)
		if err != nil || p.Get("scope") != fmt.Sprintf("mark:%d", n) {
			writeError(w, http.StatusBadRequest, "Scope должен иметь вид mark:1…")
			return
		}
		if n == 0 || n > uint64(cc.Max) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("Scope ID должен быть от 1 до %d", cc.Max))
			return
		}
		if cc.Mask != 0 {
			if _, err := cc.MarkFor(uint32(n)); err != nil {
				writeError(w, http.StatusBadRequest, "Некорректный scope: "+err.Error())
				return
			}
		}
	}

	s.listMu.Lock()
	err = mutateClientScopeMap(path, p.Get("action"), addr, p.Get("scope"))
	s.listMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restarted": s.maybeRestart(r.Context(), p)})
}

func mutateClientScopeMap(path, action string, addr netip.Addr, scope string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("не удалось прочитать client_scope.tsv: %w", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(raw) == 0 {
		lines = nil
	}
	kept := make([]string, 0, len(lines)+1)
	exists := false
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) >= 2 {
			oldIP, parseErr := netip.ParseAddr(strings.TrimSpace(fields[1]))
			if parseErr == nil && oldIP.Zone() == "" && oldIP.Unmap() == addr {
				if action == "remove" || fields[0] != scope {
					continue
				}
				exists = true
			}
		}
		kept = append(kept, line)
	}
	if action == "add" && !exists {
		kept = append(kept, scope+"\t"+addr.String())
	}
	data := ""
	if len(kept) > 0 {
		data = strings.Join(kept, "\n") + "\n"
	}
	if err := fsutil.AtomicWriteFile(path, []byte(data), 0o644); err != nil {
		return fmt.Errorf("не удалось сохранить client_scope.tsv: %w", err)
	}
	return nil
}

func clientScopeMapPath(file, root string) (string, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", err
	}
	parentReal, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("каталог client_scope.tsv недоступен: %w", err)
	}
	rel, err := filepath.Rel(rootReal, parentReal)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("файл client_scope.tsv должен находиться внутри %s", rootReal)
	}
	return filepath.Join(parentReal, filepath.Base(abs)), nil
}

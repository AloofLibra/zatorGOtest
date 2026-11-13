package webapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AloofLibra/zator/internal/deploy"
	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/service"
)

// validateConfigRawShellSafety отклоняет очевидный shell breakout в zapret2/config
// (source'ится shell-скриптами). Не парсер shell — практичный фильтр.
func validateConfigRawShellSafety(content string) error {
	for _, line := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.Contains(trim, "$(") || strings.Contains(trim, "`") {
			return fmt.Errorf("запрещены подстановки $(...) и backtick в config")
		}
		if hasUnquotedShellMeta(trim) {
			return fmt.Errorf("опасные shell-метасимволы (;|&) вне кавычек")
		}
	}
	return nil
}

func hasUnquotedShellMeta(line string) bool {
	inDQ, inSQ, esc := false, false, false
	for _, r := range line {
		if esc {
			esc = false
			continue
		}
		if r == '\\' && inDQ {
			esc = true
			continue
		}
		if r == '"' && !inSQ {
			inDQ = !inDQ
			continue
		}
		if r == '\'' && !inDQ {
			inSQ = !inSQ
			continue
		}
		if inDQ || inSQ {
			continue
		}
		switch r {
		case ';', '|', '&', '\n', '\r':
			return true
		}
	}
	return false
}

const (
	maxConfigRawBytes = 2 << 20 // 2 MiB
	maxErrLogBytes    = 256 << 10
)

// configRawGet — GET settings.cgi?setting=config_raw (п.7 CLI).
func (s *Server) configRawGet() map[string]any {
	path := s.paths.ZapretConfig
	out := map[string]any{
		"path":    path,
		"content": "",
		"exists":  false,
		"size":    0,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			out["error"] = err.Error()
		}
		return out
	}
	out["exists"] = true
	out["size"] = len(data)
	out["content"] = string(data)
	return out
}

func (s *Server) postConfigRaw(r *http.Request, p params) (map[string]any, error) {
	// Raw — без TrimSpace, иначе съедаются края многострочного config.
	content := p.Raw("value")
	if strings.TrimSpace(content) == "" {
		return nil, errf(http.StatusBadRequest, "Пустой конфиг")
	}
	if len(content) > maxConfigRawBytes {
		return nil, errf(http.StatusBadRequest, "Конфиг слишком большой")
	}
	if !utf8.ValidString(content) {
		return nil, errf(http.StatusBadRequest, "Конфиг должен быть UTF-8 текстом")
	}
	if !strings.Contains(content, "NFQWS2_OPT") {
		return nil, errf(http.StatusBadRequest, "В конфиге нет NFQWS2_OPT — отказ в записи")
	}
	if err := validateConfigRawShellSafety(content); err != nil {
		return nil, errf(http.StatusBadRequest, err.Error())
	}
	path := s.paths.ZapretConfig
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := fsutil.AtomicWriteFile(path, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "restarted": s.maybeRestart(r.Context(), p), "size": len(content)}, nil
}

// nfqws2ErrorsGet — журнал ошибок последнего запуска (п.666 CLI).
func (s *Server) nfqws2ErrorsGet() map[string]any {
	candidates := []string{
		"/tmp/nfqws2_1.err",
		"/opt/tmp/nfqws2_1.err",
		filepath.Join(os.TempDir(), "nfqws2_1.err"),
	}
	out := map[string]any{"available": false, "path": "", "content": "", "lines": 0}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if len(data) > maxErrLogBytes {
			data = data[len(data)-maxErrLogBytes:]
		}
		text := string(data)
		n := 0
		for _, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) != "" {
				n++
			}
		}
		out["available"] = true
		out["path"] = p
		out["content"] = text
		out["lines"] = n
		return out
	}
	return out
}

func (s *Server) postUpdateApply(r *http.Request, _ params) (map[string]any, error) {
	u := deploy.New(s.paths)
	if deploy.DetectOffline(os.Getenv, "").Offline {
		u.Offline = true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	rep, err := u.Run(ctx, deploy.RunOptions{Variant: deploy.VariantFull})
	if err != nil {
		return nil, errf(http.StatusBadGateway, "Обновление не удалось: "+err.Error())
	}
	out := map[string]any{"ok": true, "up_to_date": false, "installed": 0, "restart_required": false}
	installed := 0
	upToDate := false
	if rep != nil {
		upToDate = rep.UpToDate
		out["up_to_date"] = upToDate
		if rep.Apply != nil {
			installed = rep.Apply.Installed
			out["installed"] = installed
		}
		if rep.Check != nil && rep.Check.Latest != nil {
			out["release"] = rep.Check.Latest.Release
		}
	}
	if installed > 0 && !upToDate {
		out["restart_required"] = true
		s.schedulePostUpdateRestarts()
		out["webui_restart_scheduled"] = true
		out["zapret2_restart_scheduled"] = true
	}
	return out, nil
}

// schedulePostUpdateRestarts — отложенный рестарт webui + zapret2 (новый Lua в nfqws2).
func (s *Server) schedulePostUpdateRestarts() {
	if s.svc == nil {
		return
	}
	go func() {
		time.Sleep(1500 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = s.svc.Restart(ctx, service.UnitZapret2)
		_ = s.svc.Restart(ctx, service.UnitValidator)
		_ = s.svc.Restart(ctx, service.UnitWebUI)
	}()
}

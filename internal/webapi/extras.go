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
	out := map[string]any{"ok": true, "up_to_date": false, "installed": 0, "restart_required": false}
	mode := strings.TrimSpace(r.FormValue("mode"))
	if mode == "" {
		mode = deploy.ModeApp
	}
	preCtx, preCancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer preCancel()
	u := deploy.New(s.paths)
	off := deploy.ResolveOfflineBundle(os.Getenv)
	var check *deploy.CheckResult
	var checkErr error
	if !off.Offline {
		check, checkErr = u.CheckLatest(preCtx)
	}
	if checkErr != nil && check == nil {
		return nil, errf(http.StatusBadGateway, "Не удалось проверить обновление: "+checkErr.Error())
	}
	var latest *deploy.Latest
	if check != nil {
		latest = check.Latest
	}
	preflight, err := deploy.CheckPreflight(s.paths, latest, mode)
	if err != nil {
		return nil, errf(http.StatusInternalServerError, "Проверка места: "+err.Error())
	}
	out["preflight"] = preflight
	if !preflight.CanUpdate {
		return nil, errf(http.StatusInsufficientStorage, fmt.Sprintf("Недостаточно flash (Zator %d/%d, zapret2 %d/%d байт свободно/нужно)", preflight.FlashAvailable, preflight.FlashRequired, preflight.ZapretFlashAvailable, preflight.ZapretFlashRequired))
	}
	if !preflight.CanProceedWithoutBackup {
		return nil, errf(http.StatusInsufficientStorage, fmt.Sprintf("Недостаточно /tmp даже для загрузки и распаковки обновления: свободно %d байт, ориентировочно нужно %d", preflight.TmpAvailable, preflight.TmpBaseRequired))
	}
	if !preflight.CanRollback && r.FormValue("continue_without_backup") != "1" {
		return map[string]any{"ok": false, "needs_confirmation": true, "warning": "В /tmp недостаточно места для резервной копии. Можно продолжить без отката на свой риск.", "preflight": preflight}, nil
	}
	rollbackAvailable := false
	// Создать полный pre-update backup до Apply; при ошибке обновление не начинать.
	if s.app != nil && preflight.CanRollback {
		info, err := s.app.CreatePreUpdateBackup()
		if err != nil {
			return nil, errf(http.StatusInternalServerError, "Обновление отменено: pre-update backup не создан: "+err.Error())
		}
		out["pre_update_backup"] = info.Name
		rollbackAvailable = true
	} else if s.app != nil {
		out["rollback_available"] = false
		out["warning"] = "Обновление выполняется без резервной копии; откат может быть невозможен."
	}
	rollbackFailure := func(status int, message string, cause error) (map[string]any, error) {
		detail := message + cause.Error()
		if s.app != nil && rollbackAvailable {
			if rerr := s.app.RollbackPreUpdateBackup(); rerr != nil {
				detail += "; rollback: " + rerr.Error()
				s.app.WriteServiceWarnings([]string{cause.Error(), "rollback: " + rerr.Error()})
			} else {
				detail += "; откат из pre-update backup выполнен"
			}
		}
		return nil, errf(status, detail)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	var (
		installed int
		upToDate  bool
	)
	if off.Offline && off.Bundle != nil {
		out["offline"] = true
		out["offline_reason"] = off.Reason
		ar, aerr := u.ApplyBundleMode(ctx, *off.Bundle, mode)
		if aerr != nil {
			return rollbackFailure(http.StatusBadGateway, "Офлайн-обновление не удалось: ", aerr)
		}
		if ar != nil {
			installed = ar.Installed
			out["installed"] = installed
		}
		out["up_to_date"] = false
	} else {
		if off.Offline {
			u.Offline = true
		}
		rep, rerr := u.Run(ctx, deploy.RunOptions{Mode: mode})
		if rerr != nil {
			return rollbackFailure(http.StatusBadGateway, "Обновление не удалось: ", rerr)
		}
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
	}
	if installed > 0 && !upToDate {
		if s.app != nil && mode != deploy.ModeConfig {
			if err := s.app.EnsureZatorCommand(); err != nil {
				out["command_path_warning"] = err.Error()
			}
		}
		verifyOK := true
		if s.app != nil && mode != deploy.ModeConfig {
			if err := s.app.VerifyAfterUpdate(); err != nil {
				verifyOK = false
				out["post_update_verify"] = "fail"
				out["post_update_verify_detail"] = err.Error()
				if !rollbackAvailable {
					out["rollback"] = "unavailable"
					out["restart_skipped"] = "post-update verify failed; backup was unavailable"
				} else if rerr := s.app.RollbackPreUpdateBackup(); rerr != nil {
					out["rollback"] = "fail"
					out["rollback_detail"] = rerr.Error()
					s.app.WriteServiceWarnings([]string{err.Error(), "rollback: " + rerr.Error()})
				} else {
					out["rollback"] = "ok"
					out["post_update_verify"] = "ok"
					out["post_update_verify_detail"] = "restored from pre-update backup"
					verifyOK = true
				}
			} else {
				out["post_update_verify"] = "ok"
			}
		}
		if verifyOK {
			out["restart_required"] = true
			s.schedulePostUpdateRestarts(mode != deploy.ModeConfig)
			out["webui_restart_scheduled"] = mode != deploy.ModeConfig
			out["zapret2_restart_scheduled"] = true
		} else {
			out["restart_required"] = false
			out["restart_skipped"] = "post-update verify failed — рестарт отложен; откат из pre_update_backup не удался"
		}
	}
	return out, nil
}

func (s *Server) postUpdatePreflight(r *http.Request, p params) (map[string]any, error) {
	mode := strings.TrimSpace(p.Get("mode"))
	if mode == "" {
		mode = deploy.ModeApp
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	u := deploy.New(s.paths)
	off := deploy.ResolveOfflineBundle(os.Getenv)
	var check *deploy.CheckResult
	var err error
	if !off.Offline {
		check, err = u.CheckLatest(ctx)
	}
	if err != nil && check == nil {
		return nil, errf(http.StatusBadGateway, "Проверка релиза: "+err.Error())
	}
	var latest *deploy.Latest
	if check != nil {
		latest = check.Latest
	}
	report, err := deploy.CheckPreflight(s.paths, latest, mode)
	if err != nil {
		return nil, errf(http.StatusBadRequest, err.Error())
	}
	return map[string]any{"ok": true, "preflight": report}, nil
}

// schedulePostUpdateRestarts — отложенный рестарт webui + zapret2 (новый Lua в nfqws2).
func (s *Server) schedulePostUpdateRestarts(restartWebUI bool) {
	if s.svc == nil {
		return
	}
	go func() {
		time.Sleep(1500 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if s.app != nil {
			s.runtimeMu.Lock()
			if err := s.app.RestartZapretTransactional(ctx, func(ctx context.Context) error {
				return s.svc.Restart(ctx, service.UnitZapret2)
			}); err != nil {
				s.app.WriteServiceWarnings([]string{"post-update restart: " + err.Error()})
			} else {
				var warns []string
				if st, d := s.app.FwEnsureStatus(); st == "fail" && d != "" {
					warns = append(warns, "fw ensure: "+d)
				}
				s.app.StartNfqwsWatchdog()
				if len(warns) > 0 {
					s.app.WriteServiceWarnings(warns)
				} else {
					s.app.ClearServiceWarnings()
				}
			}
			s.runtimeMu.Unlock()
		} else {
			_ = s.svc.Restart(ctx, service.UnitZapret2)
		}
		_ = s.svc.Restart(ctx, service.UnitValidator)
		if restartWebUI {
			_ = s.svc.Restart(ctx, service.UnitWebUI)
		}
		if restartWebUI && s.app != nil {
			s.app.StartWebUIWatchdog()
		}
	}()
}

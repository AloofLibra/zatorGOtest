package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/AloofLibra/zator"
	"github.com/AloofLibra/zator/internal/app"
	"github.com/AloofLibra/zator/internal/backup"
	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/deploy"
	"github.com/AloofLibra/zator/internal/orchestra"
	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/service"
	"github.com/AloofLibra/zator/internal/sweep"
	"github.com/AloofLibra/zator/internal/webapi"
	"golang.org/x/term"
)

// menu — намеренно последовательный текстовый интерфейс: он не опрашивает
// клавиатуру и использует те же команды, что и неинтерактивный CLI.
func runMenu(input io.Reader, output, stderr io.Writer) int {
	r := bufio.NewReader(input)
	for {
		fmt.Fprintln(output, menuColor("36;1", "\n=== ZATOR GO ==="))
		fmt.Fprintf(output, "Версия: %s\n", zator.Version())
		if a, err := app.Load(); err == nil {
			fmt.Fprintf(output, "Платформа: %s | Установка: %s\n", a.Platform, map[bool]string{true: "установлен", false: "не установлен"}[a.Installed()])
			fmt.Fprintf(output, "Обновления: %s\n", menuColor("33", menuCachedUpdates(a.Paths, a.Installed())))
		}
		fmt.Fprintln(output, "\n 1. Статус и диагностика\n 2. Стратегии\n 3. Supersweep\n 4. Домены и списки\n 5. Настройки обхода\n 6. Службы\n 7. Обновление\n 8. Резервные копии\n 9. Дополнительные инструменты\n 0. Выход")
		choice, err := menuRead(r, output, "Выберите пункт: ")
		if err != nil {
			if err == io.EOF {
				return 0
			}
			fmt.Fprintln(stderr, err)
			return 1
		}
		var code int
		switch choice {
		case "0":
			if menuExit(r, output, stderr) {
				return 0
			}
		case "1":
			code = menuStatus(r, output, stderr)
		case "2":
			code = menuStrategies(r, output, stderr)
		case "3":
			code = menuSweep(r, output, stderr)
		case "4":
			code = menuDomains(r, output, stderr)
		case "5":
			code = menuSettings(r, output, stderr)
		case "6":
			code = menuServices(r, output, stderr)
		case "7":
			code = menuUpdate(r, output, stderr)
		case "8":
			code = menuBackups(r, output, stderr)
		case "9":
			code = menuTools(r, output, stderr)
		default:
			fmt.Fprintln(output, "Введите номер от 0 до 9.")
		}
		if code != 0 {
			fmt.Fprintf(stderr, "Команда завершилась с кодом %d.\n", code)
		}
		if _, err := menuRead(r, output, "Нажмите Enter для возврата в меню..."); err == io.EOF {
			return 0
		}
	}
}

func menuCachedUpdates(p paths.Paths, installed bool) string {
	cache := deploy.LatestCachePath(p)
	info, err := os.Stat(cache)
	if err != nil {
		return "не проверялись"
	}
	raw, err := os.ReadFile(cache)
	if err != nil {
		return "кэш недоступен"
	}
	latest, err := deploy.ParseLatest(raw)
	if err != nil {
		return "кэш повреждён"
	}
	current, err := deploy.ReadVersion(p)
	if err != nil {
		return "кэш есть, текущая версия неизвестна"
	}
	result := deploy.CompareLatest(current, latest, installed)
	state := "актуально по кэшу"
	if result.UpdateAvailable() {
		state = "доступно по кэшу"
	}
	return fmt.Sprintf("%s (кэш от %s)", state, info.ModTime().Format("2006-01-02 15:04"))
}

func menuRead(r *bufio.Reader, w io.Writer, prompt string) (string, error) {
	fmt.Fprint(w, prompt)
	s, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	if err == io.EOF && s == "" {
		return "", io.EOF
	}
	return strings.TrimSpace(s), nil
}

func menuStatus(r *bufio.Reader, out, errw io.Writer) int {
	code := cmdStatus(out, errw)
	fmt.Fprintln(out, "\n 1. Обновить статус\n 2. Ошибки nfqws2 и предупреждения служб\n 3. Проверить доступность\n 4. Службы\n 0. Назад")
	c, err := menuRead(r, out, "Выберите пункт: ")
	if err != nil {
		return 0
	}
	switch c {
	case "0":
		return code
	case "1":
		return cmdStatus(out, errw)
	case "2":
		return cmdErrors(nil, out, errw)
	case "3":
		s, err := menuServer()
		if err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res, err := s.CLIReachability(ctx)
		return menuResult(out, errw, res, err)
	case "4":
		return menuServices(r, out, errw)
	default:
		fmt.Fprintln(out, "Некорректный выбор.")
	}
	return code
}

func menuStrategies(r *bufio.Reader, out, errw io.Writer) int {
	scope, err := menuRead(r, out, "Scope (default или mark:N, Enter — default): ")
	if err != nil {
		return 0
	}
	if scope == "" {
		scope = orchestra.ScopeDefault
	}
	a, err := app.Load()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	cfg, err := config.Load(a.Paths.ZapretConfig)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	store := orchestra.NewStore(a.Paths.StateOrchestraDir())
	fmt.Fprintf(out, "Ручные фиксации: %s\n", map[bool]string{true: "есть", false: "нет"}[store.HasLocks()])
	srv, err := menuServer()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	for _, p := range cfg.Profiles() {
		lock, source, err := srv.CLIProfileLockLabel(scope, p.ID)
		if err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		fmt.Fprintf(out, " %2d. %-18s стратегия %s (%s, макс. %d)\n", p.ID, p.Label, lock, source, cfg.ProfileMaxStrategyOrDefault(p.ID))
	}
	fmt.Fprintln(out, "\n 1. Зафиксировать стратегию / выключить профиль (0)\n 2. Снять фиксацию (auto)\n 3. Проверить профиль\n 0. Назад")
	c, e := menuRead(r, out, "Выберите пункт: ")
	if e != nil || c == "0" {
		return 0
	}
	idText, e := menuRead(r, out, "Номер профиля: ")
	if e != nil {
		return 0
	}
	id, e := strconv.Atoi(idText)
	if e != nil || !config.ValidProfile(id) {
		fmt.Fprintln(errw, "Некорректный профиль.")
		return 1
	}
	switch c {
	case "1":
		raw, e := menuRead(r, out, fmt.Sprintf("Стратегия 0..%d (0 = выключить профиль): ", cfg.ProfileMaxStrategyOrDefault(id)))
		if e != nil {
			return 0
		}
		n, e := strconv.Atoi(raw)
		if e != nil || n < 0 || n > cfg.ProfileMaxStrategyOrDefault(id) {
			fmt.Fprintln(errw, "Номер стратегии вне допустимого диапазона.")
			return 1
		}
		if n == 0 {
			ok, e := menuConfirm(r, out, "Отключить профиль?")
			if e != nil || !ok {
				return 0
			}
		}
		res, err := srv.CLISetProfileLock(context.Background(), scope, id, &n)
		return menuResult(out, errw, res, err)
	case "2":
		ok, e := menuConfirm(r, out, "Снять фиксацию и вернуть профиль в auto?")
		if e != nil || !ok {
			return 0
		}
		res, err := srv.CLISetProfileLock(context.Background(), scope, id, nil)
		return menuResult(out, errw, res, err)
	case "3":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res, err := srv.CLIProfileCheck(ctx, id)
		return menuResult(out, errw, res, err)
	default:
		fmt.Fprintln(out, "Введите 1, 2, 3 или 0.")
		return 0
	}
}

func menuServices(r *bufio.Reader, out, errw io.Writer) int {
	code := cmdService([]string{"status"}, out, errw)
	fmt.Fprintln(out, "\n 1. Запустить службу\n 2. Остановить службу\n 3. Перезапустить службу\n 4. Предупреждения\n 0. Назад")
	c, err := menuRead(r, out, "Выберите пункт: ")
	if err != nil {
		return code
	}
	switch c {
	case "0":
		return code
	case "1", "2", "3":
		fmt.Fprintf(out, "Службы: 1 zapret2, 2 %s, 3 %s\n", service.UnitValidator, service.UnitWebUI)
		unitChoice, e := menuRead(r, out, "Номер службы: ")
		if e != nil {
			return 0
		}
		units := map[string]string{"1": "zapret2", "2": string(service.UnitValidator), "3": string(service.UnitWebUI)}
		unit, ok := units[unitChoice]
		if !ok {
			fmt.Fprintln(errw, "Некорректная служба.")
			return 1
		}
		action := map[string]string{"1": "start", "2": "stop", "3": "restart"}[c]
		if action != "start" {
			ok, e := menuConfirm(r, out, fmt.Sprintf("%s %s?", map[string]string{"stop": "Остановить", "restart": "Перезапустить"}[action], unit))
			if e != nil || !ok {
				return 0
			}
		}
		return cmdService([]string{action, unit}, out, errw)
	case "4":
		return cmdService([]string{"warnings"}, out, errw)
	default:
		fmt.Fprintln(out, "Некорректный выбор.")
	}
	return code
}

func menuBackups(r *bufio.Reader, out, errw io.Writer) int {
	a, err := app.Load()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	m := backup.NewIn(a.Paths, a.Paths.BackupDir)
	items, err := m.List()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	fmt.Fprintf(out, "Резервные копии (%d):\n", len(items))
	for _, item := range items {
		fmt.Fprintf(out, " %s  %d байт  %s\n", item.Name, item.Size, item.ModTime.Format("2006-01-02 15:04"))
	}
	fmt.Fprintln(out, "\n 1. Создать резервную копию\n 2. Smart-восстановление\n 3. Откат последнего обновления\n 0. Назад")
	c, err := menuRead(r, out, "Выберите пункт: ")
	if err != nil {
		return 0
	}
	switch c {
	case "0":
		return 0
	case "1":
		created, e := m.Create(backup.CreateOptions{IncludeLists: true, IncludeExtraStrats: true, IncludeOrchestraState: true})
		if e != nil {
			fmt.Fprintln(errw, e)
			return 1
		}
		fmt.Fprintf(out, "Создана резервная копия: %s (%d байт)\n", created.Name, created.Size)
		return 0
	case "2":
		name, e := menuRead(r, out, "Имя backup для восстановления: ")
		if e != nil {
			return 0
		}
		if !backup.ValidName(name) {
			fmt.Fprintln(errw, "Некорректное имя backup.")
			return 1
		}
		fmt.Fprintln(out, "Smart-восстановление переносит поддерживаемые настройки и состояние списков/фиксаций; живой config полностью не заменяется.")
		ok, e := menuConfirm(r, out, "Восстановить эту резервную копию?")
		if e != nil || !ok {
			return 0
		}
		result, e := m.Restore(name, backup.RestoreOptions{Mode: backup.ModeSmart})
		if e != nil {
			fmt.Fprintln(errw, e)
			return 1
		}
		fmt.Fprintf(out, "Восстановлено файлов: %d; параметров config: %d\n", len(result.Files), len(result.SmartApplied))
		return 0
	case "3":
		return cmdRollback(nil, out, errw)
	default:
		fmt.Fprintln(out, "Некорректный выбор.")
	}
	return 0
}

func menuTools(r *bufio.Reader, out, errw io.Writer) int {
	fmt.Fprintln(out, " 1. Проверить HTTPS/TLS (blockcheck2)\n 2. Запустить Web UI\n 3. Статус авторизации\n 4. Включить авторизацию\n 5. Отключить авторизацию\n 6. Справка CLI\n 0. Назад")
	c, err := menuRead(r, out, "Выберите пункт: ")
	if err != nil {
		return 0
	}
	switch c {
	case "0":
		return 0
	case "1":
		fmt.Fprintln(out, "Внимание: blockcheck2 временно останавливает zapret2 и затем пытается восстановить службу.")
		ok, e := menuConfirm(r, out, "Запустить проверку?")
		if e != nil || !ok {
			return 0
		}
		domain, e := menuRead(r, out, "Домен или URL для проверки: ")
		if e != nil {
			return 0
		}
		return cmdBlockcheck([]string{"-y", domain}, out, errw)
	case "2":
		return cmdServe("webui", nil, out, errw)
	case "3":
		return cmdAuth([]string{"status"}, out, errw)
	case "4":
		return cmdAuth([]string{"enable"}, out, errw)
	case "5":
		ok, e := menuConfirm(r, out, "Отключить проверку доступа к Web UI?")
		if e != nil || !ok {
			return 0
		}
		return cmdAuth([]string{"disable", "-y"}, out, errw)
	case "6":
		fmt.Fprint(out, usage)
	default:
		fmt.Fprintln(out, "Некорректный выбор.")
	}
	return 0
}

func menuServer() (*webapi.Server, error) {
	a, err := app.Load()
	if err != nil {
		return nil, err
	}
	return webapi.New(webapi.Options{App: a, Static: os.DirFS(a.Paths.WebUIDir)})
}

func menuResult(out, errw io.Writer, value any, err error) int {
	if err != nil {
		fmt.Fprintln(errw, menuColor("31", fmt.Sprint(err)))
		return 1
	}
	data, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		fmt.Fprintln(errw, e)
		return 1
	}
	fmt.Fprintln(out, menuColor("32", "Готово"))
	fmt.Fprintln(out, string(data))
	return 0
}

func menuColor(code, text string) string {
	if !term.IsTerminal(int(os.Stdout.Fd())) || os.Getenv("TERM") == "dumb" || os.Getenv("NO_COLOR") != "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func menuConfirm(r *bufio.Reader, out io.Writer, question string) (bool, error) {
	a, err := menuRead(r, out, question+" [y/N]: ")
	if err != nil {
		return false, err
	}
	return strings.EqualFold(a, "y") || strings.EqualFold(a, "yes") || a == "д", nil
}

func menuSweep(r *bufio.Reader, out, errw io.Writer) int {
	fmt.Fprintln(out, " 1. Состояние и прогресс\n 2. Полный Supersweep\n 3. Быстрый подбор профиля\n 4. Отменить текущий прогон\n 0. Назад")
	a, e := menuRead(r, out, "Выберите пункт: ")
	if e != nil || a == "0" {
		return 0
	}
	s, err := menuServer()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	ctx := context.Background()
	var result map[string]any
	switch a {
	case "1":
		result, err = s.CLISupersweep(ctx, "status", nil)
	case "2":
		pref, e := menuRead(r, out, "TLS (any/12/13/both, Enter — any): ")
		if e != nil {
			return 0
		}
		if pref == "" {
			pref = "any"
		}
		if pref != "any" && pref != "12" && pref != "13" && pref != "both" {
			fmt.Fprintln(errw, "Допустимые TLS значения: any, 12, 13, both.")
			return 1
		}
		domains, e := menuRead(r, out, "Дополнительные домены через запятую (Enter — стандартный список): ")
		if e != nil {
			return 0
		}
		ok, e := menuConfirm(r, out, "Полный Supersweep проверит профили и домены, может выполняться долго и менять фиксации. Начать?")
		if e != nil || !ok {
			return 0
		}
		scope, e := menuRead(r, out, "Scope (default или mark:N, Enter — default): ")
		if e != nil {
			return 0
		}
		if scope == "" {
			scope = "default"
		}
		result, err = s.CLISupersweep(ctx, "start", map[string]string{"scope": scope, "tls_pref": pref, "domain": domains})
	case "3":
		ptext, e := menuRead(r, out, "Профиль быстрого подбора (1 YouTube, 2 Googlevideo, 3 РКН, 4 Discord): ")
		if e != nil {
			return 0
		}
		p, pe := strconv.Atoi(ptext)
		if pe != nil || p < 1 || p > 4 {
			fmt.Fprintln(errw, "Допустимы профили 1..4.")
			return 1
		}
		addr, e := menuRead(r, out, "Адрес проверки (Enter — стандартный): ")
		if e != nil {
			return 0
		}
		pref, e := menuRead(r, out, "TLS (any/12/13/both, Enter — any): ")
		if e != nil {
			return 0
		}
		if pref == "" {
			pref = "any"
		}
		ok, e := menuConfirm(r, out, "Начать быстрый подбор? Будут созданы временные тестовые правила.")
		if e != nil || !ok {
			return 0
		}
		if pref != "any" && pref != "12" && pref != "13" && pref != "both" {
			fmt.Fprintln(errw, "Допустимые TLS значения: any, 12, 13, both.")
			return 1
		}
		scope, e := menuRead(r, out, "Scope (default или mark:N, Enter — default): ")
		if e != nil {
			return 0
		}
		if scope == "" {
			scope = "default"
		}
		result, err = s.CLIStartQuickSweep(ctx, p, addr, scope, pref)
	case "4":
		ok, e := menuConfirm(r, out, "Отменить именно тестирование Supersweep?")
		if e != nil || !ok {
			return 0
		}
		result, err = s.CLISupersweep(ctx, "cancel", nil)
	default:
		fmt.Fprintln(out, "Некорректный выбор.")
		return 0
	}
	if a == "2" || a == "3" {
		if err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		fmt.Fprintln(out, "Прогон запущен. Ctrl+C отменяет именно тестирование; выход из меню не отменяет прогон.")
		return monitorSweep(s, out, errw)
	}
	code := menuResult(out, errw, result, err)
	if a == "1" && err == nil {
		printSweepProgress(out, result)
	}
	return code
}

func monitorSweep(s *webapi.Server, out, errw io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		result, err := s.CLISupersweep(checkCtx, "status", nil)
		cancel()
		if err != nil {
			fmt.Fprintln(errw, "Не удалось прочитать состояние Supersweep:", err)
			return 1
		}
		printSweepLine(out, result)
		root, _ := result["running"].(bool)
		if !root {
			fmt.Fprintln(out)
			printSweepProgress(out, result)
			fmt.Fprintln(out, "Supersweep завершён.")
			return 0
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(out, "\nПросмотр остановлен. Supersweep продолжает работу; для отмены выберите пункт 4.")
			return 0
		case <-ticker.C:
		}
	}
}

func printSweepLine(out io.Writer, value any) {
	root, ok := value.(map[string]any)
	if !ok {
		return
	}
	rep, ok := root["report"].(sweep.Report)
	if !ok {
		b, _ := json.Marshal(root["report"])
		_ = json.Unmarshal(b, &rep)
	}
	if !rep.State.Valid() {
		return
	}
	pct := 0
	if rep.Total > 0 {
		pct = rep.Completed * 100 / rep.Total
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	fmt.Fprintf(out, "\rSupersweep %-12s %3d%% (%d/%d), воркеры %d, найдено %d", rep.Phase, pct, rep.Completed, rep.Total, len(rep.Workers), len(rep.Applied))
}

func menuExit(r *bufio.Reader, out, errw io.Writer) bool {
	s, err := menuServer()
	if err != nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	state, err := s.CLISupersweep(ctx, "status", nil)
	cancel()
	if err != nil {
		return true
	}
	running, _ := state["running"].(bool)
	if !running {
		return true
	}
	fmt.Fprintln(out, "Supersweep ещё выполняется. Выход завершит процесс и прервёт прогон.")
	stop, err := menuConfirm(r, out, "Отменить тестирование и выйти?")
	if err != nil || !stop {
		return false
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	_, err = s.CLISupersweep(ctx, "cancel", nil)
	cancel()
	if err != nil {
		fmt.Fprintln(errw, err)
		return false
	}
	return true
}

func printSweepProgress(out io.Writer, value any) {
	root, ok := value.(map[string]any)
	if !ok {
		return
	}
	rep, ok := root["report"].(sweep.Report)
	if !ok {
		b, _ := json.Marshal(root["report"])
		_ = json.Unmarshal(b, &rep)
	}
	if !rep.State.Valid() {
		return
	}
	pct := 0
	if rep.Total > 0 {
		pct = rep.Completed * 100 / rep.Total
		if pct > 100 {
			pct = 100
		}
	}
	if pct < 0 {
		pct = 0
	}
	filled := pct / 5
	fmt.Fprintf(out, "Supersweep: %s\nПроверено: %d / %d\nПрогресс: [%s%s] %d%%\nВоркеры: %d\nСостояние: %s\nНайдено применений: %d\n", rep.Phase, rep.Completed, rep.Total, strings.Repeat("#", filled), strings.Repeat("-", 20-filled), pct, len(rep.Workers), rep.State, len(rep.Applied))
}

func menuDomains(r *bufio.Reader, out, errw io.Writer) int {
	fmt.Fprintln(out, "Списки: netrogat (исключения), custom_rkn (свои RKN домены), substring, netrogat_substring, googlevideo")
	name, e := menuRead(r, out, "Имя списка: ")
	if e != nil || name == "0" {
		return 0
	}
	scope, e := menuRead(r, out, "Scope (default или mark:N, Enter — default): ")
	if e != nil {
		return 0
	}
	if scope == "" {
		scope = orchestra.ScopeDefault
	}
	s, err := menuServer()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	data, err := s.CLIDomains(context.Background(), name, scope, "list", nil)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	items, _ := data["items"].([]map[string]any)
	if items == nil {
		b, _ := json.Marshal(data)
		var d struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.Unmarshal(b, &d)
		items = d.Items
	}
	fmt.Fprintf(out, "Список %s: %d записей\n", name, len(items))
	const maxVisibleDomains = 40
	for i, it := range items {
		if i == maxVisibleDomains {
			fmt.Fprintf(out, "  ... ещё %d записей (поиск покажет совпадения)\n", len(items)-maxVisibleDomains)
			break
		}
		fmt.Fprintf(out, "  %v\n", it["value"])
	}
	if name == "custom_rkn" {
		fmt.Fprintln(out, " 1. Добавить\n 2. Удалить\n 3. Поиск\n 4. Импорт из файла\n 5. Назначить стратегию\n 6. Снять фиксацию\n 0. Назад")
	} else {
		fmt.Fprintln(out, " 1. Добавить\n 2. Удалить\n 3. Поиск\n 4. Импорт из файла\n 0. Назад")
	}
	a, e := menuRead(r, out, "Выберите пункт: ")
	if e != nil || a == "0" {
		return 0
	}
	if a == "3" {
		q, e := menuRead(r, out, "Строка поиска: ")
		if e != nil {
			return 0
		}
		n := 0
		for _, it := range items {
			v := fmt.Sprint(it["value"])
			if strings.Contains(strings.ToLower(v), strings.ToLower(q)) {
				fmt.Fprintln(out, v)
				n++
			}
		}
		fmt.Fprintf(out, "Найдено: %d\n", n)
		return 0
	}
	if a != "1" && a != "2" && a != "4" && !(name == "custom_rkn" && (a == "5" || a == "6")) {
		fmt.Fprintln(out, "Некорректный пункт.")
		return 0
	}
	if a == "5" || a == "6" {
		domain, e := menuRead(r, out, "Домен: ")
		if e != nil {
			return 0
		}
		if a == "6" {
			ok, e := menuConfirm(r, out, "Снять фиксацию стратегии?")
			if e != nil || !ok {
				return 0
			}
			res, err := s.CLIDomains(context.Background(), name, scope, "clear_strategy", map[string]string{"domain": domain})
			return menuResult(out, errw, res, err)
		}
		max := 0
		switch v := data["max_strategy"].(type) {
		case int:
			max = v
		case float64:
			max = int(v)
		}
		if max < 1 {
			max = 19
		}
		raw, e := menuRead(r, out, fmt.Sprintf("Стратегия 1..%d: ", max))
		if e != nil {
			return 0
		}
		n, pe := strconv.Atoi(raw)
		if pe != nil || n < 1 || n > max {
			fmt.Fprintln(errw, "Номер стратегии вне диапазона.")
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res, err := s.CLIDomains(ctx, name, scope, "set_strategy", map[string]string{"domain": domain, "strategy": strconv.Itoa(n)})
		return menuResult(out, errw, res, err)
	}
	verb := map[string]string{"1": "add", "2": "remove", "4": "import"}[a]
	if verb == "remove" {
		d, e := menuRead(r, out, "Домен / значение: ")
		if e != nil {
			return 0
		}
		ok, e := menuConfirm(r, out, "Удалить запись из списка?")
		if e != nil || !ok {
			return 0
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res, err := s.CLIDomains(ctx, name, scope, verb, map[string]string{"domain": d})
		return menuResult(out, errw, res, err)
	}
	if verb == "add" {
		d, e := menuRead(r, out, "Домен / значение: ")
		if e != nil {
			return 0
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res, err := s.CLIDomains(ctx, name, scope, verb, map[string]string{"domain": d})
		return menuResult(out, errw, res, err)
	}
	if verb == "import" {
		file, e := menuRead(r, out, "Путь к файлу со списком: ")
		if e != nil {
			return 0
		}
		info, e := os.Stat(file)
		if e != nil {
			fmt.Fprintln(errw, e)
			return 1
		}
		if info.Size() > 4<<20 {
			fmt.Fprintln(errw, "Файл больше допустимых 4 MiB.")
			return 1
		}
		content, e := os.ReadFile(file)
		if e != nil {
			fmt.Fprintln(errw, e)
			return 1
		}
		lineCount := 0
		for _, b := range content {
			if b == '\n' {
				lineCount++
			}
		}
		if len(content) > 0 && content[len(content)-1] != '\n' {
			lineCount++
		}
		fmt.Fprintf(out, "Будет обработано строк: %d. Текущие записи сохранятся.\n", lineCount)
		ok, e := menuConfirm(r, out, "Импортировать список из файла?")
		if e != nil || !ok {
			return 0
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res, err := s.CLIDomains(ctx, name, scope, verb, map[string]string{"domain": string(content)})
		return menuResult(out, errw, res, err)
	}
	return 0
}

func menuSettings(r *bufio.Reader, out, errw io.Writer) int {
	settings := []struct{ key, label string }{{"auto_mode", "Авторотация"}, {"fallback", "Fallback"}, {"hostlist", "Autohostlist"}, {"udp_games", "UDP Games"}, {"dns_desync", "DNS desync"}, {"rst_guard", "RST guard"}, {"wg_state", "WireGuard UDP"}, {"reasm", "Reassembly"}, {"wssize_ramp", "WSSIZE ramp"}}
	s, err := menuServer()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	for i, v := range settings {
		state, e := s.CLISetting(context.Background(), v.key)
		fmt.Fprintf(out, " %d. %s: %s\n", i+1, v.label, menuStateText(state))
		if e != nil {
			fmt.Fprintf(out, "    недоступно: %v\n", e)
		}
	}
	fmt.Fprintln(out, " 0. Назад")
	raw, e := menuRead(r, out, "Номер настройки для переключения: ")
	if e != nil || raw == "0" {
		return 0
	}
	n, e := strconv.Atoi(raw)
	if e != nil || n < 1 || n > len(settings) {
		fmt.Fprintln(errw, "Некорректный номер.")
		return 1
	}
	setting := settings[n-1]
	current, e := s.CLISetting(context.Background(), setting.key)
	if e != nil {
		fmt.Fprintln(errw, e)
		return 1
	}
	b, _ := json.Marshal(current)
	var st struct {
		Enabled bool   `json:"enabled"`
		Auto    bool   `json:"auto"`
		State   string `json:"state"`
	}
	_ = json.Unmarshal(b, &st)
	on := !st.Enabled
	if setting.key == "hostlist" {
		on = !st.Auto
	} else if st.State != "" {
		switch strings.ToLower(st.State) {
		case "включен", "включено", "enabled", "1", "on":
			on = false
		case "выключен", "выключено", "отключен", "disabled", "0", "off", "ручной":
			on = true
		}
	}
	value := "0"
	if on {
		value = "1"
	}
	ok, e := menuConfirm(r, out, fmt.Sprintf("%s: переключить состояние на %s?", setting.label, map[bool]string{true: "включено", false: "выключено"}[on]))
	if e != nil || !ok {
		return 0
	}
	res, err := s.CLISetSetting(context.Background(), setting.key, map[string]string{"value": value, "restart": "1"})
	return menuResult(out, errw, res, err)
}

func menuStateText(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	var state struct {
		State   string `json:"state"`
		Enabled *bool  `json:"enabled"`
		Auto    *bool  `json:"auto"`
	}
	if json.Unmarshal(b, &state) != nil {
		return fmt.Sprint(value)
	}
	if state.State != "" {
		return state.State
	}
	if state.Enabled != nil {
		return map[bool]string{true: "включено", false: "выключено"}[*state.Enabled]
	}
	if state.Auto != nil {
		return map[bool]string{true: "авто", false: "ручной"}[*state.Auto]
	}
	return fmt.Sprint(value)
}

func menuUpdate(r *bufio.Reader, out, errw io.Writer) int {
	a, err := app.Load()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	offline := deploy.ResolveOfflineBundle(os.Getenv)
	if offline.Offline {
		if offline.Bundle == nil {
			fmt.Fprintln(errw, "Офлайн-режим активен, но bundle приложения/ресурсов не найден.")
			return 1
		}
		fmt.Fprintf(out, "Офлайн bundle: %s\nСетевая проверка обновлений пропущена.\n", offline.Bundle.Tarball)
		ok, e := menuConfirm(r, out, "Применить обновление из локального bundle?")
		if e != nil || !ok {
			return 0
		}
		mode, ok := menuChooseUpdateMode(r, out, offline.Bundle.AppArchive != "", offline.Bundle.ConfigArchive != "")
		if !ok {
			return 0
		}
		return cmdUpdate([]string{"-mode", mode}, out, errw)
	}
	u := deploy.New(a.Paths)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := u.CheckLatest(ctx)
	if err != nil {
		fmt.Fprintf(errw, "Проверка обновления: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "Установлена версия %s\n", zator.Version())
	if res.Latest != nil {
		fmt.Fprintf(out, "Последний релиз: %s\n", res.Latest.Release)
	}
	fmt.Fprintf(out, "Обновление доступно: %t\n", res.UpdateAvailable())
	fmt.Fprintf(out, "Обновление шаблона конфигурации: %t\n", res.UpdateConfig)
	if !res.UpdateAvailable() {
		return 0
	}
	mode, ok := menuChooseUpdateMode(r, out, res.UpdateZator || res.UpdateWebUI, res.UpdateConfig)
	if !ok {
		return 0
	}
	ok, e := menuConfirm(r, out, "Создать резервную копию и установить обновление?")
	if e != nil || !ok {
		return 0
	}
	return cmdUpdate([]string{"-mode", mode}, out, errw)
}

func menuChooseUpdateMode(r *bufio.Reader, out io.Writer, appUpdate, configUpdate bool) (string, bool) {
	if appUpdate && configUpdate {
		choice, err := menuRead(r, out, "Что обновить: 1 — Zator и Web UI, 2 — ресурсы/шаблон, 3 — всё, 0 — отмена: ")
		if err != nil || choice == "0" {
			return "", false
		}
		switch choice {
		case "1":
			return deploy.ModeApp, true
		case "2":
			return deploy.ModeConfig, true
		case "3":
			return deploy.ModeFull, true
		default:
			fmt.Fprintln(out, "Некорректный выбор.")
			return "", false
		}
	}
	if configUpdate {
		return deploy.ModeConfig, true
	}
	if appUpdate {
		return deploy.ModeApp, true
	}
	return "", false
}

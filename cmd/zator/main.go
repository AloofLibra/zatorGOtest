// Команда zator — установка, обновление и управление zapret2 через Web UI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/AloofLibra/zator"
	"github.com/AloofLibra/zator/internal/app"
	"github.com/AloofLibra/zator/internal/backup"
	"github.com/AloofLibra/zator/internal/deploy"
	"github.com/AloofLibra/zator/internal/orchestra"
	"github.com/AloofLibra/zator/internal/service"
	"github.com/AloofLibra/zator/internal/webapi"
)

const usage = `zator — управление zapret2 через Web UI

Использование:
  zator [команда] [флаги]

Команды:
  version     показать версию
  status      показать состояние установки и платформу
  service     аварийный контроль служб: status|start|stop|restart <unit>
  locks       аварийный сброс фиксаций стратегий (с backup)
  restore     smart-восстановление из backup
  rollback    восстановить последний pre-update backup
  install     установить zator (каталоги, Web UI, конфигурация)
  update      обновить ресурсы и бинарник с GitHub (-force — всегда скачать)
  uninstall   удалить runtime (-y); --purge сносит весь каталог
  auth        пароль и включение/отключение проверки доступа
  webui       запустить Web UI (порт по умолчанию 17682)
  serve       синоним webui
  help        эта справка
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return cmdDefault(stdout)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "-v", "--version":
		fmt.Fprintf(stdout, "zator %s\n", zator.Version())
		return 0
	case "status":
		return cmdStatus(stdout, stderr)
	case "service":
		return cmdService(rest, stdout, stderr)
	case "locks":
		return cmdLocks(rest, stdout, stderr)
	case "restore":
		return cmdRestore(rest, stdout, stderr)
	case "rollback":
		return cmdRollback(rest, stdout, stderr)
	case "install":
		return cmdInstall(rest, stdout, stderr)
	case "update":
		return cmdUpdate(rest, stdout, stderr)
	case "uninstall":
		return cmdUninstall(rest, stdout, stderr)
	case "auth":
		return cmdAuth(rest, stdout, stderr)
	case "webui", "serve":
		return cmdServe(cmd, rest, stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "неизвестная команда %q\n\n%s", cmd, usage)
		return 2
	}
}

func cmdRestore(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || !backup.ValidName(args[0]) {
		fmt.Fprintln(stderr, "использование: zator restore z2r_backup_YYYYMMDD_HHMMSS.tar")
		return 2
	}
	a, err := app.Load()
	if err != nil {
		fmt.Fprintf(stderr, "ошибка конфигурации: %v\n", err)
		return 1
	}
	result, err := backup.NewIn(a.Paths, a.Paths.BackupDir).Restore(args[0], backup.RestoreOptions{Mode: backup.ModeSmart})
	if err != nil {
		fmt.Fprintf(stderr, "восстановление не выполнено: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "восстановлено файлов: %d (режим smart)\n", len(result.Files))
	return 0
}

func cmdLocks(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "clear" {
		fmt.Fprintln(stderr, "использование: zator locks clear -y")
		return 2
	}
	fs := flag.NewFlagSet("locks clear", flag.ContinueOnError)
	fs.SetOutput(stderr)
	yes := fs.Bool("y", false, "подтвердить сброс фиксаций")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if !*yes {
		fmt.Fprintln(stderr, "фиксации стратегий будут удалены; повторите с -y")
		return 1
	}
	a, err := app.Load()
	if err != nil {
		fmt.Fprintf(stderr, "ошибка конфигурации: %v\n", err)
		return 1
	}
	store := orchestra.NewStore(a.Paths.StateOrchestraDir())
	locks, err := store.ListAllLocks()
	if err != nil {
		fmt.Fprintf(stderr, "не удалось прочитать фиксации: %v\n", err)
		return 1
	}
	if len(locks) == 0 {
		fmt.Fprintln(stdout, "фиксаций нет")
		return 0
	}
	info, err := backup.NewIn(a.Paths, a.Paths.BackupDir).Create(backup.CreateOptions{})
	if err != nil {
		fmt.Fprintf(stderr, "сброс остановлен: backup не создан: %v\n", err)
		return 1
	}
	for _, lock := range locks {
		if err := store.ClearLock(lock.Scope, lock.Profile, string(lock.Proto)); err != nil {
			fmt.Fprintf(stderr, "частичный сброс остановлен на %s: %v (backup %s)\n", lock, err, info.Name)
			return 1
		}
	}
	fmt.Fprintf(stdout, "сброшено фиксаций: %d; backup: %s\n", len(locks), info.Name)
	return 0
}

func cmdRollback(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "использование: zator rollback")
		return 2
	}
	a, err := app.Load()
	if err != nil {
		fmt.Fprintf(stderr, "ошибка конфигурации: %v\n", err)
		return 1
	}
	if err := a.RollbackPreUpdateBackup(); err != nil {
		fmt.Fprintf(stderr, "откат не выполнен: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "выполнен smart-откат из последнего pre-update backup")
	return 0
}

func cmdService(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "использование: zator service status|warnings|start|stop|restart [zapret2|zator-strategy-validator|zator-webui]")
		return 2
	}
	a, err := app.Load()
	if err != nil {
		fmt.Fprintf(stderr, "ошибка конфигурации: %v\n", err)
		return 1
	}
	action := args[0]
	if action == "warnings" {
		if len(args) != 1 {
			fmt.Fprintln(stderr, "использование: zator service warnings")
			return 2
		}
		warnings := a.ServiceWarnings()
		if len(warnings) == 0 {
			fmt.Fprintln(stdout, "ошибок служб нет")
			return 0
		}
		for _, warning := range warnings {
			fmt.Fprintln(stdout, warning)
		}
		return 0
	}
	var units []service.Unit
	if action == "status" {
		if len(args) > 2 {
			fmt.Fprintln(stderr, "использование: zator service status [unit]")
			return 2
		}
		units, err = serviceUnits(args[1:])
	} else {
		if action != "start" && action != "stop" && action != "restart" || len(args) != 2 {
			fmt.Fprintln(stderr, "использование: zator service start|stop|restart <unit>")
			return 2
		}
		units, err = serviceUnits(args[1:])
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctl, err := service.New(service.Config{Platform: a.Platform, Paths: a.Paths})
	if err != nil {
		fmt.Fprintf(stderr, "управление службами недоступно: %v\n", err)
		return 1
	}
	if action == "status" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		failed := false
		for _, unit := range units {
			st, err := ctl.Status(ctx, unit)
			if err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", unit, err)
				failed = true
				continue
			}
			fmt.Fprintf(stdout, "%s: running=%t pids=%v backend=%s\n", unit, st.Running, st.PIDs, st.Backend)
		}
		if failed {
			return 1
		}
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	failed := false
	for _, unit := range units {
		switch action {
		case "start":
			err = ctl.Start(ctx, unit)
		case "stop":
			err = ctl.Stop(ctx, unit)
		case "restart":
			err = ctl.Restart(ctx, unit)
		}
		if err != nil {
			fmt.Fprintf(stderr, "%s %s: %v\n", action, unit, err)
			failed = true
		} else {
			fmt.Fprintf(stdout, "%s: %s ok\n", unit, action)
		}
	}
	if failed {
		return 1
	}
	return 0
}

func serviceUnits(args []string) ([]service.Unit, error) {
	if len(args) == 0 {
		return service.Units(), nil
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("укажите одну службу")
	}
	if args[0] == "all" {
		return service.Units(), nil
	}
	unit := service.Unit(args[0])
	if !unit.Valid() {
		return nil, fmt.Errorf("неизвестная служба %q", args[0])
	}
	return []service.Unit{unit}, nil
}

func cmdDefault(stdout io.Writer) int {
	a, err := app.Load()
	if err != nil {
		fmt.Fprintf(stdout, "zator %s: ошибка конфигурации: %v\n", zator.Version(), err)
		return 1
	}
	if a.Installed() {
		fmt.Fprintf(stdout, "zator %s установлен в %s\n", zator.Version(), a.Paths.ZatorRoot)
		fmt.Fprintf(stdout, "Web UI: %s\n", a.WebUIURL(""))
		fmt.Fprintln(stdout, "Подробнее: zator status, запуск: zator webui")
		return 0
	}
	fmt.Fprintf(stdout, "zator %s не установлен.\n", zator.Version())
	fmt.Fprintln(stdout, "Установите командой: zator install")
	return 0
}

func cmdStatus(stdout, stderr io.Writer) int {
	a, err := app.Load()
	if err != nil {
		fmt.Fprintf(stderr, "ошибка конфигурации: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Версия:      %s\n", zator.Version())
	fmt.Fprintf(stdout, "Установлен:  %t\n", a.Installed())
	fmt.Fprintf(stdout, "Платформа:   %s\n", a.Platform)
	fmt.Fprintf(stdout, "ZATOR_ROOT:  %s\n", a.Paths.ZatorRoot)
	fmt.Fprintf(stdout, "ZAPRET2:     %s\n", a.Paths.ZapretRoot)
	fmt.Fprintf(stdout, "Конфиг:      %s\n", a.ConfigPath)
	fmt.Fprintf(stdout, "Web UI:      http://localhost:%d/\n", a.Config.Port)
	fmt.Fprintf(stdout, "Auth:        enabled=%t password=%t\n", a.Config.AuthEnabled, a.Config.HasPassword())
	fmt.Fprintf(stdout, "Источник:    %s\n", a.Config.ZapretSource)
	return 0
}

func cmdInstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	port := fs.Int("port", 0, "порт Web UI")
	mirror := fs.String("mirror", "", "зеркало для загрузок")
	source := fs.String("source", "", "источник zapret: official|fork")
	withZapret := fs.Bool("zapret", true, "скачать и установить zapret2")
	zapretVer := fs.String("zapret-version", "", "версия zapret2 (пусто = latest с GitHub)")
	skipService := fs.Bool("skip-service", false, "не ставить systemd/procd службу")
	password := fs.String("password", "", "пароль Web UI (неинтерактивно)")
	noAuth := fs.Bool("no-auth", false, "сознательно отключить проверку доступа")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *password != "" && *noAuth {
		fmt.Fprintln(stderr, "нельзя одновременно -password и -no-auth")
		return 2
	}
	a, err := app.Load()
	if err != nil {
		fmt.Fprintf(stderr, "ошибка конфигурации: %v\n", err)
		return 1
	}
	if *port != 0 {
		a.Config.Port = *port
	}
	if *mirror != "" {
		a.Config.Mirror = *mirror
	}
	if *source != "" {
		a.Config.ZapretSource = *source
	}
	if err := a.Config.Validate(); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	switch {
	case *noAuth:
		a.DisableAuthExplicitly()
		fmt.Fprintln(stderr, "внимание: проверка доступа отключена (-no-auth); Web UI открыт всей сети")
	case *password != "":
		if err := a.SetPassword(*password); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	case a.Config.HasPassword():
		// Повторный install / refresh ассетов (в т.ч. из update) — сохраняем auth.
	case a.Installed() && !a.Config.AuthEnabled:
		// Ранее сознательно -no-auth / auth disable — не навязываем пароль при refresh.
	default:
		pass, err := promptPassword(stderr, "Пароль Web UI: ", "Повтор пароля: ")
		if err != nil {
			fmt.Fprintln(stderr, err)
			fmt.Fprintln(stderr, "подсказка: zator install -password '…' или zator install -no-auth")
			return 1
		}
		if err := a.SetPassword(pass); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if err := a.Install(); err != nil {
		fmt.Fprintf(stderr, "установка не удалась: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "zator установлен в %s\n", a.Paths.ZatorRoot)
	if err := a.InstallSelfBinary(); err != nil {
		fmt.Fprintf(stderr, "предупреждение: бинарник не скопирован: %v\n", err)
	}
	if *withZapret {
		fmt.Fprintln(stdout, "загрузка zapret2 (latest)...")
		zver := *zapretVer
		if zver == "" {
			zver = "latest"
		}
		res, err := a.InstallZapret(context.Background(), app.InstallZapretOptions{
			SkipService: *skipService,
			Version:     zver,
			Log:         stdout,
		})
		if err != nil {
			var soft *app.SoftServiceError
			if errors.As(err, &soft) {
				fmt.Fprintf(stderr, "предупреждение zapret2/службы: %v\n", soft)
				fmt.Fprintln(stderr, "Службы zapret2 могут быть не запущены — смотрите Web UI → Обзор.")
			} else {
				fmt.Fprintf(stderr, "zapret2: %v\n", err)
				return 1
			}
		}
		if res != nil {
			skip := ""
			if res.Skipped {
				skip = " (уже установлен)"
			}
			fmt.Fprintf(stdout, "zapret2 %s (%s) → %s%s\n", res.Version, res.Source, res.Root, skip)
		}
	} else if !*skipService {
		if err := a.StartServices(context.Background(), app.BootstrapOptions{}); err != nil {
			var soft *app.SoftServiceError
			if errors.As(err, &soft) {
				fmt.Fprintf(stderr, "предупреждение служб: %v\n", soft)
				fmt.Fprintln(stderr, "Web UI поднят; zapret2 может требовать ручного start.")
			} else {
				fmt.Fprintf(stderr, "автозапуск служб: %v\n", err)
				return 1
			}
		}
	}
	fmt.Fprintf(stdout, "Web UI: %s\n", a.WebUIURL(""))
	if a.Config.AuthRequired() {
		fmt.Fprintln(stdout, "Вход: пароль, заданный при установке.")
	}
	if !*skipService {
		if warns := a.ServiceWarnings(); len(warns) > 0 {
			fmt.Fprintln(stdout, "Службы: Web UI запущен; zapret2 — с предупреждениями (см. Обзор).")
		} else {
			fmt.Fprintln(stdout, "Службы zapret2 и Web UI установлены и запущены.")
		}
	} else {
		fmt.Fprintln(stdout, "Запуск панели: zator webui")
	}
	return 0
}

func cmdUpdate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "скачать релиз даже если версия совпадает")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	a, err := app.Load()
	if err != nil {
		fmt.Fprintf(stderr, "ошибка конфигурации: %v\n", err)
		return 1
	}
	if !a.Installed() {
		fmt.Fprintf(stderr, "zator не установлен в %s — сначала: zator install\n", a.Paths.ZatorRoot)
		return 1
	}
	if info, err := a.CreatePreUpdateBackup(); err != nil {
		fmt.Fprintf(stderr, "предупреждение: pre-update backup: %v\n", err)
	} else {
		fmt.Fprintf(stdout, "pre-update backup: %s\n", info.Name)
	}
	// Сначала локальные встроенные ресурсы, затем (если онлайн) релизный tar/бинарник.
	if err := a.Update(); err != nil {
		fmt.Fprintf(stderr, "обновление ресурсов не удалось: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "zator %s: встроенные ресурсы обновлены\n", zator.Version())

	u := deploy.New(a.Paths)
	off := deploy.ResolveOfflineBundle(os.Getenv)
	if off.Offline {
		if off.Bundle == nil {
			fmt.Fprintln(stdout, "офлайн-режим: бандл не найден (ZATOR_BUNDLE_DIR / vendor+zator-*.tar.gz)")
			return 0
		}
		fmt.Fprintf(stdout, "офлайн-бандл: %s\n", off.Bundle.Tarball)
		ar, err := u.ApplyBundle(*off.Bundle, "")
		if err != nil {
			fmt.Fprintf(stderr, "офлайн ApplyBundle: %v\n", err)
			return 1
		}
		if ar != nil {
			fmt.Fprintf(stdout, "офлайн: установлено файлов %d\n", ar.Installed)
		}
		if err := a.VerifyAfterUpdate(); err != nil {
			fmt.Fprintf(stderr, "post-update verify: %v\n", err)
			if rerr := a.RollbackPreUpdateBackup(); rerr != nil {
				fmt.Fprintf(stderr, "rollback: %v\n", rerr)
			} else {
				fmt.Fprintln(stdout, "выполнен откат из pre-update backup")
			}
			return 1
		}
		return 0
	}
	rep, err := u.Run(context.Background(), deploy.RunOptions{
		Variant: deploy.VariantFull,
		Force:   *force,
	})
	if err != nil {
		fmt.Fprintf(stderr, "проверка/применение релиза: %v\n", err)
		return 1
	}
	if rep != nil && rep.UpToDate {
		fmt.Fprintln(stdout, "релизы: уже актуальная версия (для принудительного скачивания: zator update -force)")
		return 0
	}
	if rep != nil && rep.Apply != nil {
		fmt.Fprintf(stdout, "релиз применён: установлено файлов %d", rep.Apply.Installed)
		if rep.Archive != "" {
			fmt.Fprintf(stdout, " (%s)", rep.Archive)
		}
		fmt.Fprintln(stdout)
		if err := a.VerifyAfterUpdate(); err != nil {
			fmt.Fprintf(stderr, "post-update verify: %v\n", err)
			if rerr := a.RollbackPreUpdateBackup(); rerr != nil {
				fmt.Fprintf(stderr, "rollback: %v\n", rerr)
			} else {
				fmt.Fprintln(stdout, "выполнен откат из pre-update backup")
			}
			return 1
		}
		fmt.Fprintln(stdout, "post-update verify: ok")
	}
	return 0
}

func cmdUninstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	yes := fs.Bool("y", false, "не спрашивать подтверждение")
	purge := fs.Bool("purge", false, "удалить весь каталог zator (config/state/lists)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	a := app.New()
	if !a.Installed() {
		fmt.Fprintln(stdout, "zator не установлен")
		return 0
	}
	if !*yes {
		if *purge {
			fmt.Fprintf(stderr, "будет полностью удалён %s (включая config/state/lists); повторите с -y --purge\n", a.Paths.ZatorRoot)
		} else {
			fmt.Fprintf(stderr, "будут удалены bin/webui/lua и службы; config/state/lists/files сохранятся в %s; повторите с -y (полный снос: -y --purge)\n", a.Paths.ZatorRoot)
		}
		return 1
	}
	if err := a.Uninstall(app.UninstallOptions{Purge: *purge}); err != nil {
		fmt.Fprintf(stderr, "удаление не удалось: %v\n", err)
		return 1
	}
	if *purge {
		fmt.Fprintln(stdout, "zator полностью удалён (--purge)")
	} else {
		fmt.Fprintln(stdout, "runtime zator удалён; config/state/lists/files сохранены")
	}
	return 0
}

// newWebHandler собирает обработчик Web UI: API (/cgi-bin/*.cgi и /api/*) и
// статику. Без auth_enabled API открыт всем — предупреждение в stderr.
func newWebHandler(a *app.App, stderr io.Writer) (http.Handler, error) {
	api, err := webapi.New(webapi.Options{App: a})
	if err != nil {
		return nil, err
	}
	if !a.Config.AuthRequired() {
		fmt.Fprintln(stderr, "Web UI без проверки доступа (auth_enabled=false); включите: zator auth enable / set-password")
	}
	return api.Handler(), nil
}

func cmdServe(name string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	port := fs.Int("port", 0, "порт (по умолчанию из конфигурации, 17682)")
	host := fs.String("host", "", "адрес привязки (по умолчанию все интерфейсы)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	a, err := app.Load()
	if err != nil {
		fmt.Fprintf(stderr, "ошибка конфигурации: %v\n", err)
		return 1
	}
	if *port != 0 {
		a.Config.Port = *port
	}
	// Восстановить стоковые blob, если каталог пуст (ручная замена бинарника / старый uninstall).
	if err := a.EnsureStockFakes(); err != nil {
		fmt.Fprintf(stderr, "предупреждение files/fake: %v\n", err)
	}

	handler, err := newWebHandler(a, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "ошибка инициализации Web UI: %v\n", err)
		return 1
	}

	addr := net.JoinHostPort(*host, strconv.Itoa(a.Config.Port))
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := a.StartGooglevideoCollection(ctx); err != nil {
		fmt.Fprintf(stderr, "предупреждение googlevideo-сбор: %v\n", err)
	}
	defer a.StopGooglevideoCollection()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	fmt.Fprintf(stdout, "Web UI: %s\n", a.WebUIURL(""))

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "ошибка сервера: %v\n", err)
			return 1
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
	return 0
}

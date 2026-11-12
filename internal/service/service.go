// Package service управляет службами zapret2 (nfqws2) и zator-webui:
// записывает unit/init-файлы и вызывает systemd, procd, SysV или Entware.
package service

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/platform"
)

// Unit — управляемая служба.
type Unit string

const (
	// UnitZapret2 — демон nfqws2.
	UnitZapret2 Unit = "zapret2"
	// UnitWebUI — Web UI zator.
	UnitWebUI Unit = "zator-webui"
)

// Valid сообщает, известна ли служба.
func (u Unit) Valid() bool { return u == UnitZapret2 || u == UnitWebUI }

// Units — все управляемые службы.
func Units() []Unit { return []Unit{UnitZapret2, UnitWebUI} }

// Backend — механизм управления службами.
type Backend string

const (
	BackendSystemd Backend = "systemd"
	BackendProcd   Backend = "procd"
	BackendEntware Backend = "entware"
	BackendSysV    Backend = "sysv"
)

// Hook — хук перестроения межсетевого экрана, зависящий от прошивки.
type Hook string

const (
	HookNone Hook = ""
	// HookMerlin — строка в /jffs/scripts/firewall-start (Asuswrt-Merlin).
	HookMerlin Hook = "merlin"
	// HookKeenetic — скрипт /opt/etc/ndm/netfilter.d/000-zapret2.sh (Keenetic/Netcraze).
	HookKeenetic Hook = "keenetic"
)

// DetectBackend выбирает механизм по платформе.
func DetectBackend(p platform.Platform) Backend {
	switch p.InitSystem {
	case platform.InitSystemd:
		return BackendSystemd
	case platform.InitProcd:
		return BackendProcd
	case platform.InitEntware:
		return BackendEntware
	case platform.InitSysV:
		return BackendSysV
	}
	switch p.Kind {
	case platform.KindOpenWRT:
		return BackendProcd
	case platform.KindEntware:
		return BackendEntware
	}
	return BackendSysV
}

// DetectHook выбирает хук межсетевого экрана (только для Entware-прошивок).
func DetectHook(p platform.Platform) Hook {
	if DetectBackend(p) != BackendEntware {
		return HookNone
	}
	switch p.Hardware {
	case platform.HardwareMerlin:
		return HookMerlin
	case platform.HardwareKeenetic, platform.HardwareNetcraze:
		return HookKeenetic
	}
	return HookNone
}

// Status — состояние службы.
type Status struct {
	Unit    Unit
	Backend Backend
	Running bool
	PIDs    []int
}

// Manager управляет жизненным циклом служб.
type Manager interface {
	Start(ctx context.Context, u Unit) error
	Stop(ctx context.Context, u Unit) error
	Restart(ctx context.Context, u Unit) error
	Status(ctx context.Context, u Unit) (Status, error)
}

// Installer записывает файлы служб и включает автозапуск.
type Installer interface {
	Install(ctx context.Context, u Unit) error
	Uninstall(ctx context.Context, u Unit) error
}

// Controller объединяет Manager и Installer.
type Controller interface {
	Manager
	Installer
	// Backend возвращает используемый механизм.
	Backend() Backend
	// Hook возвращает выбранный хук межсетевого экрана.
	Hook() Hook
}

// Runner запускает внешние команды (подменяется в тестах).
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// ExecRunner — Runner на os/exec.
func ExecRunner() Runner { return execRunner{} }

// Config — параметры менеджера служб.
type Config struct {
	Platform platform.Platform
	Paths    paths.Paths

	// Backend и Hook переопределяют автоопределение (необязательно).
	Backend Backend
	Hook    Hook

	// ZatorBin — путь к исполняемому файлу zator (по умолчанию Paths.BinDir/zator).
	ZatorBin string
	// ArgsFile — файл параметров nfqws2 (по умолчанию ZapretRoot/nfqws2.args).
	ArgsFile string
	// QNum — номер NFQUEUE для argv nfqws2 (должен быть до @argsfile;
	// в @file один этот флаг nfqws2 не видит). По умолчанию "200".
	QNum string
	// FWHook — необязательный скрипт настройки межсетевого экрана
	// (вызывается с аргументами start|stop|restart).
	FWHook string

	// Root — префикс файловой системы для системных каталогов (/etc, /opt/etc,
	// /jffs). Пусто — реальная корневая ФС. Нужен для тестов.
	Root string
	// ProcRoot — каталог procfs (по умолчанию /proc).
	ProcRoot string
	// Runner — запуск команд (по умолчанию os/exec).
	Runner Runner
}

// New создаёт Controller для платформы.
func New(cfg Config) (Controller, error) {
	if cfg.Backend == "" {
		cfg.Backend = DetectBackend(cfg.Platform)
	}
	if cfg.Hook == "" {
		cfg.Hook = DetectHook(cfg.Platform)
	}
	if cfg.Paths.ZapretRoot == "" {
		cfg.Paths = paths.New(cfg.Paths.ZatorRoot, "")
	}
	if cfg.ZatorBin == "" {
		cfg.ZatorBin = filepath.Join(cfg.Paths.BinDir, "zator")
	}
	if cfg.ArgsFile == "" {
		cfg.ArgsFile = filepath.Join(cfg.Paths.ZapretRoot, "nfqws2.args")
	}
	if cfg.QNum == "" {
		cfg.QNum = "200"
	}
	if cfg.ProcRoot == "" {
		cfg.ProcRoot = "/proc"
	}
	if cfg.Runner == nil {
		cfg.Runner = ExecRunner()
	}
	for name, v := range map[string]string{
		"ZatorBin": cfg.ZatorBin, "ArgsFile": cfg.ArgsFile, "FWHook": cfg.FWHook,
		"ZapretRoot": cfg.Paths.ZapretRoot, "ZatorRoot": cfg.Paths.ZatorRoot,
	} {
		if err := validatePath(v); err != nil {
			return nil, fmt.Errorf("service: %s: %w", name, err)
		}
	}

	m := &manager{cfg: cfg, scan: Scanner{ProcRoot: cfg.ProcRoot, Runner: cfg.Runner}}
	switch cfg.Backend {
	case BackendSystemd:
		m.impl = systemdBackend{m}
	case BackendProcd:
		m.impl = procdBackend{m}
	case BackendEntware:
		m.impl = entwareBackend{sysvBackend{m}}
	case BackendSysV:
		m.impl = sysvBackend{m}
	default:
		return nil, fmt.Errorf("service: неизвестный backend %q", cfg.Backend)
	}
	return m, nil
}

// validatePath отвергает пути, которые нельзя безопасно подставить в unit/init-файл.
func validatePath(p string) error {
	if strings.ContainsAny(p, "\x00\r\n\t \"'`$%;&|<>()") {
		return fmt.Errorf("недопустимые символы в пути %q", p)
	}
	return nil
}

// backendImpl — реализация для конкретного механизма.
type backendImpl interface {
	install(ctx context.Context, u Unit) error
	uninstall(ctx context.Context, u Unit) error
	control(ctx context.Context, u Unit, action string) error
	// query используется, когда сканирование процессов недоступно.
	query(ctx context.Context, u Unit) (bool, error)
}

type manager struct {
	cfg  Config
	impl backendImpl
	scan Scanner
}

func (m *manager) Backend() Backend { return m.cfg.Backend }
func (m *manager) Hook() Hook       { return m.cfg.Hook }

func (m *manager) check(u Unit) error {
	if !u.Valid() {
		return fmt.Errorf("service: неизвестная служба %q", string(u))
	}
	return nil
}

func (m *manager) Install(ctx context.Context, u Unit) error {
	if err := m.check(u); err != nil {
		return err
	}
	return m.impl.install(ctx, u)
}

func (m *manager) Uninstall(ctx context.Context, u Unit) error {
	if err := m.check(u); err != nil {
		return err
	}
	return m.impl.uninstall(ctx, u)
}

func (m *manager) Start(ctx context.Context, u Unit) error   { return m.act(ctx, u, "start") }
func (m *manager) Stop(ctx context.Context, u Unit) error    { return m.act(ctx, u, "stop") }
func (m *manager) Restart(ctx context.Context, u Unit) error { return m.act(ctx, u, "restart") }

func (m *manager) act(ctx context.Context, u Unit, action string) error {
	if err := m.check(u); err != nil {
		return err
	}
	return m.impl.control(ctx, u, action)
}

// Status определяет состояние по списку процессов; если сканирование
// недоступно, спрашивает механизм управления.
func (m *manager) Status(ctx context.Context, u Unit) (Status, error) {
	if err := m.check(u); err != nil {
		return Status{}, err
	}
	st := Status{Unit: u, Backend: m.cfg.Backend}
	var match Matcher
	pidofName := "" // у Web UI нет собственного имени процесса — pidof не подходит
	switch u {
	case UnitZapret2:
		match, pidofName = MatchNfqws2, "nfqws2"
	case UnitWebUI:
		match = MatchWebUI
	}
	pids, err := m.scan.Find(ctx, match, pidofName)
	switch {
	case err == nil:
		st.PIDs = pids
		st.Running = len(pids) > 0
		return st, nil
	case errors.Is(err, ErrScanUnsupported):
		running, qerr := m.impl.query(ctx, u)
		if qerr != nil {
			return st, qerr
		}
		st.Running = running
		return st, nil
	}
	return st, err
}

// IsNfqws2Running сообщает, запущен ли процесс nfqws2 (сканирование /proc
// либо pidof); при невозможности определить возвращает false.
func IsNfqws2Running() bool {
	pids, err := DefaultScanner().Find(context.Background(), MatchNfqws2, "nfqws2")
	return err == nil && len(pids) > 0
}

// run выполняет команду и оборачивает вывод в ошибку.
func (m *manager) run(ctx context.Context, name string, args ...string) error {
	out, err := m.cfg.Runner.Run(ctx, name, args...)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("service: %s %s: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return fmt.Errorf("service: %s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// sys возвращает путь системного каталога с учётом Config.Root.
func (m *manager) sys(abs string) string {
	if m.cfg.Root == "" || m.cfg.Root == "/" {
		return filepath.FromSlash(abs)
	}
	return filepath.Join(m.cfg.Root, filepath.FromSlash(abs))
}

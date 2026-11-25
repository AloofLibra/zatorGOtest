// Package sysprofile инкапсулирует различия ОС и init-систем (systemd, OpenRC,
// procd, Entware, SysV) в едином легковесном резолвере без глобального состояния.
package sysprofile

import (
	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/platform"
)

// Options задаёт параметры разрешения системного профиля.
type Options struct {
	// Root — префикс корневой ФС для тестов; пустая строка означает "/".
	Root string
}

func (o Options) root() string {
	if o.Root == "" {
		return "/"
	}
	return o.Root
}

// CmdSpec описывает внешний вызов команды.
type CmdSpec struct {
	Name string
	Args []string
}

// CommandOp описывает шаг управления службой с необязательным запасным вызовом.
type CommandOp struct {
	Primary  CmdSpec
	Fallback *CmdSpec
	// BestEffort разрешает проигнорировать любую ошибку выполнения шага.
	BestEffort bool
	// IgnoreNotFound разрешает проигнорировать отсутствие бинарников Primary/Fallback
	// (exec.ErrNotFound), не скрывая реальный ненулевой код возврата команды.
	IgnoreNotFound bool
}

// InitMode задаёт способ подключения нативного init-файла zapret2.
type InitMode uint8

const (
	// InitModeCopy копирует unit-файл в системный каталог (systemd).
	InitModeCopy InitMode = iota
	// InitModeSymlink создаёт символическую ссылку /etc/init.d/zapret2 → upstream.
	InitModeSymlink
	// InitModeInPlace использует init-скрипт прямо из каталога zapret2 (Entware).
	InitModeInPlace
)

// NativeZapretSpec описывает подключение штатного init-файла zapret2.
type NativeZapretSpec struct {
	UpstreamRel   string
	FallbackRel   string
	SystemPath    string
	Mode          InitMode
	OpenRCWrapper bool
}

// DepCheck описывает проверяемый системный бинарник или модуль ядра.
type DepCheck struct {
	Name     string
	Critical bool
	Detail   string
}

// DepsSpec описывает системные зависимости профиля.
type DepsSpec struct {
	RequiredPkgManager string
	Binaries           []DepCheck
	Modules            []DepCheck
	Packages           []string
	PreferModprobe     bool
}

// Profile предоставляет команды, пути и правила зависимостей для конкретной ОС/init.
type Profile interface {
	Name() string
	InitSystem() platform.InitSystem
	OpenRCActive() bool

	NativeZapret(p paths.Paths) NativeZapretSpec
	ValidateUpstreamZapret(zapretRoot string) bool

	ServiceScriptPath(unit string, p paths.Paths) string
	ControlCmd(unit, action string, p paths.Paths) CmdSpec
	QueryCmd(unit string, p paths.Paths) CmdSpec
	EnableOps(unit string, p paths.Paths) []CommandOp
	DisableOps(unit string, p paths.Paths) []CommandOp
	PostUninstallOps(unit string, p paths.Paths) []CommandOp

	FirewallReloadCmd(p paths.Paths, root string) (CmdSpec, bool)
	CustomDFlavors(zapretRoot string) []string

	PkgManagerOrder() []string
	PkgUpdateCmd(mgr, binPath string) (CmdSpec, bool)
	PkgInstallCmd(mgr, binPath string, pkgs []string) CmdSpec
	RequiredDeps(fw platform.FWType) DepsSpec
	ModuleLoadCmds(modName, kernelRel, root string, findBin func(string) string) []CmdSpec
}

// Package paths описывает раскладку файлов zator и zapret2 на устройстве.
package paths

import (
	"os"
	"path/filepath"
)

const (
	// DefaultZatorRoot — корень zator по умолчанию.
	DefaultZatorRoot = "/opt/zator"
	// DefaultZapretRoot — корень zapret2 по умолчанию.
	DefaultZapretRoot = "/opt/zapret2"

	// EnvZatorRoot переопределяет корень zator.
	EnvZatorRoot = "ZATOR_ROOT"
	// EnvZapretRoot переопределяет корень zapret2.
	EnvZapretRoot = "ZAPRET2_ROOT"

	// ConfigFileName — имя файла конфигурации zator.
	ConfigFileName = "zator.json"
)

// Paths содержит все каталоги и ключевые файлы zator.
type Paths struct {
	ZatorRoot  string
	ZapretRoot string

	BinDir    string
	WebUIDir  string
	LuaDir    string
	ListsDir  string
	FakeDir   string
	DataDir   string
	StateDir  string
	ConfigDir string

	// OrchestraDir — legacy-каталог /opt/zator/orchestra (создаётся при install).
	// Канон локов и overrides: StateOrchestraDir() = state/orchestra.
	OrchestraDir string

	// ExtraStratsDir — пользовательские списки стратегий (TCP_Custom.txt и т.п.).
	ExtraStratsDir string

	// BackupDir — каталог архивов бэкапа (рядом с ZatorRoot, переживает переустановку).
	BackupDir string

	// ZapretConfig — файл конфигурации zapret2 (ZapretRoot/config).
	ZapretConfig string

	// ZatorConfig — файл конфигурации zator (ConfigDir/zator.json).
	ZatorConfig string

	// Файлы состояния orchestra.
	LockedTSV       string
	LockedManualTSV string
}

// New строит раскладку для заданных корней. Пустые значения заменяются
// значениями по умолчанию.
func New(zatorRoot, zapretRoot string) Paths {
	if zatorRoot == "" {
		zatorRoot = DefaultZatorRoot
	}
	if zapretRoot == "" {
		zapretRoot = DefaultZapretRoot
	}
	zatorRoot = filepath.Clean(zatorRoot)
	zapretRoot = filepath.Clean(zapretRoot)

	stateDir := filepath.Join(zatorRoot, "state")
	configDir := filepath.Join(zatorRoot, "config")
	orchestraDir := filepath.Join(zatorRoot, "orchestra")

	return Paths{
		ZatorRoot:  zatorRoot,
		ZapretRoot: zapretRoot,

		BinDir:       filepath.Join(zatorRoot, "bin"),
		WebUIDir:     filepath.Join(zatorRoot, "webui"),
		LuaDir:       filepath.Join(zatorRoot, "lua"),
		ListsDir:     filepath.Join(zatorRoot, "lists"),
		FakeDir:      filepath.Join(zatorRoot, "files", "fake"),
		DataDir:      filepath.Join(zatorRoot, "data"),
		StateDir:     stateDir,
		ConfigDir:    configDir,
		OrchestraDir: orchestraDir,

		ExtraStratsDir: filepath.Join(zatorRoot, "extra_strats"),
		BackupDir:      zatorRoot + "_backup",

		ZapretConfig: filepath.Join(zapretRoot, "config"),
		ZatorConfig:  filepath.Join(configDir, ConfigFileName),

		LockedTSV:       filepath.Join(stateDir, "orchestra", "locked.tsv"),
		LockedManualTSV: filepath.Join(stateDir, "orchestra", "locked.manual.tsv"),
	}
}

// FromEnv строит раскладку с учётом переменных окружения
// ZATOR_ROOT и ZAPRET2_ROOT.
func FromEnv() Paths {
	return New(os.Getenv(EnvZatorRoot), os.Getenv(EnvZapretRoot))
}

// Default возвращает раскладку по умолчанию (с учётом переменных окружения).
func Default() Paths { return FromEnv() }

// StateOrchestraDir возвращает каталог состояния orchestra.
func (p Paths) StateOrchestraDir() string {
	return filepath.Join(p.StateDir, "orchestra")
}

// Dirs возвращает список каталогов, которые необходимо создать при установке.
func (p Paths) Dirs() []string {
	return []string{
		p.ZatorRoot, p.BinDir, p.WebUIDir, p.LuaDir, p.ListsDir, p.FakeDir,
		p.DataDir, p.StateDir, p.StateOrchestraDir(), p.ConfigDir, p.OrchestraDir, p.ExtraStratsDir,
	}
}

// EnsureDirs создаёт все каталоги раскладки.
func (p Paths) EnsureDirs() error {
	for _, d := range p.Dirs() {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Installed сообщает, установлен ли zator (существует файл конфигурации
// или исполняемый файл в BinDir).
func (p Paths) Installed() bool {
	if fileExists(p.ZatorConfig) {
		return true
	}
	return fileExists(filepath.Join(p.BinDir, "zator")) ||
		fileExists(filepath.Join(p.BinDir, "zator.exe"))
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

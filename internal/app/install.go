package app

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AloofLibra/zator"
	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/deploy"
	"github.com/AloofLibra/zator/internal/telemetry"
	"github.com/AloofLibra/zator/internal/uiembed"
)

// Install создаёт раскладку каталогов, распаковывает встроенные ресурсы
// и записывает текущую a.Config (пароль/auth задаёт вызывающий код).
// Вызывающий код должен при необходимости предварительно вызвать Load.
// На роутере сразу ставит системные зависимости (iptables/ipset/модули).
func (a *App) Install() error {
	if err := a.Paths.EnsureDirs(); err != nil {
		return fmt.Errorf("создание каталогов: %w", err)
	}
	if err := a.Config.Validate(); err != nil {
		return err
	}
	if err := a.syncAssets(); err != nil {
		return err
	}
	if err := a.recordInstalledVersion(); err != nil {
		return err
	}
	if err := a.Save(); err != nil {
		return err
	}
	// Best-effort: ставим iptables/ipset сразу. Жёсткая проверка — в InstallZapret.
	if isRouterHost() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		_, _ = a.EnsureDependencies(ctx)
	}
	return nil
}

// Update обновляет встроенные ресурсы, сохраняя существующую конфигурацию
// (пароль, auth_enabled, legacy token не трогаются).
// На роутере повторно ставит iptables/ipset и подтягивает IFACE.
func (a *App) Update() error {
	if !a.Installed() {
		return fmt.Errorf("zator не установлен в %s", a.Paths.ZatorRoot)
	}
	if err := a.Paths.EnsureDirs(); err != nil {
		return err
	}
	if err := a.Load(); err != nil {
		return err
	}
	if err := a.syncAssets(); err != nil {
		return err
	}
	if err := a.recordInstalledVersion(); err != nil {
		return err
	}
	if err := a.Save(); err != nil {
		return err
	}
	if isRouterHost() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		_, _ = a.EnsureDependencies(ctx)
		_, _, _ = a.ApplyDetectedIfaces()
	}
	return nil
}

func (a *App) recordInstalledVersion() error {
	v, err := deploy.ReadVersion(a.Paths)
	if err != nil {
		return err
	}
	version := zator.Version()
	now := time.Now().UTC().Format("2006-01-02 15:04")
	if v.ZatorVersion == "" {
		v.ZatorVersion = version
	}
	if v.ZatorDate == "" {
		v.ZatorDate = now
	}
	if v.WebUIVersion == "" {
		v.WebUIVersion = version
	}
	if v.WebUIDate == "" {
		v.WebUIDate = now
	}
	if v.ConfigVersion == "" {
		v.ConfigVersion = version
	}
	if v.Tracking == "" {
		v.Tracking = deploy.TrackingLatest
	}
	return deploy.WriteVersion(a.Paths, v)
}

// UninstallOptions — поведение удаления.
type UninstallOptions struct {
	// Purge удаляет весь ZatorRoot (включая config/state/lists).
	Purge bool
	// SkipService не трогает unit-файлы служб.
	SkipService bool
}

// Uninstall снимает runtime. Без Purge сохраняет config, state, lists, extra_strats.
// Каталог zapret2 не затрагивается.
func (a *App) Uninstall(opts UninstallOptions) error {
	root := a.Paths.ZatorRoot
	if root == "" || root == "/" || root == filepath.VolumeName(root)+string(filepath.Separator) {
		return fmt.Errorf("небезопасный корневой каталог: %q", root)
	}
	if !opts.SkipService {
		if err := a.StopAndUninstallServices(); err != nil {
			return fmt.Errorf("остановка и отключение служб: %w", err)
		}
	}
	if opts.Purge {
		return os.RemoveAll(root)
	}
	return a.removeRuntime()
}

// removeRuntime удаляет бинарник и встроенные ассеты, оставляя пользовательские данные.
// FakeDir (files/fake) сохраняем: там и стоковые blob, и загруженные пользователем.
func (a *App) removeRuntime() error {
	for _, p := range []string{
		a.Paths.BinDir,
		a.Paths.WebUIDir,
		a.Paths.LuaDir,
		a.Paths.DataDir,
		filepath.Join(a.Paths.ZatorRoot, "blockcheck2.d"),
		filepath.Join(a.Paths.ZatorRoot, "tools", "bin", "googlevideo-dns-sniffer"),
		filepath.Join(a.Paths.ExtraStratsDir, "cache"),
		a.Paths.OrchestraDir,
	} {
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("удаление %s: %w", p, err)
		}
	}
	return nil
}

func (a *App) syncAssets() error {
	if err := a.Paths.EnsureDirs(); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(a.Paths.WebUIDir, "index.html")); err != nil {
		if err := a.copyDevelopmentAssets(); err != nil {
			return fmt.Errorf("WebUI-ресурсы отсутствуют; сначала установите app archive: %w", err)
		}
	}
	if _, err := os.Stat(filepath.Join(a.Paths.LuaDir, "wssize-ramp.lua")); err != nil {
		return fmt.Errorf("config-ресурсы отсутствуют; сначала установите config archive")
	}
	if data, err := uiembed.ConfigDefaultAt(a.Paths); err == nil {
		if err := a.seedZapretConfig(data); err != nil {
			return err
		}
	}
	if err := a.migrateLegacyTelemetry(); err != nil {
		return err
	}
	return a.ensureOrchestraState()
}

// copyDevelopmentAssets supports local builds from a source checkout. Release
// binaries do not contain these files and are supplied separate resource archives.
func (a *App) copyDevelopmentAssets() error {
	root := uiembed.SourceAssetsRoot()
	if _, err := os.Stat(filepath.Join(root, "webui", "index.html")); err != nil {
		return err
	}
	copyTree := func(src, dst string) error { return copyFS(os.DirFS(filepath.Join(root, src)), dst) }
	for _, item := range [][2]string{
		{"webui", a.Paths.WebUIDir}, {"lua", a.Paths.LuaDir},
		{"orchestra", filepath.Join(a.Paths.ExtraStratsDir, "cache", "orchestra")},
		{"data", a.Paths.DataDir}, {"files", filepath.Join(a.Paths.ZatorRoot, "files")},
		{"init", filepath.Join(a.Paths.ZatorRoot, "init")}, {"tools", filepath.Join(a.Paths.ZatorRoot, "tools")},
	} {
		if err := copyTree(item[0], item[1]); err != nil {
			return err
		}
	}
	if err := copyFSKeepExisting(os.DirFS(filepath.Join(root, "lists")), a.Paths.ListsDir); err != nil {
		return err
	}
	if err := copyFSKeepExisting(os.DirFS(filepath.Join(root, "extra_strats")), a.Paths.ExtraStratsDir); err != nil {
		return err
	}
	if err := copyFSKeepCustomTLS(os.DirFS(filepath.Join(root, "files", "fake")), a.Paths.FakeDir); err != nil {
		return err
	}
	if err := copyTree("blockcheck2", filepath.Join(a.Paths.ZapretRoot, "blockcheck2.d")); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "config.default"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(a.Paths.ConfigDir, "config.default"), data, 0o644)
}

func (a *App) migrateLegacyTelemetry() error {
	c := telemetry.New(a.Paths)
	current, err := os.Stat(c.ConfigPath())
	if err == nil && current.Size() > 0 {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	legacyPath := filepath.Join(a.Paths.ZatorRoot, "z2r_lib", telemetry.ConfigFileName)
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	settings := telemetry.ParseSettings(string(data))
	if !settings.Decided && settings.UUID == "" && settings.ChannelID == "" {
		return nil
	}
	return c.SaveSettings(settings)
}

// copyFSKeepExisting копирует файлы, не затирая уже существующие
// (netrogat.txt и пользовательские списки).
func copyFSKeepExisting(src fs.FS, dst string) error {
	return fs.WalkDir(src, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		data, err := fs.ReadFile(src, name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(strings.ToLower(name), ".sh") {
			mode = 0o755
		}
		return os.WriteFile(target, data, mode)
	})
}

// seedZapretConfig записывает ZapretConfig из шаблона, если config отсутствует
// и каталог zapret2 существует.
func (a *App) seedZapretConfig(template []byte) error {
	cfg := a.Paths.ZapretConfig
	if cfg == "" {
		return nil
	}
	if _, err := os.Stat(cfg); err == nil {
		return nil
	}
	if st, err := os.Stat(a.Paths.ZapretRoot); err != nil || !st.IsDir() {
		return nil
	}
	if err := os.WriteFile(cfg, template, 0o644); err != nil {
		return fmt.Errorf("запись %s: %w", cfg, err)
	}
	if c, err := config.Load(cfg); err == nil {
		changed, e := a.ensureNetworkIfaces(c)
		if e != nil {
			// На сборочном хосте (Windows) маршрутов роутера нет — не валим install.
			if !isRouterHost() {
				return nil
			}
			return fmt.Errorf("автоопределение IFACE_WAN/LAN: %w", e)
		}
		if changed {
			if err := c.Save(); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureOrchestraState создаёт каталоги и пустые файлы состояния orchestra
// (locked.tsv, locked.manual.tsv), чтобы Lua и WebUI видели их с первого запуска.
func (a *App) ensureOrchestraState() error {
	dir := a.Paths.StateOrchestraDir()
	if err := os.MkdirAll(filepath.Join(dir, "scopes"), 0o755); err != nil {
		return fmt.Errorf("каталог orchestra: %w", err)
	}
	for _, p := range []string{a.Paths.LockedTSV, a.Paths.LockedManualTSV, a.Paths.AutoLockedTSV} {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("файл состояния %s: %w", p, err)
		}
		f.Close()
	}
	return nil
}

// migrateOrchestraCacheToState переносит локи из старого пути
// extra_strats/cache/orchestra → state/orchestra (канон для Go и Lua).
func (a *App) migrateOrchestraCacheToState() error {
	oldDir := filepath.Join(a.Paths.ExtraStratsDir, "cache", "orchestra")
	newDir := a.Paths.StateOrchestraDir()
	if err := os.MkdirAll(filepath.Join(newDir, "scopes"), 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(oldDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if name == "locked.lua" || strings.HasSuffix(name, ".lua") {
			continue
		}
		src := filepath.Join(oldDir, name)
		dst := filepath.Join(newDir, name)
		if e.IsDir() {
			if name != "scopes" {
				continue
			}
			if err := copyDirKeepExisting(src, dst); err != nil {
				return err
			}
			continue
		}
		// Пустой/нулевой state (от ensureOrchestraState) не блокирует перенос legacy.
		if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
			continue // непустое state — не затираем
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			continue
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func copyDirKeepExisting(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDirKeepExisting(s, d); err != nil {
				return err
			}
			continue
		}
		if st, err := os.Stat(d); err == nil && st.Size() > 0 {
			continue
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		data, err := os.ReadFile(s)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			continue
		}
		if err := os.WriteFile(d, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// EnsureStockFakes распаковывает встроенные blob в FakeDir. Пользовательский
// custom_tls.bin сохраняется при обновлении, как в Bash-версии.
func (a *App) EnsureStockFakes() error {
	return os.MkdirAll(a.Paths.FakeDir, 0o755)
}

func copyFSKeepCustomTLS(src fs.FS, dst string) error {
	return fs.WalkDir(src, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if filepath.ToSlash(name) == "custom_tls.bin" {
			if _, err := os.Stat(target); err == nil {
				return nil
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		data, err := fs.ReadFile(src, name)
		if err != nil {
			return err
		}
		if current, err := os.ReadFile(target); err == nil && bytes.Equal(current, data) {
			return nil // avoid flash writes for unchanged embedded assets
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(strings.ToLower(name), ".sh") {
			mode = 0o755
		}
		return os.WriteFile(target, data, mode)
	})
}

// copyFS копирует содержимое src в каталог dst.
func copyFS(src fs.FS, dst string) error {
	return fs.WalkDir(src, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(src, name)
		if err != nil {
			return err
		}
		if current, err := os.ReadFile(target); err == nil && bytes.Equal(current, data) {
			return nil // avoid flash writes for unchanged embedded assets
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(strings.ToLower(name), ".sh") {
			mode = 0o755
		}
		return os.WriteFile(target, data, mode)
	})
}

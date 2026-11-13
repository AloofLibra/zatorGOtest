package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AloofLibra/zator"
	"github.com/AloofLibra/zator/internal/deploy"
	"github.com/AloofLibra/zator/internal/uiembed"
)

// Install создаёт раскладку каталогов, распаковывает встроенные ресурсы
// и записывает текущую a.Config (с новым токеном, если его ещё нет).
// Вызывающий код должен при необходимости предварительно вызвать Load.
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
	// Токен опционален: по умолчанию Web UI открыт без авторизации.
	// Задайте token в zator.json и перезапустите webui, если нужна защита.
	_ = deploy.WriteVersion(a.Paths, deploy.Version{
		ZatorVersion: zator.Version(),
		ZatorDate:    time.Now().UTC().Format("2006-01-02 15:04"),
		Tracking:     "latest",
	})
	return a.Save()
}

// Update обновляет встроенные ресурсы, сохраняя существующую конфигурацию.
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
	// Web UI по умолчанию без токена: снимаем ранее автосгенерированный.
	a.ClearToken()
	if err := a.syncAssets(); err != nil {
		return err
	}
	return a.Save()
}

// Uninstall удаляет каталог zator целиком. Каталог zapret2 не затрагивается.
func (a *App) Uninstall() error {
	root := a.Paths.ZatorRoot
	if root == "" || root == "/" || root == filepath.VolumeName(root)+string(filepath.Separator) {
		return fmt.Errorf("небезопасный корневой каталог: %q", root)
	}
	return os.RemoveAll(root)
}

func (a *App) syncAssets() error {
	if err := copyFS(uiembed.WebUI(), a.Paths.WebUIDir); err != nil {
		return fmt.Errorf("распаковка Web UI: %w", err)
	}
	if err := copyFS(uiembed.Lua(), a.Paths.LuaDir); err != nil {
		return fmt.Errorf("распаковка lua: %w", err)
	}
	// strategy-validator.sh должен быть исполняемым (служба zator-strategy-validator).
	_ = os.Chmod(filepath.Join(a.Paths.LuaDir, "strategy-validator.sh"), 0o755)
	if err := copyFS(uiembed.Orchestra(), filepath.Join(a.Paths.ExtraStratsDir, "cache", "orchestra")); err != nil {
		return fmt.Errorf("распаковка orchestra: %w", err)
	}
	// locked.lua должен лежать в /opt/zator/lua (пути в config.default).
	if err := copyFSKeepExisting(uiembed.Orchestra(), a.Paths.LuaDir); err != nil {
		return fmt.Errorf("locked.lua → lua: %w", err)
	}
	if err := copyFSKeepExisting(uiembed.Lists(), a.Paths.ListsDir); err != nil {
		return fmt.Errorf("распаковка lists: %w", err)
	}
	if err := copyFS(uiembed.Data(), a.Paths.DataDir); err != nil {
		return fmt.Errorf("распаковка data: %w", err)
	}
	if err := copyFS(uiembed.Files(), filepath.Join(a.Paths.ZatorRoot, "files")); err != nil {
		return fmt.Errorf("распаковка files: %w", err)
	}
	if err := copyFSKeepExisting(uiembed.ExtraStrats(), a.Paths.ExtraStratsDir); err != nil {
		return fmt.Errorf("распаковка extra_strats: %w", err)
	}
	if data, err := uiembed.ConfigDefault(); err == nil {
		dst := filepath.Join(a.Paths.ConfigDir, "config.default")
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("запись config.default: %w", err)
		}
		if err := a.seedZapretConfig(data); err != nil {
			return err
		}
	}
	return a.ensureOrchestraState()
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
	return nil
}

// ensureOrchestraState создаёт каталоги и пустые файлы состояния orchestra
// (locked.tsv, locked.manual.tsv), чтобы Lua и WebUI видели их с первого запуска.
func (a *App) ensureOrchestraState() error {
	dir := a.Paths.StateOrchestraDir()
	if err := os.MkdirAll(filepath.Join(dir, "scopes"), 0o755); err != nil {
		return fmt.Errorf("каталог orchestra: %w", err)
	}
	for _, p := range []string{a.Paths.LockedTSV, a.Paths.LockedManualTSV} {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("файл состояния %s: %w", p, err)
		}
		f.Close()
	}
	return nil
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
		mode := os.FileMode(0o644)
		if strings.HasSuffix(strings.ToLower(name), ".sh") {
			mode = 0o755
		}
		return os.WriteFile(target, data, mode)
	})
}

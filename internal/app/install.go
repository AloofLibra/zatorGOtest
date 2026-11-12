package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/AloofLibra/zator/internal/uiembed"
)

// Install создаёт раскладку каталогов, распаковывает встроенные ресурсы
// и записывает текущую a.Config (с новым токеном, если его ещё нет).
func (a *App) Install() error {
	if err := a.Paths.EnsureDirs(); err != nil {
		return fmt.Errorf("создание каталогов: %w", err)
	}
	if err := a.Config.Validate(); err != nil {
		return err
	}
	_ = a.InstallSelfBinary()
	if err := a.syncAssets(); err != nil {
		return err
	}
	if err := a.EnsureToken(); err != nil {
		return err
	}
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
	_ = a.InstallSelfBinary()
	return a.syncAssets()
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
	orchDir := filepath.Join(a.Paths.ExtraStratsDir, "cache", "orchestra")
	if err := copyFS(uiembed.Orchestra(), orchDir); err != nil {
		return fmt.Errorf("распаковка orchestra: %w", err)
	}
	// config.default ожидает /opt/zator/lua/locked.lua
	if data, err := fs.ReadFile(uiembed.Orchestra(), "locked.lua"); err == nil {
		_ = os.WriteFile(filepath.Join(a.Paths.LuaDir, "locked.lua"), data, 0o644)
	}
	if err := copyFSKeepExisting(uiembed.Lists(), a.Paths.ListsDir); err != nil {
		return fmt.Errorf("распаковка lists: %w", err)
	}
	if err := copyFSKeepExisting(uiembed.ExtraStrats(), a.Paths.ExtraStratsDir); err != nil {
		return fmt.Errorf("распаковка extra_strats: %w", err)
	}
	if err := a.linkFlatHostlists(); err != nil {
		return err
	}
	if err := copyFS(uiembed.Data(), a.Paths.DataDir); err != nil {
		return fmt.Errorf("распаковка data: %w", err)
	}
	if err := copyFS(uiembed.Fake(), a.Paths.FakeDir); err != nil {
		return fmt.Errorf("распаковка fake: %w", err)
	}
	// config ссылается на /opt/zator/files/fake/...
	filesFake := filepath.Join(a.Paths.ZatorRoot, "files", "fake")
	if err := copyFS(uiembed.Fake(), filesFake); err != nil {
		return fmt.Errorf("распаковка files/fake: %w", err)
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

// linkFlatHostlists создаёт имена файлов, которые ждёт config.default.
func (a *App) linkFlatHostlists() error {
	pairs := []struct{ src, dst string }{
		{"TCP/RKN/List.txt", "TCP_RKN_list.txt"},
		{"TCP/RKN/Custom.txt", "TCP_Custom.txt"},
		{"TCP/RKN/Discord.txt", "TCP_Discord.txt"},
		{"TCP/YT/List.txt", "TCP_YT_list.txt"},
		{"UDP/YT/List.txt", "UDP_YT_list.txt"},
	}
	for _, p := range pairs {
		src := filepath.Join(a.Paths.ExtraStratsDir, filepath.FromSlash(p.src))
		dst := filepath.Join(a.Paths.ExtraStratsDir, p.dst)
		if _, err := os.Stat(src); err != nil {
			// создаём пустой dst, чтобы nfqws2 не падал на open()
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			f.Close()
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

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
		return os.WriteFile(target, data, 0o644)
	})
}

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

func (a *App) ensureOrchestraState() error {
	orch := a.Paths.StateOrchestraDir()
	if err := os.MkdirAll(filepath.Join(orch, "scopes"), 0o755); err != nil {
		return fmt.Errorf("каталог orchestra: %w", err)
	}
	_ = os.MkdirAll(a.Paths.HistoryOrchestraDir(), 0o755)
	for _, p := range []string{a.Paths.LockedTSV, a.Paths.LockedManualTSV} {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("файл состояния %s: %w", p, err)
		}
		f.Close()
	}
	return nil
}

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
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

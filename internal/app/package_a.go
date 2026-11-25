package app

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/platform"
	"github.com/AloofLibra/zator/internal/uiembed"
)

// NfqwsWantRunningPath — флаг «пользователь хочет nfqws2 запущенным» для Entware-watchdog.
func (a *App) NfqwsWantRunningPath() string {
	return filepath.Join(a.Paths.StateDir, "nfqws2.want_running")
}

// migratePackageA приводит live-конфиг к пакету A (Keenetic-hardened):
//   - снимает legacy shell-if у WSSIZE;
//   - включает WSSIZE lite только если у legacy config ещё нет пользовательского значения;
//   - синхронизирует ARGS/SKIP;
//   - profile 2 остаётся на circular_locked, получает soft_success_in=0 и
//     может применять SNI-локи, если conntrack metadata отсутствует.
//
// Явное ENABLE=0 и режим пользователя сохраняются при каждом вызове.
func (a *App) migratePackageA(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	changed := false
	if cfg.StripWssizeShellIf() {
		changed = true
	}

	if _, ok := cfg.Get("Z2R_WSSIZE_RAMP_ENABLE"); !ok {
		if err := cfg.Set("Z2R_WSSIZE_RAMP_ENABLE", "1"); err == nil {
			changed = true
		}
	}
	if _, ok := cfg.Get("Z2R_WSSIZE_RAMP_MODE"); !ok {
		if err := cfg.Set("Z2R_WSSIZE_RAMP_MODE", "lite"); err == nil {
			changed = true
		}
	}

	if ok, err := cfg.SyncWssizeRampVars(); err == nil && ok {
		changed = true
	}
	if ok, err := cfg.EnsureProfile2SoftSuccess(); err == nil && ok {
		changed = true
	}
	if ok, err := cfg.EnsureProfile2AllowNoHost(); err == nil && ok {
		changed = true
	}
	return changed
}

// ApplyZapretRuntime — единый pipeline: migrate → SyncWssize → Save →
// WriteNfqws2Args → SyncCustomD. NFQUEUE/firewall rules belong to native init.
// Не затирает живой config при
// битом NFQWS2_OPT (в отличие от старого wipe-fallback).
func (a *App) ApplyZapretRuntime() (fwHook string, err error) {
	cfgPath := a.Paths.ZapretConfig
	if err := os.MkdirAll(a.Paths.ZapretRoot, 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(cfgPath); err != nil {
		data, e := uiembed.ConfigDefaultAt(a.Paths)
		if e != nil {
			return "", fmt.Errorf("нет %s и нет встроенного шаблона", cfgPath)
		}
		if e := a.seedZapretConfig(data); e != nil {
			return "", e
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return "", fmt.Errorf("чтение config: %w", err)
	}

	dirty := false
	if changed, e := a.ensureNetworkIfaces(cfg); e == nil && changed {
		dirty = true
	}
	if a.migrateDNSDesyncOff(cfg) {
		dirty = true
	}
	if a.migratePackageA(cfg) {
		dirty = true
	}
	if changed, e := cfg.EnsureWssizeRampProfile(a.Paths.ZatorRoot); e != nil {
		return "", fmt.Errorf("WSSIZE profile: %w", e)
	} else if changed {
		dirty = true
	}
	wssizeLua := filepath.ToSlash(filepath.Join(a.Paths.LuaDir, "wssize-ramp.lua"))
	if changed, e := cfg.EnsureLuaInit(wssizeLua); e != nil {
		return "", fmt.Errorf("WSSIZE Lua: %w", e)
	} else if changed {
		dirty = true
	}
	gvLua := filepath.ToSlash(filepath.Join(a.Paths.LuaDir, "googlevideo-collect.lua"))
	if ok, e := cfg.EnsureLuaInit(gvLua); e == nil && ok {
		dirty = true
	}
	sniLua := filepath.ToSlash(filepath.Join(a.Paths.LuaDir, "sni-pool.lua"))
	if ok, e := cfg.EnsureLuaInit(sniLua); e == nil && ok {
		dirty = true
	}
	if ok, e := cfg.EnsureSlotBlob("z2r_prof_5", "quic_initial_www_google_com.bin"); e == nil && ok {
		dirty = true
	}
	if ok, e := a.ReapplyConfigOverlays(cfg); e != nil {
		return "", fmt.Errorf("оверлеи блобов и стратегий: %w", e)
	} else if ok {
		dirty = true
	}
	if dirty {
		if err := cfg.Save(); err != nil {
			return "", err
		}
		defPath := filepath.Join(a.Paths.ConfigDir, "config.default")
		if def, e := config.Load(defPath); e == nil {
			_ = a.migratePackageA(def)
			_ = def.Save()
		}
	}

	argsFile := filepath.Join(a.Paths.ZapretRoot, "nfqws2.args")
	zapretLuaDir := filepath.ToSlash(filepath.Join(a.Paths.ZapretRoot, "lua"))
	if err := cfg.WriteNfqws2RuntimeArgs(argsFile, zapretLuaDir, ""); err != nil {
		return "", fmt.Errorf("nfqws2.args: %w (config не затёрт — исправьте NFQWS2_OPT вручную)", err)
	}

	if err := a.removeLegacyQueueHook(); err != nil {
		return "", err
	}
	_ = a.SyncCustomDHooks(cfg)
	_ = a.ensureHostlistPlaceholders()
	_ = a.deployNfqwsWatchdog()
	a.SyncAutoLockedPersist()
	// Package B: подтянуть googlevideo-ipv4 (best-effort, уважает TTL).
	_, _ = a.RefreshGooglevideoIPv4(context.Background(), false)
	return "", nil
}

// removeLegacyQueueHook drops rules created by the old Go-owned queue hook.
func (a *App) removeLegacyQueueHook() error {
	path := filepath.Join(a.Paths.ZapretRoot, "zator-nfqueue.sh")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := exec.LookPath("iptables"); err == nil {
		if _, err := exec.CommandContext(ctx, "iptables", "-t", "mangle", "-S", "ZATOR_NFQ").Output(); err == nil {
			for _, hook := range []string{"POSTROUTING", "PREROUTING"} {
				for i := 0; i < 16; i++ {
					if err := exec.CommandContext(ctx, "iptables", "-t", "mangle", "-D", hook, "-j", "ZATOR_NFQ").Run(); err != nil {
						break
					}
				}
			}
			if out, err := exec.CommandContext(ctx, "iptables", "-t", "mangle", "-F", "ZATOR_NFQ").CombinedOutput(); err != nil {
				return fmt.Errorf("flush legacy NFQUEUE chain: %w: %s", err, strings.TrimSpace(string(out)))
			}
			if out, err := exec.CommandContext(ctx, "iptables", "-t", "mangle", "-X", "ZATOR_NFQ").CombinedOutput(); err != nil {
				return fmt.Errorf("remove legacy NFQUEUE chain: %w: %s", err, strings.TrimSpace(string(out)))
			}
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SetNfqwsWantRunning выставляет/снимает флаг для Entware-watchdog.
func (a *App) SetNfqwsWantRunning(want bool) {
	path := a.NfqwsWantRunningPath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if want {
		_ = fsutil.AtomicWriteFile(path, []byte("1\n"), 0o644)
		return
	}
	_ = os.Remove(path)
}

// NfqwsWatchdogRunning — жив ли процесс watchdog (по pidfile).
func (a *App) NfqwsWatchdogRunning() bool {
	b, err := os.ReadFile("/var/run/zator-nfqws-watchdog.pid")
	if err != nil {
		return false
	}
	pid := strings.TrimSpace(string(b))
	if pid == "" {
		return false
	}
	_, err = os.Stat(filepath.Join("/proc", pid))
	return err == nil
}

// deployNfqwsWatchdog кладёт скрипт watchdog в BinDir (Entware/Keenetic).
func (a *App) deployNfqwsWatchdog() error {
	if a.Platform.InitSystem != platform.InitEntware && a.Platform.Kind != platform.KindEntware {
		return nil
	}
	data, err := fs.ReadFile(uiembed.InitAt(a.Paths), "entware/nfqws-watchdog")
	if err != nil || len(data) == 0 {
		return err
	}
	dst := filepath.Join(a.Paths.BinDir, "zator-nfqws-watchdog")
	if err := os.MkdirAll(a.Paths.BinDir, 0o755); err != nil {
		return err
	}
	if err := fsutil.AtomicWriteFile(dst, data, 0o755); err != nil {
		return err
	}
	link := "/opt/etc/init.d/S91zator-nfqws-watchdog"
	_ = os.MkdirAll(filepath.Dir(link), 0o755)
	_ = os.Remove(link)
	if err := os.Symlink(dst, link); err != nil {
		_ = fsutil.AtomicWriteFile(link, data, 0o755)
	}
	return nil
}

// StartNfqwsWatchdog запускает watchdog после старта zapret2 (best-effort).
func (a *App) StartNfqwsWatchdog() {
	if a.Platform.InitSystem != platform.InitEntware && a.Platform.Kind != platform.KindEntware {
		return
	}
	_ = a.deployNfqwsWatchdog()
	a.SetNfqwsWantRunning(true)
	bin := filepath.Join(a.Paths.BinDir, "zator-nfqws-watchdog")
	if _, err := os.Stat(bin); err != nil {
		return
	}
	// Не блокируем bootstrap: fork через setsid делает сам скрипт.
	_ = runDetached(bin, "start")
}

// StopNfqwsWatchdog снимает want-флаг и останавливает loop.
func (a *App) StopNfqwsWatchdog() {
	a.SetNfqwsWantRunning(false)
	bin := filepath.Join(a.Paths.BinDir, "zator-nfqws-watchdog")
	if _, err := os.Stat(bin); err != nil {
		return
	}
	_ = runDetached(bin, "stop")
}

// WebUIWantRunningPath — флаг «пользователь хочет Web UI» для Entware-watchdog.
func (a *App) WebUIWantRunningPath() string {
	return filepath.Join(a.Paths.StateDir, "webui.want_running")
}

func (a *App) setWebUIWantRunning(want bool) {
	path := a.WebUIWantRunningPath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if want {
		_ = fsutil.AtomicWriteFile(path, []byte("1\n"), 0o644)
		return
	}
	_ = os.Remove(path)
}

func (a *App) deployWebUIWatchdog() error {
	if a.Platform.InitSystem != platform.InitEntware && a.Platform.Kind != platform.KindEntware {
		return nil
	}
	data, err := fs.ReadFile(uiembed.InitAt(a.Paths), "entware/webui-watchdog")
	if err != nil || len(data) == 0 {
		return err
	}
	dst := filepath.Join(a.Paths.BinDir, "zator-webui-watchdog")
	if err := os.MkdirAll(a.Paths.BinDir, 0o755); err != nil {
		return err
	}
	if err := fsutil.AtomicWriteFile(dst, data, 0o755); err != nil {
		return err
	}
	link := "/opt/etc/init.d/S93zator-webui-watchdog"
	_ = os.MkdirAll(filepath.Dir(link), 0o755)
	_ = os.Remove(link)
	if err := os.Symlink(dst, link); err != nil {
		_ = fsutil.AtomicWriteFile(link, data, 0o755)
	}
	return nil
}

// StartWebUIWatchdog поднимает respawn панели (Entware/Keenetic).
func (a *App) StartWebUIWatchdog() {
	if a.Platform.InitSystem != platform.InitEntware && a.Platform.Kind != platform.KindEntware {
		return
	}
	_ = a.deployWebUIWatchdog()
	a.setWebUIWantRunning(true)
	bin := filepath.Join(a.Paths.BinDir, "zator-webui-watchdog")
	if _, err := os.Stat(bin); err != nil {
		return
	}
	_ = runDetached(bin, "start")
}

// StopWebUIWatchdog снимает want и останавливает loop.
func (a *App) StopWebUIWatchdog() {
	a.setWebUIWantRunning(false)
	bin := filepath.Join(a.Paths.BinDir, "zator-webui-watchdog")
	if _, err := os.Stat(bin); err != nil {
		return
	}
	_ = runDetached(bin, "stop")
}

// WebUIWatchdogRunning — жив ли процесс webui-watchdog.
func (a *App) WebUIWatchdogRunning() bool {
	b, err := os.ReadFile("/var/run/zator-webui-watchdog.pid")
	if err != nil {
		return false
	}
	pid := strings.TrimSpace(string(b))
	if pid == "" {
		return false
	}
	_, err = os.Stat(filepath.Join("/proc", pid))
	return err == nil
}

func runDetached(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	return cmd.Start()
}

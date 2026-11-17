package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/firewall"
	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/platform"
	"github.com/AloofLibra/zator/internal/service"
)

// BootstrapOptions — автозапуск служб после установки.
type BootstrapOptions struct {
	// SkipService не ставит и не запускает службы.
	SkipService bool
	// StartWebUI ставит и запускает zator-webui (по умолчанию true).
	StartWebUI *bool
}

func (o BootstrapOptions) webUI() bool {
	if o.StartWebUI == nil {
		return true
	}
	return *o.StartWebUI
}

// InstallSelfBinary копирует текущий исполняемый файл в BinDir/zator.
func (a *App) InstallSelfBinary() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("путь к бинарнику: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		exe, _ = os.Executable()
	}
	dst := filepath.Join(a.Paths.BinDir, "zator")
	if sameFile(exe, dst) {
		return a.EnsureZatorCommand()
	}
	if err := a.Paths.EnsureDirs(); err != nil {
		return err
	}
	in, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		// Windows / busy FS: попробовать перезапись
		_ = os.Remove(dst)
		if err2 := os.Rename(tmp, dst); err2 != nil {
			os.Remove(tmp)
			return err2
		}
	}
	_ = os.Chmod(dst, 0o755)
	return a.EnsureZatorCommand()
}

// EnsureZatorCommand exposes the installed CLI through a system executable directory.
func (a *App) EnsureZatorCommand() error {
	if runtime.GOOS != "linux" {
		return nil
	}
	target := filepath.Join(a.Paths.BinDir, "zator")
	dirs := []string{"/usr/bin"}
	if info, err := os.Stat("/usr/local/bin"); err == nil && info.IsDir() {
		dirs = append([]string{"/usr/local/bin"}, dirs...)
	}
	if info, err := os.Stat("/opt/bin"); err == nil && info.IsDir() {
		dirs = append([]string{"/opt/bin"}, dirs...)
	}
	var lastErr error
	for _, dir := range dirs {
		link := filepath.Join(dir, "zator")
		if resolved, err := filepath.EvalSymlinks(link); err == nil {
			if resolved == target {
				return nil
			}
			lastErr = fmt.Errorf("%s уже указывает на %s", link, resolved)
			continue
		}
		if _, err := os.Lstat(link); err == nil {
			lastErr = fmt.Errorf("%s уже существует", link)
			continue
		} else if !os.IsNotExist(err) {
			lastErr = err
			continue
		}
		if err := os.Symlink(target, link); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		return fmt.Errorf("не найдена подходящая системная папка для команды zator")
	}
	return fmt.Errorf("не удалось добавить zator в PATH (/opt/bin, /usr/local/bin, /usr/bin): %w", lastErr)
}

func sameFile(a, b string) bool {
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// PrepareZapretRuntime готовит config → nfqws2.args и firewall-хук.
// Делегирует пакету A (единый Apply: migrate WSSIZE/profile2 + args + custom.d).
func (a *App) PrepareZapretRuntime() (fwHook string, err error) {
	return a.ApplyZapretRuntime()
}

// SyncCustomDHooks пишет wssize + client-scope хуки в init.d/*/custom.d (sysv и openwrt).
func (a *App) SyncCustomDHooks(cfg *config.Config) error {
	if cfg == nil {
		var err error
		cfg, err = config.Load(a.Paths.ZapretConfig)
		if err != nil {
			return err
		}
	}
	root := a.Paths.ZatorRoot
	wcfg, err := firewall.WssizeConfigFromGetter(cfg.Get, root)
	if err != nil {
		return err
	}
	wscript, err := firewall.RenderWssizeHook(wcfg, root)
	if err != nil {
		return err
	}
	ccfg, err := firewall.ClientScopeConfigFromGetter(cfg.Get, root)
	if err != nil {
		return err
	}
	cscript, err := firewall.RenderClientScopeHook(ccfg)
	if err != nil {
		return err
	}
	for _, flavor := range []firewall.CustomDFlavor{firewall.CustomDSysV, firewall.CustomDOpenWrt} {
		if _, err := firewall.WriteCustomD(a.Paths.ZapretRoot, flavor, firewall.CustomDWssizeName, wscript); err != nil {
			return err
		}
		if _, err := firewall.WriteCustomD(a.Paths.ZapretRoot, flavor, firewall.CustomDClientScopeName, cscript); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) ensureHostlistPlaceholders() error {
	files := []string{
		filepath.Join(a.Paths.ExtraStratsDir, "TCP_RKN_list.txt"),
		filepath.Join(a.Paths.ExtraStratsDir, "TCP_Custom.txt"),
		filepath.Join(a.Paths.ExtraStratsDir, "TCP_Discord.txt"),
		filepath.Join(a.Paths.ExtraStratsDir, "TCP_YT_list.txt"),
		filepath.Join(a.Paths.ExtraStratsDir, "UDP_YT_list.txt"),
		filepath.Join(a.Paths.ListsDir, "netrogat.txt"),
		filepath.Join(a.Paths.ListsDir, "netrogat_substrings.txt"),
		filepath.Join(a.Paths.ListsDir, "googlevideo-ipv4.txt"),
	}
	for _, p := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if _, err := os.Stat(p); err == nil {
			continue
		}
		if err := fsutil.AtomicWriteFile(p, []byte{}, 0o644); err != nil {
			return err
		}
	}
	return nil
}

const dnsDesyncOffMarker = "dns_desync_default_off.v1"

// migrateDNSDesyncOff один раз выключает антиспуф DNS на обновлениях со старым
// небезопасным дефолтом. Маркер в state/ не даёт повторно гасить ручное включение.
func (a *App) migrateDNSDesyncOff(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	marker := filepath.Join(a.Paths.StateDir, dnsDesyncOffMarker)
	if _, err := os.Stat(marker); err == nil {
		return false
	}
	_ = os.MkdirAll(a.Paths.StateDir, 0o755)
	changed := false
	if cfg.DNSDesyncText() == "Включен" {
		if err := cfg.SetDNSDesync(false); err == nil {
			changed = true
		}
	}
	_ = fsutil.AtomicWriteFile(marker, []byte("1\n"), 0o644)
	return changed
}

// StartServices ставит в автозагрузку и запускает zapret2 и Web UI.
// Ошибки prepare/zapret не блокируют установку Web UI: панель должна
// подняться даже при битом /opt/zapret2/config после wipe.
func (a *App) StartServices(ctx context.Context, opts BootstrapOptions) error {
	if opts.SkipService {
		return nil
	}
	if err := a.InstallSelfBinary(); err != nil {
		// не фатально на Windows-сборочной машине
		_ = err
	}
	// На Windows службы роутера недоступны — только подготовка runtime (для тестов/сборки).
	if runtime.GOOS == "windows" {
		_, _ = a.PrepareZapretRuntime()
		return nil
	}
	// Повторная страховка: зависимости + IFACE (если ставили без InstallZapret).
	if rep, err := a.EnsureDependencies(ctx); err != nil {
		_ = rep // отчёт уже мог быть напечатан в InstallZapret; здесь не блокируем Web UI
	} else {
		_, _, _ = a.ApplyDetectedIfaces()
	}
	fwHook, prepErr := a.PrepareZapretRuntime()
	mgr, err := service.New(service.Config{
		Paths:    a.Paths,
		Platform: a.Platform,
		FWHook:   fwHook,
		ZatorBin: filepath.Join(a.Paths.BinDir, "zator"),
		ArgsFile: filepath.Join(a.Paths.ZapretRoot, "nfqws2.args"),
	})
	if err != nil {
		return fmt.Errorf("службы: %w", err)
	}

	var soft, hard []string
	if prepErr != nil {
		soft = append(soft, "prepare zapret: "+prepErr.Error())
	}

	zapretUnits := []service.Unit{service.UnitZapret2, service.UnitValidator}
	if prepErr == nil {
		upstreamZapretInit := filepath.Join(a.Paths.ZapretRoot, "init.d", "openwrt", "zapret2")
		systemZapretInit := filepath.Join("/etc/init.d", string(service.UnitZapret2))
		var registerZapretErr error
		if a.Platform.Kind == platform.KindOpenWRT && a.Platform.InitSystem == platform.InitProcd {
			registerZapretErr = registerOpenWrtZapretInit(upstreamZapretInit, systemZapretInit)
			if registerZapretErr != nil {
				soft = append(soft, "register upstream OpenWrt zapret2 init: "+registerZapretErr.Error())
			} else {
				if err := cleanupOpenWrtInitLinks(systemZapretInit, "/etc/rc.d"); err != nil {
					soft = append(soft, "cleanup OpenWrt zapret2 init links: "+err.Error())
				}
				if err := enableOpenWrtZapretInit(ctx, systemZapretInit); err != nil {
					soft = append(soft, "enable upstream OpenWrt zapret2 init: "+err.Error())
				}
			}
		}
		for _, u := range zapretUnits {
			if u == service.UnitZapret2 && registerZapretErr != nil {
				continue
			}
			// Сохраняем upstream init только если он действительно подключён в
			// /etc/init.d; одного исходного файла в каталоге zapret2 недостаточно.
			preserveOpenWrtInit := u == service.UnitZapret2 && preserveOpenWrtZapretInit(
				a.Platform,
				upstreamZapretInit,
				systemZapretInit,
			)
			if !preserveOpenWrtInit {
				if err := mgr.Install(ctx, u); err != nil {
					soft = append(soft, fmt.Sprintf("install %s: %v", u, err))
					continue
				}
			}
			if err := mgr.Restart(ctx, u); err != nil {
				soft = append(soft, fmt.Sprintf("start %s: %v", u, err))
			}
		}
		time.Sleep(800 * time.Millisecond)
		if st, err := mgr.Status(ctx, service.UnitZapret2); err == nil && !st.Running {
			soft = append(soft, "nfqws2 не запущен после start (проверьте /opt/zapret2/nfqws2.args и логи)")
		} else {
			if err := a.EnsureFirewallApplied(ctx); err != nil {
				soft = append(soft, "fw ensure: "+err.Error())
			}
			if err := a.ApplyWssizeFirewall(ctx); err != nil {
				soft = append(soft, "WSSIZE firewall: "+err.Error())
			}
			a.SyncAutoLockedPersist()
			a.StartNfqwsWatchdog()
		}
	}

	if opts.webUI() {
		u := service.UnitWebUI
		if err := mgr.Install(ctx, u); err != nil {
			hard = append(hard, fmt.Sprintf("install %s: %v", u, err))
		} else if err := mgr.Restart(ctx, u); err != nil {
			hard = append(hard, fmt.Sprintf("start %s: %v", u, err))
		} else {
			time.Sleep(500 * time.Millisecond)
			st, err := mgr.Status(ctx, u)
			if err != nil || !st.Running {
				logHint := filepath.ToSlash(filepath.Join(a.Paths.StateDir, string(u)+".log"))
				hard = append(hard, "zator-webui не запущен после start (лог: "+logHint+")")
			} else {
				a.StartWebUIWatchdog()
			}
		}
	}

	if len(hard) > 0 {
		all := append(append([]string{}, hard...), soft...)
		a.WriteServiceWarnings(all)
		return fmt.Errorf("%s", joinErrors(all))
	}
	if len(soft) > 0 {
		// Web UI реально жив — zapret-проблемы не валят install (иначе launcher die).
		a.WriteServiceWarnings(soft)
		return &SoftServiceError{Msgs: soft}
	}
	a.ClearServiceWarnings()
	return nil
}

func preserveOpenWrtZapretInit(pl platform.Platform, upstreamPath, systemPath string) bool {
	if pl.Kind != platform.KindOpenWRT || pl.InitSystem != platform.InitProcd {
		return false
	}
	for _, path := range []string{upstreamPath, systemPath} {
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func enableOpenWrtZapretInit(ctx context.Context, systemPath string) error {
	out, err := service.ExecRunner().Run(ctx, systemPath, "enable")
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func cleanupOpenWrtInitLinks(systemPath, rcDir string) error {
	data, err := os.ReadFile(systemPath)
	if err != nil {
		return err
	}
	start, stop := "", ""
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "START="):
			start = strings.TrimPrefix(t, "START=")
		case strings.HasPrefix(t, "STOP="):
			stop = strings.TrimPrefix(t, "STOP=")
		}
	}
	if stop == "" {
		stop = "10" // rc.common default when an init script omits STOP.
	}
	if !validInitPriority(start) || !validInitPriority(stop) {
		return nil
	}
	startNum, _ := strconv.Atoi(start)
	stopNum, _ := strconv.Atoi(stop)
	expected := map[string]bool{
		"S" + fmt.Sprintf("%02d", startNum) + "zapret2": true,
		"K" + fmt.Sprintf("%02d", stopNum) + "zapret2":  true,
	}
	entries, err := os.ReadDir(rcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if expected[name] {
			continue
		}
		if !(strings.HasSuffix(name, "zapret2") && (strings.HasPrefix(name, "S") || strings.HasPrefix(name, "K"))) {
			continue
		}
		linkPath := filepath.Join(rcDir, name)
		if entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		link, err := os.Readlink(linkPath)
		if err != nil {
			return err
		}
		if filepath.Clean(filepath.Join(rcDir, link)) == filepath.Clean(systemPath) {
			if err := os.Remove(linkPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func validInitPriority(value string) bool {
	if value == "" || len(value) > 2 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func registerOpenWrtZapretInit(upstreamPath, systemPath string) error {
	if !regularFile(upstreamPath) {
		return fmt.Errorf("upstream init not found at %s", upstreamPath)
	}
	info, err := os.Lstat(systemPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			link, readErr := os.Readlink(systemPath)
			if readErr != nil {
				return readErr
			}
			if link == upstreamPath {
				return nil
			}
			if regularFile(systemPath) {
				return nil // preserve a locally managed symlink
			}
			return fmt.Errorf("existing init symlink is broken: %s", systemPath)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		content, readErr := os.ReadFile(systemPath)
		if readErr != nil {
			return readErr
		}
		if !strings.Contains(string(content), "(создано zator)") {
			return nil // preserve a locally managed init script
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	tmp := systemPath + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(upstreamPath, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, systemPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// SoftServiceError — zapret/runtime предупреждения при живом Web UI.
type SoftServiceError struct {
	Msgs []string
}

func (e *SoftServiceError) Error() string { return joinErrors(e.Msgs) }
func (e *SoftServiceError) Soft() bool    { return true }

func joinErrors(errs []string) string {
	out := ""
	for i, e := range errs {
		if i > 0 {
			out += "; "
		}
		out += e
	}
	return out
}

// StopAndUninstallServices останавливает и снимает unit-файлы zator.
func (a *App) StopAndUninstallServices() error {
	if runtime.GOOS == "windows" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	mgr, err := service.New(service.Config{
		Paths:    a.Paths,
		Platform: a.Platform,
		ZatorBin: filepath.Join(a.Paths.BinDir, "zator"),
		ArgsFile: filepath.Join(a.Paths.ZapretRoot, "nfqws2.args"),
	})
	if err != nil {
		return err
	}
	a.StopNfqwsWatchdog()
	for _, u := range service.Units() {
		if err := mgr.Uninstall(ctx, u); err != nil {
			if u == service.UnitZapret2 {
				a.StartNfqwsWatchdog()
			}
			return fmt.Errorf("%s: %w", u, err)
		}
	}
	return nil
}

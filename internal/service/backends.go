package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator/internal/sysprofile"
)

// ---------------------------------------------------------------- вспомогательное

func (m *manager) writeFile(path, content string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	// Не переходим по старому симлинку при записи — иначе испортим целевой файл в ZapretRoot.
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("service: удалить старую ссылку %s: %w", path, err)
		}
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("service: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("service: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	cleanup = false
	return nil
}

// atomicSymlink атомарно создаёт символическую ссылку link → target
// (с откатом на копию, если ФС/ОС не разрешает символические ссылки).
func (m *manager) atomicSymlink(target, link string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	if info, err := os.Lstat(link); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if cur, rerr := os.Readlink(link); rerr == nil && cur == target {
			return nil
		}
	}
	tmp := link + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err == nil {
		if err := os.Rename(tmp, link); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("service: %w", err)
		}
		return nil
	}
	_ = os.Remove(tmp)
	data, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("service: %w", err)
	}
	return m.writeFile(link, string(data), 0o755)
}

func inspectInstalledFile(path string) (exists, brokenSymlink bool, err error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if _, serr := os.Stat(path); os.IsNotExist(serr) {
			return true, true, nil
		}
	}
	return true, false, nil
}

// queryResult разделяет «команда вернула ненулевой код» и «команду не удалось запустить».
func queryResult(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return false, nil
	}
	return false, err
}

func (m *manager) queryCmd(ctx context.Context, name string, args ...string) (bool, error) {
	_, err := m.cfg.Runner.Run(ctx, name, args...)
	return queryResult(err)
}

func (m *manager) uninstallProfileManaged(ctx context.Context, u Unit, scriptOnDisk string) error {
	exists, broken, err := inspectInstalledFile(scriptOnDisk)
	if err != nil {
		return fmt.Errorf("service: чтение %s: %w", u, err)
	}
	if !exists {
		return nil
	}
	if !broken {
		if err := m.runOps(ctx, m.profile.DisableOps(string(u), m.cfg.Paths)); err != nil {
			return err
		}
	}
	if err := os.Remove(scriptOnDisk); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("service: %w", err)
	}
	return m.runOps(ctx, m.profile.PostUninstallOps(string(u), m.cfg.Paths))
}

func regularFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// ---------------------------------------------------------------- systemd

type systemdBackend struct{ m *manager }

func (b systemdBackend) unitFile(u Unit) string {
	return b.m.sys(b.m.profile.ServiceScriptPath(string(u), b.m.cfg.Paths))
}

func (b systemdBackend) install(ctx context.Context, u Unit) error {
	if u == UnitZapret2 {
		return b.installNativeZapret(ctx)
	}
	content, err := RenderSystemd(b.m.templateData(u))
	if err != nil {
		return err
	}
	if err := b.m.writeFile(b.unitFile(u), content, 0o644); err != nil {
		return err
	}
	return b.m.runOps(ctx, b.m.profile.EnableOps(string(u), b.m.cfg.Paths))
}

func (b systemdBackend) installNativeZapret(ctx context.Context) error {
	nat := b.m.profile.NativeZapret(b.m.cfg.Paths)
	src := filepath.Join(b.m.cfg.Paths.ZapretRoot, filepath.FromSlash(nat.UpstreamRel))
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("service: upstream zapret2 systemd unit: %w", err)
	}
	if err := b.m.writeFile(b.unitFile(UnitZapret2), string(data), 0o644); err != nil {
		return err
	}
	return b.m.runOps(ctx, b.m.profile.EnableOps(string(UnitZapret2), b.m.cfg.Paths))
}

func (b systemdBackend) uninstall(ctx context.Context, u Unit) error {
	return b.m.uninstallProfileManaged(ctx, u, b.unitFile(u))
}

func (b systemdBackend) control(ctx context.Context, u Unit, action string) error {
	return b.m.runSpec(ctx, b.m.profile.ControlCmd(string(u), action, b.m.cfg.Paths))
}

func (b systemdBackend) query(ctx context.Context, u Unit) (bool, error) {
	spec := b.m.profile.QueryCmd(string(u), b.m.cfg.Paths)
	return b.m.queryCmd(ctx, spec.Name, spec.Args...)
}

// ---------------------------------------------------------------- OpenRC (Alpine / Gentoo)

type openrcBackend struct{ m *manager }

func (b openrcBackend) script(u Unit) string {
	return b.m.profile.ServiceScriptPath(string(u), b.m.cfg.Paths)
}

func (b openrcBackend) install(ctx context.Context, u Unit) error {
	if u == UnitZapret2 {
		return b.installNativeZapret(ctx)
	}
	dst := b.m.sys(b.script(u))
	var (
		content string
		err     error
	)
	if b.m.profile.OpenRCActive() {
		b.stopLegacySysVIfPresent(ctx, u, dst)
		content, err = RenderOpenRC(b.m.templateData(u))
	} else {
		content, err = RenderSysV(b.m.templateData(u))
	}
	if err != nil {
		return err
	}
	if err := b.m.writeFile(dst, content, 0o755); err != nil {
		return err
	}
	return b.m.runOps(ctx, b.m.profile.EnableOps(string(u), b.m.cfg.Paths))
}

func (b openrcBackend) stopLegacySysVIfPresent(ctx context.Context, u Unit, dst string) {
	info, err := os.Lstat(dst)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		return
	}
	text := string(data)
	if !strings.HasPrefix(text, "#!") || strings.HasPrefix(text, "#!/sbin/openrc-run") {
		return
	}
	_ = b.m.run(ctx, b.script(u), "stop")
}

func (b openrcBackend) installNativeZapret(ctx context.Context) error {
	dst := b.m.sys(b.script(UnitZapret2))
	if !b.m.profile.OpenRCActive() {
		src := filepath.Join(b.m.cfg.Paths.ZapretRoot, "init.d", "sysv", "zapret2")
		if !regularFile(src) {
			return fmt.Errorf("service: upstream zapret2 SysV init not found at %s", src)
		}
		if err := os.Chmod(src, 0o755); err != nil {
			return fmt.Errorf("service: %w", err)
		}
		if err := b.m.atomicSymlink(src, dst); err != nil {
			return err
		}
		return b.m.runOps(ctx, b.m.profile.EnableOps(string(UnitZapret2), b.m.cfg.Paths))
	}

	openrcSrc := filepath.Join(b.m.cfg.Paths.ZapretRoot, "init.d", "openrc", "zapret2")
	if sysprofile.ValidOpenRCZapretScript(b.m.cfg.Paths.ZapretRoot, openrcSrc) {
		if err := os.Chmod(openrcSrc, 0o755); err != nil {
			return fmt.Errorf("service: %w", err)
		}
		if err := b.m.atomicSymlink(openrcSrc, dst); err != nil {
			return err
		}
	} else {
		sysvSrc := filepath.Join(b.m.cfg.Paths.ZapretRoot, "init.d", "sysv", "zapret2")
		if !regularFile(sysvSrc) {
			return fmt.Errorf("service: upstream zapret2 OpenRC/SysV init not found in %s", b.m.cfg.Paths.ZapretRoot)
		}
		if err := os.Chmod(sysvSrc, 0o755); err != nil {
			return fmt.Errorf("service: %w", err)
		}
		wrapper, err := RenderOpenRCZapretWrapper(sysvSrc)
		if err != nil {
			return err
		}
		if err := b.m.writeFile(dst, wrapper, 0o755); err != nil {
			return err
		}
	}
	return b.m.runOps(ctx, b.m.profile.EnableOps(string(UnitZapret2), b.m.cfg.Paths))
}

func (b openrcBackend) uninstall(ctx context.Context, u Unit) error {
	return b.m.uninstallProfileManaged(ctx, u, b.m.sys(b.script(u)))
}

func (b openrcBackend) control(ctx context.Context, u Unit, action string) error {
	return b.m.runSpec(ctx, b.m.profile.ControlCmd(string(u), action, b.m.cfg.Paths))
}

func (b openrcBackend) query(ctx context.Context, u Unit) (bool, error) {
	spec := b.m.profile.QueryCmd(string(u), b.m.cfg.Paths)
	return b.m.queryCmd(ctx, spec.Name, spec.Args...)
}

// ---------------------------------------------------------------- procd (OpenWrt)

type procdBackend struct{ m *manager }

func (b procdBackend) script(u Unit) string {
	return b.m.profile.ServiceScriptPath(string(u), b.m.cfg.Paths)
}

func (b procdBackend) install(ctx context.Context, u Unit) error {
	if u == UnitZapret2 {
		return b.installNativeZapret(ctx)
	}
	content, err := RenderProcd(b.m.templateData(u))
	if err != nil {
		return err
	}
	if err := b.m.writeFile(b.m.sys(b.script(u)), content, 0o755); err != nil {
		return err
	}
	return b.m.runOps(ctx, b.m.profile.EnableOps(string(u), b.m.cfg.Paths))
}

func (b procdBackend) installNativeZapret(ctx context.Context) error {
	src := filepath.Join(b.m.cfg.Paths.ZapretRoot, "init.d", "openwrt", "zapret2")
	if !regularFile(src) {
		return fmt.Errorf("service: upstream OpenWrt zapret2 init not found at %s", src)
	}
	if err := os.Chmod(src, 0o755); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	dst := b.m.sys(b.script(UnitZapret2))
	if err := b.m.registerOpenWrtInit(src, dst); err != nil {
		return err
	}
	if err := cleanupOpenWrtInitLinks(dst, b.m.sys("/etc/rc.d")); err != nil {
		return err
	}
	return b.m.runOps(ctx, b.m.profile.EnableOps(string(UnitZapret2), b.m.cfg.Paths))
}

func (m *manager) registerOpenWrtInit(upstreamPath, systemPath string) error {
	info, err := os.Lstat(systemPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			link, readErr := os.Readlink(systemPath)
			if readErr == nil && link == upstreamPath {
				return nil
			}
			if regularFile(systemPath) {
				return nil // сохраняем локально настроенный симлинк
			}
			// Битый симлинк заменяем на актуальный upstream.
		} else if info.Mode().IsRegular() {
			content, readErr := os.ReadFile(systemPath)
			if readErr != nil {
				return readErr
			}
			if !strings.Contains(string(content), "(создано zator)") {
				return nil // сохраняем локально изменённый init-скрипт
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return m.atomicSymlink(upstreamPath, systemPath)
}

func cleanupOpenWrtInitLinks(systemPath, rcDir string) error {
	data, err := os.ReadFile(systemPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
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
		stop = "10" // значение по умолчанию в rc.common, если STOP= не задан
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
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cleanSystem := filepath.Clean(systemPath)
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
		resolved := filepath.Clean(filepath.Join(rcDir, link))
		if filepath.IsAbs(link) {
			resolved = filepath.Clean(link)
		}
		if resolved == cleanSystem {
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

func (b procdBackend) uninstall(ctx context.Context, u Unit) error {
	return b.m.uninstallProfileManaged(ctx, u, b.m.sys(b.script(u)))
}

func (b procdBackend) control(ctx context.Context, u Unit, action string) error {
	return b.m.runSpec(ctx, b.m.profile.ControlCmd(string(u), action, b.m.cfg.Paths))
}

func (b procdBackend) query(ctx context.Context, u Unit) (bool, error) {
	spec := b.m.profile.QueryCmd(string(u), b.m.cfg.Paths)
	return b.m.queryCmd(ctx, spec.Name, spec.Args...)
}

// ---------------------------------------------------------------- SysV

type sysvBackend struct{ m *manager }

func (b sysvBackend) script(u Unit) string {
	return b.m.profile.ServiceScriptPath(string(u), b.m.cfg.Paths)
}

func (b sysvBackend) install(ctx context.Context, u Unit) error {
	if u == UnitZapret2 {
		return b.installNativeZapret(ctx)
	}
	content, err := RenderSysV(b.m.templateData(u))
	if err != nil {
		return err
	}
	if err := b.m.writeFile(b.m.sys(b.script(u)), content, 0o755); err != nil {
		return err
	}
	return b.m.runOps(ctx, b.m.profile.EnableOps(string(u), b.m.cfg.Paths))
}

func (b sysvBackend) installNativeZapret(ctx context.Context) error {
	src := filepath.Join(b.m.cfg.Paths.ZapretRoot, "init.d", "sysv", "zapret2")
	if !regularFile(src) {
		return fmt.Errorf("service: upstream zapret2 SysV init not found at %s", src)
	}
	if err := os.Chmod(src, 0o755); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	if err := b.m.atomicSymlink(src, b.m.sys(b.script(UnitZapret2))); err != nil {
		return err
	}
	return b.m.runOps(ctx, b.m.profile.EnableOps(string(UnitZapret2), b.m.cfg.Paths))
}

func (b sysvBackend) uninstall(ctx context.Context, u Unit) error {
	return b.m.uninstallProfileManaged(ctx, u, b.m.sys(b.script(u)))
}

func (b sysvBackend) control(ctx context.Context, u Unit, action string) error {
	return b.m.runSpec(ctx, b.m.profile.ControlCmd(string(u), action, b.m.cfg.Paths))
}

func (b sysvBackend) query(ctx context.Context, u Unit) (bool, error) {
	spec := b.m.profile.QueryCmd(string(u), b.m.cfg.Paths)
	return b.m.queryCmd(ctx, spec.Name, spec.Args...)
}

// ---------------------------------------------------------------- Entware

type entwareBackend struct{ sysvBackend }

// initScriptFor возвращает путь init-скрипта, который вызывается для службы
// (для zapret2 — скрипт в каталоге zapret2, на который указывает S90-симлинк).
func (m *manager) initScriptFor(u Unit) string {
	return m.profile.ServiceScriptPath(string(u), m.cfg.Paths)
}

func (b entwareBackend) s90Link() string {
	return b.m.sys(entwareInitDir + "/S90-" + string(UnitZapret2))
}

func (b entwareBackend) merlinFile() string { return b.m.sys(merlinHookFile) }
func (b entwareBackend) keeneticHook() string {
	return b.m.sys(keeneticHookDir + "/" + KeeneticHookName)
}

func (b entwareBackend) install(ctx context.Context, u Unit) error {
	d := b.m.templateData(u)
	script := b.m.initScriptFor(u)
	d.InitScript = filepath.ToSlash(script)
	scriptOnDisk := script
	if u != UnitZapret2 {
		scriptOnDisk = b.m.sys(script)
	}
	// zapret2 всегда использует штатный init-скрипт из архива релиза.
	if u == UnitZapret2 {
		if _, err := os.Stat(scriptOnDisk); err != nil {
			return fmt.Errorf("service: upstream zapret2 init is required: %w", err)
		} else if err := os.Chmod(scriptOnDisk, 0o755); err != nil {
			return fmt.Errorf("service: %w", err)
		}
		if err := b.linkS90(scriptOnDisk); err != nil {
			return err
		}
		return b.installHook(d)
	}
	content, err := RenderSysV(d)
	if err != nil {
		return err
	}
	return b.m.writeFile(scriptOnDisk, content, 0o755)
}

// linkS90 создаёт /opt/etc/init.d/S90-zapret2 → init-скрипт (копия, если
// символические ссылки недоступны).
func (b entwareBackend) linkS90(target string) error {
	return b.m.atomicSymlink(target, b.s90Link())
}

func (b entwareBackend) installHook(d TemplateData) error {
	switch b.m.cfg.Hook {
	case HookKeenetic:
		content, err := RenderKeeneticHook(d)
		if err != nil {
			return err
		}
		return b.m.writeFile(b.keeneticHook(), content, 0o755)
	case HookMerlin:
		path := b.merlinFile()
		old, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: %w", err)
		}
		updated := AddHookLine(string(old), MerlinHookLine(d.InitScript))
		return b.m.writeFile(path, updated, 0o755)
	}
	return nil
}

func (b entwareBackend) removeHook() error {
	switch b.m.cfg.Hook {
	case HookKeenetic:
		if err := os.Remove(b.keeneticHook()); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: %w", err)
		}
	case HookMerlin:
		path := b.merlinFile()
		old, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("service: %w", err)
		}
		line := MerlinHookLine(filepath.ToSlash(b.m.initScriptFor(UnitZapret2)))
		return b.m.writeFile(path, RemoveHookLine(string(old), line), 0o755)
	}
	return nil
}

func (b entwareBackend) uninstall(ctx context.Context, u Unit) error {
	script := b.m.initScriptFor(u)
	if err := b.m.run(ctx, script, "stop"); err != nil {
		return fmt.Errorf("service: stop %s: %w", u, err)
	}
	if u == UnitZapret2 {
		// Не удаляем upstream init в /opt/zapret2 — только S90/хуки zator.
		if err := os.Remove(b.s90Link()); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: %w", err)
		}
		if err := b.removeHook(); err != nil {
			return err
		}
		return nil
	}
	if err := os.Remove(b.m.sys(script)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("service: %w", err)
	}
	return nil
}

func (b entwareBackend) control(ctx context.Context, u Unit, action string) error {
	return b.m.runSpec(ctx, b.m.profile.ControlCmd(string(u), action, b.m.cfg.Paths))
}

func (b entwareBackend) query(ctx context.Context, u Unit) (bool, error) {
	spec := b.m.profile.QueryCmd(string(u), b.m.cfg.Paths)
	return b.m.queryCmd(ctx, spec.Name, spec.Args...)
}

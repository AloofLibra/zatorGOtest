// Package deps проверяет и при возможности устанавливает зависимости ZatorGO
// под целевую платформу (Entware/Keenetic, OpenWrt, VPS).
package deps

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/platform"
	"github.com/AloofLibra/zator/internal/sysprofile"
)

// Status — состояние проверки.
type Status string

const (
	StatusOK      Status = "ok"
	StatusMissing Status = "missing"
	StatusFixed   Status = "fixed"
	StatusFailed  Status = "failed"
	StatusSkip    Status = "skip"
)

// Item — одна проверка (бинарник, модуль или пакет).
type Item struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"` // binary|module|package
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Report — результат Ensure/Check.
type Report struct {
	Platform        platform.Platform `json:"platform"`
	Items           []Item            `json:"items"`
	CriticalMissing []string          `json:"critical_missing,omitempty"`
}

// OK — нет критичных отсутствующих зависимостей.
func (r Report) OK() bool { return len(r.CriticalMissing) == 0 }

// Summary — краткий текст для CLI.
func (r Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "платформа %s", r.Platform.String())
	for _, it := range r.Items {
		fmt.Fprintf(&b, "\n  [%s] %s %s", it.Status, it.Kind, it.Name)
		if it.Detail != "" {
			fmt.Fprintf(&b, " (%s)", it.Detail)
		}
	}
	if len(r.CriticalMissing) > 0 {
		fmt.Fprintf(&b, "\nкритично отсутствует: %s", strings.Join(r.CriticalMissing, ", "))
	}
	return b.String()
}

// Options — поведение Ensure.
type Options struct {
	// InstallPackages — пытаться opkg/apk/apt установить недостающее.
	InstallPackages bool
	// LoadModules — insmod недостающих xt_*/ip_set*.
	LoadModules bool
	// Root — корень FS (для тестов); пусто = "/".
	Root string
	// PathDirs — доп. каталоги поиска бинарников.
	PathDirs []string
}

func (o Options) root() string {
	if o.Root == "" {
		return "/"
	}
	return o.Root
}

func (o Options) pathDirs() []string {
	if len(o.PathDirs) > 0 {
		return o.PathDirs
	}
	return []string{
		"/opt/sbin", "/opt/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin",
	}
}

// Check только диагностирует, ничего не ставит.
func Check(plat platform.Platform, opts Options) Report {
	opts.InstallPackages = false
	opts.LoadModules = false
	return Ensure(context.Background(), plat, opts)
}

// Ensure проверяет зависимости и по возможности чинит (пакеты/модули).
// На Windows-хосте без opts.Root — no-op (сборка/разработка).
// С opts.Root (юнит-тесты) проверки выполняются относительно fake FS.
func Ensure(ctx context.Context, plat platform.Platform, opts Options) Report {
	rep := Report{Platform: plat}
	if runtime.GOOS == "windows" && opts.Root == "" {
		rep.Items = append(rep.Items, Item{Name: "host", Kind: "platform", Status: StatusSkip, Detail: "windows host"})
		return rep
	}

	prof := sysprofile.Resolve(plat, sysprofile.Options{Root: opts.root()})
	ensureWithProfile(ctx, &rep, prof, opts)
	return rep
}

func ensureWithProfile(ctx context.Context, rep *Report, prof sysprofile.Profile, opts Options) {
	spec := prof.RequiredDeps(rep.Platform.FWType)
	if spec.RequiredPkgManager != "" {
		if p := findBinary(spec.RequiredPkgManager, opts); p != "" {
			rep.Items = append(rep.Items, Item{Name: spec.RequiredPkgManager, Kind: "binary", Status: StatusOK, Detail: p})
		} else {
			detail := ""
			if spec.RequiredPkgManager == "opkg" {
				detail = "пакетный менеджер Entware"
			}
			rep.Items = append(rep.Items, Item{Name: spec.RequiredPkgManager, Kind: "binary", Status: StatusMissing, Detail: detail})
			rep.CriticalMissing = appendUnique(rep.CriticalMissing, spec.RequiredPkgManager)
		}
	}

	needPkg := false
	for _, b := range spec.Binaries {
		if p := findBinary(b.Name, opts); p != "" {
			rep.Items = append(rep.Items, Item{Name: b.Name, Kind: "binary", Status: StatusOK, Detail: p})
			continue
		}
		needPkg = true
		rep.Items = append(rep.Items, Item{Name: b.Name, Kind: "binary", Status: StatusMissing})
		if b.Critical {
			rep.CriticalMissing = appendUnique(rep.CriticalMissing, b.Name)
		}
	}

	if needPkg && opts.InstallPackages && len(spec.Packages) > 0 {
		pkgs := uniqueStrings(spec.Packages)
		mgr, err := installPackages(ctx, prof, opts, pkgs...)
		opName := "pkg install"
		if mgr == "opkg" {
			opName = "opkg install"
		}
		if err != nil {
			rep.Items = append(rep.Items, Item{Name: opName, Kind: "package", Status: StatusFailed, Detail: err.Error()})
		} else {
			rep.Items = append(rep.Items, Item{Name: opName, Kind: "package", Status: StatusFixed, Detail: strings.Join(pkgs, " ")})
			recheckBinaries(rep, spec, opts)
		}
	}

	for _, m := range spec.Modules {
		ensureModule(rep, prof, m.Name, m.Critical, opts)
	}
}

func recheckBinaries(rep *Report, spec sysprofile.DepsSpec, opts Options) {
	kept := make([]string, 0, len(rep.CriticalMissing))
	for _, name := range rep.CriticalMissing {
		if spec.RequiredPkgManager != "" && name == spec.RequiredPkgManager && findBinary(name, opts) == "" {
			kept = append(kept, name)
		}
	}
	rep.CriticalMissing = kept
	for i := range rep.Items {
		it := &rep.Items[i]
		if it.Kind != "binary" || (it.Status != StatusMissing && it.Status != StatusFailed) {
			continue
		}
		if p := findBinary(it.Name, opts); p != "" {
			it.Status = StatusFixed
			it.Detail = p
			continue
		}
		if isCriticalInSpec(spec, it.Name) {
			rep.CriticalMissing = appendUnique(rep.CriticalMissing, it.Name)
		}
	}
}

func isCriticalInSpec(spec sysprofile.DepsSpec, name string) bool {
	if spec.RequiredPkgManager == name {
		return true
	}
	for _, b := range spec.Binaries {
		if b.Name == name && b.Critical {
			return true
		}
	}
	return false
}

func ensureModule(rep *Report, prof sysprofile.Profile, name string, critical bool, opts Options) {
	if moduleLoaded(opts, name) {
		rep.Items = append(rep.Items, Item{Name: name, Kind: "module", Status: StatusOK})
		return
	}
	if opts.LoadModules {
		if err := loadModule(prof, opts, name); err != nil {
			// повторная проверка: builtin / уже подтянут зависимостью
			if moduleLoaded(opts, name) {
				rep.Items = append(rep.Items, Item{Name: name, Kind: "module", Status: StatusFixed, Detail: "loaded"})
				return
			}
			st := StatusMissing
			if critical {
				st = StatusFailed
				rep.CriticalMissing = appendUnique(rep.CriticalMissing, name)
			}
			rep.Items = append(rep.Items, Item{Name: name, Kind: "module", Status: st, Detail: err.Error()})
			return
		}
		rep.Items = append(rep.Items, Item{Name: name, Kind: "module", Status: StatusFixed})
		return
	}
	st := StatusMissing
	if critical {
		rep.CriticalMissing = appendUnique(rep.CriticalMissing, name)
	}
	rep.Items = append(rep.Items, Item{Name: name, Kind: "module", Status: st})
}

func installPackages(ctx context.Context, prof sysprofile.Profile, opts Options, pkgs ...string) (string, error) {
	order := prof.PkgManagerOrder()
	for _, mgr := range order {
		bin := findBinary(mgr, opts)
		if bin == "" {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		if upd, ok := prof.PkgUpdateCmd(mgr, bin); ok {
			u := exec.CommandContext(cctx, upd.Name, upd.Args...)
			u.Env = enrichedEnv()
			_ = u.Run()
		}
		inst := prof.PkgInstallCmd(mgr, bin, pkgs)
		cmd := exec.CommandContext(cctx, inst.Name, inst.Args...)
		cmd.Env = enrichedEnv()
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			return mgr, fmt.Errorf("%v: %s", err, trimOut(string(out)))
		}
		return mgr, nil
	}
	return "", fmt.Errorf("нет пакетного менеджера (%s) для установки пакетов", strings.Join(order, "/"))
}

func findBinary(name string, opts Options) string {
	// Сначала фиксированные пути Entware/OpenWrt: LookPath на Keenetic часто
	// без /opt/sbin в PATH (служба/launcher).
	for _, dir := range opts.pathDirs() {
		var candidate string
		if opts.root() == "/" {
			candidate = filepath.Join(dir, name)
		} else {
			candidate = filepath.Join(opts.root(), filepath.FromSlash(strings.TrimPrefix(dir, "/")), name)
		}
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	if opts.root() == "/" {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

func moduleLoaded(opts Options, name string) bool {
	norm := normalizeModName(name)
	if _, err := os.Stat(filepath.Join(opts.root(), "sys", "module", name)); err == nil {
		return true
	}
	if norm != name {
		if _, err := os.Stat(filepath.Join(opts.root(), "sys", "module", norm)); err == nil {
			return true
		}
	}
	if f, err := os.Open(filepath.Join(opts.root(), "proc", "modules")); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if field, _, ok := strings.Cut(line, " "); ok {
				if normalizeModName(field) == norm {
					return true
				}
			} else if normalizeModName(strings.TrimSpace(line)) == norm {
				return true
			}
		}
	}
	return moduleBuiltin(opts, name)
}

func moduleBuiltin(opts Options, name string) bool {
	rel := kernelRelease(opts)
	if rel == "" {
		return false
	}
	f, err := os.Open(filepath.Join(opts.root(), "lib", "modules", rel, "modules.builtin"))
	if err != nil {
		return false
	}
	defer f.Close()
	want := normalizeModName(name)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.LastIndexByte(line, '/'); idx >= 0 {
			line = line[idx+1:]
		}
		line = strings.TrimSuffix(line, ".xz")
		line = strings.TrimSuffix(line, ".gz")
		line = strings.TrimSuffix(line, ".zst")
		line = strings.TrimSuffix(line, ".ko")
		if normalizeModName(line) == want {
			return true
		}
	}
	return false
}

func normalizeModName(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

func kernelRelease(opts Options) string {
	b, err := os.ReadFile(filepath.Join(opts.root(), "proc", "sys", "kernel", "osrelease"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func loadModule(prof sysprofile.Profile, opts Options, name string) error {
	rel := kernelRelease(opts)
	cmds := prof.ModuleLoadCmds(name, rel, opts.root(), func(bin string) string {
		return findBinary(bin, opts)
	})
	if len(cmds) == 0 {
		if rel == "" {
			return fmt.Errorf("не удалось прочитать версию ядра")
		}
		return fmt.Errorf("нет %s", filepath.Join(opts.root(), "lib", "modules", rel, name+".ko"))
	}
	var lastErr error
	for _, spec := range cmds {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
		cmd.Env = enrichedEnv()
		out, err := cmd.CombinedOutput()
		cancel()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return lastErr
}

// opkgInstallArgs — аргументы для Entware/OpenWrt opkg (без -y: его нет в opkg).
func opkgInstallArgs(pkgs ...string) []string {
	return sysprofile.Resolve(platform.Platform{Kind: platform.KindEntware}, sysprofile.Options{}).
		PkgInstallCmd("opkg", "opkg", pkgs).Args
}

func enrichedEnv() []string {
	path := "/opt/sbin:/opt/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	if cur := os.Getenv("PATH"); cur != "" {
		path = path + string(os.PathListSeparator) + cur
	}
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			out = append(out, "PATH="+path)
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		out = append(out, "PATH="+path)
	}
	return out
}

func trimOut(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func appendUnique(in []string, v string) []string {
	for _, x := range in {
		if x == v {
			return in
		}
	}
	return append(in, v)
}

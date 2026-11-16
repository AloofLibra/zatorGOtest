// Package deps проверяет и при возможности устанавливает зависимости ZatorGO
// под целевую платформу (Entware/Keenetic, OpenWrt, VPS).
package deps

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/platform"
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

	switch plat.Kind {
	case platform.KindEntware:
		ensureEntware(ctx, &rep, opts)
	case platform.KindOpenWRT:
		ensureOpenWRT(ctx, &rep, opts)
	default:
		ensureVPS(ctx, &rep, opts)
	}
	return rep
}

func ensureEntware(ctx context.Context, rep *Report, opts Options) {
	if p := findBinary("opkg", opts); p != "" {
		rep.Items = append(rep.Items, Item{Name: "opkg", Kind: "binary", Status: StatusOK, Detail: p})
	} else {
		rep.Items = append(rep.Items, Item{Name: "opkg", Kind: "binary", Status: StatusMissing, Detail: "пакетный менеджер Entware"})
		rep.CriticalMissing = append(rep.CriticalMissing, "opkg")
	}

	bins := []struct {
		name     string
		critical bool
	}{
		{"iptables", true},
		{"ipset", true},
		{"ip6tables", false},
		{"insmod", false},
	}
	needPkg := false
	for _, b := range bins {
		path := findBinary(b.name, opts)
		if path != "" {
			rep.Items = append(rep.Items, Item{Name: b.name, Kind: "binary", Status: StatusOK, Detail: path})
			continue
		}
		needPkg = true
		rep.Items = append(rep.Items, Item{Name: b.name, Kind: "binary", Status: StatusMissing})
		if b.critical {
			rep.CriticalMissing = append(rep.CriticalMissing, b.name)
		}
	}
	// Entware opkg неинтерактивен и НЕ поддерживает apt-стиль -y.
	if needPkg && opts.InstallPackages {
		pkgs := uniqueStrings([]string{"iptables", "ipset"})
		if err := opkgInstall(ctx, opts, pkgs...); err != nil {
			rep.Items = append(rep.Items, Item{Name: "opkg install", Kind: "package", Status: StatusFailed, Detail: err.Error()})
		} else {
			rep.Items = append(rep.Items, Item{Name: "opkg install", Kind: "package", Status: StatusFixed, Detail: strings.Join(pkgs, " ")})
			recheckBinaries(rep, opts)
		}
	}

	mods := []string{
		"xt_NFQUEUE", "xt_connbytes", "xt_multiport", "xt_set",
		"ip_set", "ip_set_bitmap_port", "ip_set_hash_ip", "ip_set_hash_net", "ip_set_hash_ipport",
	}
	criticalMods := map[string]bool{"xt_NFQUEUE": true, "xt_connbytes": true, "ip_set_bitmap_port": true}
	for _, m := range mods {
		ensureModule(rep, m, criticalMods[m], opts)
	}
}

func ensureOpenWRT(ctx context.Context, rep *Report, opts Options) {
	fw := rep.Platform.FWType
	if fw == platform.FWNftables {
		checkBinary(rep, "nft", true, opts)
	}
	for _, b := range []string{"iptables", "ipset"} {
		critical := b == "iptables" && fw == platform.FWIptables
		if b == "ipset" {
			critical = fw == platform.FWIptables
		}
		checkBinary(rep, b, critical, opts)
	}
	if opts.InstallPackages && hasMissingBinaries(rep) {
		pkgs := []string{"ipset", "iptables-mod-conntrack-extra", "kmod-ipt-conntrack-extra", "kmod-ipt-ipset"}
		if fw == platform.FWNftables {
			pkgs = append(pkgs, "nftables", "kmod-nfnetlink-queue", "kmod-nft-queue")
		} else {
			pkgs = append(pkgs, "iptables-mod-nfqueue", "kmod-ipt-nfqueue", "iptables-zz-legacy")
		}
		if err := opkgInstall(ctx, opts, pkgs...); err != nil {
			rep.Items = append(rep.Items, Item{Name: "opkg install", Kind: "package", Status: StatusFailed, Detail: err.Error()})
		} else {
			rep.Items = append(rep.Items, Item{Name: "opkg install", Kind: "package", Status: StatusFixed, Detail: strings.Join(pkgs, " ")})
			recheckBinaries(rep, opts)
		}
	}
	queueModules := []string{"xt_NFQUEUE"}
	if fw == platform.FWNftables {
		// OpenWrt's iptables-nft translates NFQUEUE into nft queue expressions;
		// it uses nfnetlink_queue + nft_queue, not the legacy xt_NFQUEUE module.
		queueModules = []string{"nfnetlink_queue", "nft_queue"}
	}
	criticalMods := map[string]bool{"xt_connbytes": true}
	mods := append(queueModules, "xt_connbytes", "ip_set_bitmap_port")
	for _, m := range mods {
		critical := criticalMods[m]
		if m == "xt_NFQUEUE" || m == "nfnetlink_queue" || m == "nft_queue" {
			critical = true
		}
		ensureModule(rep, m, critical, opts)
	}
}

func ensureVPS(ctx context.Context, rep *Report, opts Options) {
	for _, b := range []string{"iptables", "ipset"} {
		checkBinary(rep, b, true, opts)
	}
	if opts.InstallPackages && hasMissingBinaries(rep) {
		if err := hostPkgInstall(ctx, opts, "iptables", "ipset"); err != nil {
			rep.Items = append(rep.Items, Item{Name: "pkg install", Kind: "package", Status: StatusFailed, Detail: err.Error()})
		} else {
			rep.Items = append(rep.Items, Item{Name: "pkg install", Kind: "package", Status: StatusFixed, Detail: "iptables ipset"})
			recheckBinaries(rep, opts)
		}
	}
	for _, m := range []string{"xt_NFQUEUE", "xt_connbytes", "ip_set_bitmap_port"} {
		ensureModule(rep, m, m == "xt_NFQUEUE", opts)
	}
}

func hasMissingBinaries(rep *Report) bool {
	for _, it := range rep.Items {
		if it.Kind == "binary" && (it.Status == StatusMissing || it.Status == StatusFailed) {
			return true
		}
	}
	return false
}

func checkBinary(rep *Report, name string, critical bool, opts Options) {
	if p := findBinary(name, opts); p != "" {
		rep.Items = append(rep.Items, Item{Name: name, Kind: "binary", Status: StatusOK, Detail: p})
		return
	}
	rep.Items = append(rep.Items, Item{Name: name, Kind: "binary", Status: StatusMissing})
	if critical {
		rep.CriticalMissing = appendUnique(rep.CriticalMissing, name)
	}
}

func recheckBinaries(rep *Report, opts Options) {
	kept := make([]string, 0, len(rep.CriticalMissing))
	for _, name := range rep.CriticalMissing {
		if name == "opkg" && findBinary("opkg", opts) == "" {
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
		if isCriticalBinary(it.Name) || it.Name == "nft" || it.Name == "opkg" {
			rep.CriticalMissing = appendUnique(rep.CriticalMissing, it.Name)
		}
	}
}

func ensureModule(rep *Report, name string, critical bool, opts Options) {
	if moduleLoaded(opts, name) {
		rep.Items = append(rep.Items, Item{Name: name, Kind: "module", Status: StatusOK})
		return
	}
	if opts.LoadModules {
		if err := loadModule(opts, name); err != nil {
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

func hostPkgInstall(ctx context.Context, opts Options, pkgs ...string) error {
	if apt := findBinary("apt-get", opts); apt != "" {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		args := append([]string{"install", "-y"}, pkgs...)
		cmd := exec.CommandContext(cctx, apt, args...)
		cmd.Env = enrichedEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, trimOut(string(out)))
		}
		return nil
	}
	if apk := findBinary("apk", opts); apk != "" {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		args := append([]string{"add"}, pkgs...)
		cmd := exec.CommandContext(cctx, apk, args...)
		cmd.Env = enrichedEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, trimOut(string(out)))
		}
		return nil
	}
	return fmt.Errorf("нет apt-get/apk для установки пакетов")
}

func isCriticalBinary(name string) bool {
	return name == "iptables" || name == "ipset"
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
	if _, err := os.Stat(filepath.Join(opts.root(), "sys", "module", name)); err == nil {
		return true
	}
	data, err := os.ReadFile(filepath.Join(opts.root(), "proc", "modules"))
	if err != nil {
		return false
	}
	prefix := name + " "
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) || line == name {
			return true
		}
	}
	return false
}

func kernelRelease(opts Options) string {
	b, err := os.ReadFile(filepath.Join(opts.root(), "proc", "sys", "kernel", "osrelease"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func loadModule(opts Options, name string) error {
	rel := kernelRelease(opts)
	if rel == "" {
		return fmt.Errorf("не удалось прочитать версию ядра")
	}
	ko := filepath.Join(opts.root(), "lib", "modules", rel, name+".ko")
	if _, err := os.Stat(ko); err != nil {
		return fmt.Errorf("нет %s", ko)
	}
	insmod := findBinary("insmod", opts)
	if insmod == "" {
		insmod = "insmod"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, insmod, ko)
	cmd.Env = enrichedEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// opkgInstallArgs — аргументы для Entware/OpenWrt opkg (без -y: его нет в opkg).
func opkgInstallArgs(pkgs ...string) []string {
	return append([]string{"install"}, pkgs...)
}

func opkgInstall(ctx context.Context, opts Options, pkgs ...string) error {
	opkg := findBinary("opkg", opts)
	if opkg == "" {
		return fmt.Errorf("opkg не найден")
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	upd := exec.CommandContext(cctx, opkg, "update")
	upd.Env = enrichedEnv()
	_ = upd.Run()
	cmd := exec.CommandContext(cctx, opkg, opkgInstallArgs(pkgs...)...)
	cmd.Env = enrichedEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, trimOut(string(out)))
	}
	return nil
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

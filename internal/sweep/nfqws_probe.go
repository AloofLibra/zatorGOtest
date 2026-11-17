package sweep

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/firewall"
	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/zapret"
)

// Жёстко прошитый в Lua путь к orchestra (см. assets/lua/locked.lua).
const productionOrchestraLuaPath = "/opt/zator/state/orchestra"

var probeStrategyArg = regexp.MustCompile(`:strategy=[0-9]+(?:$|:)`)

// NfqwsProbeConfig — параметры изолированного пробника nfqws2.
type NfqwsProbeConfig struct {
	Paths paths.Paths

	// WorkDir — каталог pid/args/lua (по умолчанию /tmp/zator/supersweep-probe).
	WorkDir string
	// OrchestraDir — приватное хранилище локов ProbeStore
	// (по умолчанию WorkDir/orchestra).
	OrchestraDir string

	// Binary — путь к nfqws2 (по умолчанию ZapretRoot/nfq2/nfqws2).
	Binary string
	// Config — живой config zapret2; из него берутся NFQWS2_OPT и FWTYPE.
	Config *config.Config
	// Runner — iptables/nft (по умолчанию firewall.ExecRunner).
	Runner firewall.Runner

	// UID/GID процесса nfqws2 (по умолчанию ResolveProbeUser / SuperSweepProbeUID).
	UID, GID uint32
	// User — имя для nfqws2 --user= (обязано существовать в passwd).
	User string

	// StartDelay — пауза после старта nfqws2 до установки firewall
	// (по умолчанию 300 мс).
	StartDelay time.Duration
	// PortRangeFile подменяет /proc/.../ip_local_port_range (тесты).
	PortRangeFile string

	// NewCmd подменяет создание процесса (тесты).
	NewCmd func(ctx context.Context, name string, args []string) *exec.Cmd

	// resolveUser подменяет ResolveProbeUser в тестах.
	resolveUser func() (name string, uid, gid uint32)
}

// NfqwsProbe — изолированный nfqws2 + транспорт SuperSweep.
type NfqwsProbe struct {
	cfg NfqwsProbeConfig

	mu      sync.Mutex
	ss      *firewall.SuperSweep
	cmd     *exec.Cmd
	pid     int
	profile int
	started bool
}

// NewNfqwsProbe создаёт пробник. Config обязателен.
func NewNfqwsProbe(cfg NfqwsProbeConfig) (*NfqwsProbe, error) {
	if cfg.Config == nil {
		return nil, fmt.Errorf("%w: не задан Config", ErrConfig)
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = filepath.Join(paths.RuntimeTempDir(cfg.Paths.ZatorRoot), "supersweep-probe")
	}
	if cfg.OrchestraDir == "" {
		cfg.OrchestraDir = filepath.Join(cfg.WorkDir, "orchestra")
	}
	if cfg.Binary == "" {
		cfg.Binary = zapret.Nfqws2Path(cfg.Paths.ZapretRoot)
	}
	if cfg.Runner == nil {
		cfg.Runner = firewall.ExecRunner{}
	}
	resolve := cfg.resolveUser
	if resolve == nil {
		resolve = firewall.ResolveProbeUser
	}
	if cfg.User == "" || cfg.UID == 0 {
		name, uid, gid := resolve()
		if name == "" || uid == 0 {
			return nil, fmt.Errorf("%w: нет пользователя для probe (нужны nobody или zator-ss в passwd)", ErrProbe)
		}
		if cfg.User == "" {
			cfg.User = name
		}
		if cfg.UID == 0 {
			cfg.UID = uid
		}
		if cfg.GID == 0 {
			cfg.GID = gid
		}
	}
	if cfg.GID == 0 {
		cfg.GID = cfg.UID
	}
	if cfg.User == "" {
		return nil, fmt.Errorf("%w: пустое --user для nfqws2", ErrProbe)
	}
	if cfg.StartDelay <= 0 {
		cfg.StartDelay = 300 * time.Millisecond
	}
	return &NfqwsProbe{cfg: cfg}, nil
}

// OrchestraDir возвращает каталог приватных локов.
func (p *NfqwsProbe) OrchestraDir() string { return p.cfg.OrchestraDir }

// WorkDir возвращает рабочий каталог пробника.
func (p *NfqwsProbe) WorkDir() string { return p.cfg.WorkDir }

// Start реализует Probe: поднимает nfqws2 (отдельная очередь) и правила изоляции.
func (p *NfqwsProbe) Start(ctx context.Context, profile int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Cleanup никогда не через sweep-ctx: cancel иначе оставляет Z2R_SS_*.
	if err := p.stopLocked(context.Background()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.profile = profile

	if err := os.MkdirAll(p.cfg.OrchestraDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(p.cfg.OrchestraDir, "scopes"), 0o755); err != nil {
		return err
	}
	for _, name := range []string{"locked.tsv", "locked.manual.tsv", "auto_locked.tsv"} {
		path := filepath.Join(p.cfg.OrchestraDir, name)
		if _, err := os.Stat(path); err != nil {
			if err := fsutil.AtomicWriteFile(path, nil, 0o644); err != nil {
				return err
			}
		}
	}

	luaDir := filepath.Join(p.cfg.WorkDir, "lua")
	if err := os.MkdirAll(luaDir, 0o755); err != nil {
		return err
	}
	if err := p.writeProbeLua(luaDir); err != nil {
		return err
	}

	argsPath := filepath.Join(p.cfg.WorkDir, "nfqws2.args")
	args, err := p.buildArgs(luaDir)
	if err != nil {
		return err
	}
	if err := writeArgsFile(argsPath, args); err != nil {
		return err
	}

	bin := p.cfg.Binary
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("%w: nfqws2: %v", ErrProbe, err)
	}
	// nfqws2 понимает @path только как содержимое для --lua-init=@file / --blob=,
	// а не как список argv. Аргументы передаём напрямую (файл — для отладки).
	// Не привязываем к sweep-ctx: иначе cancel убивает nfqws до Stop/firewall cleanup.
	cmdArgs := args
	var cmd *exec.Cmd
	if p.cfg.NewCmd != nil {
		cmd = p.cfg.NewCmd(ctx, bin, cmdArgs)
	} else {
		cmd = exec.Command(bin, cmdArgs...)
	}
	_ = argsPath
	if cmd == nil {
		return fmt.Errorf("%w: пустая команда nfqws2", ErrProbe)
	}
	applyProbeCredential(cmd, p.cfg.UID, p.cfg.GID)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: запуск nfqws2: %v", ErrProbe, err)
	}
	p.cmd = cmd
	p.pid = cmd.Process.Pid
	_ = fsutil.AtomicWriteFile(filepath.Join(p.cfg.WorkDir, "nfqws2.pid"),
		[]byte(fmt.Sprintf("%d\n", p.pid)), 0o644)

	select {
	case <-ctx.Done():
		_ = p.stopLocked(context.Background())
		return ctx.Err()
	case <-time.After(p.cfg.StartDelay):
	}
	if !probeProcessRunning(cmd.Process) {
		_ = p.stopLocked(context.Background())
		return fmt.Errorf("%w: nfqws2 завершился сразу после старта (проверьте --user=%s и NFQUEUE)", ErrProbe, p.cfg.User)
	}

	ssCfg, err := firewall.SuperSweepConfigFromGetter(p.cfg.Config.Get)
	if err != nil {
		_ = p.stopLocked(context.Background())
		return fmt.Errorf("%w: %v", ErrProbe, err)
	}
	if ssCfg.Backend == "" {
		_ = p.stopLocked(context.Background())
		return fmt.Errorf("%w: не задан FWTYPE", ErrProbe)
	}
	ssCfg.ProbeUID = p.cfg.UID
	ssCfg.TrafficUID = firewall.ResolveTrafficUID()
	wsCfg, err := firewall.WssizeConfigFromGetter(p.cfg.Config.Get, p.cfg.Paths.ZatorRoot)
	if err != nil {
		_ = p.stopLocked(context.Background())
		return fmt.Errorf("%w: WSSIZE config: %v", ErrProbe, err)
	}
	if wsCfg.Active() {
		ssCfg.Wssize = &wsCfg
	}
	if p.cfg.PortRangeFile != "" {
		ssCfg.PortRangeFile = p.cfg.PortRangeFile
	}
	ss, err := firewall.NewSuperSweep(ssCfg, p.cfg.Runner)
	if err != nil {
		_ = p.stopLocked(context.Background())
		return fmt.Errorf("%w: %v", ErrProbe, err)
	}
	if err := ss.Start(ctx); err != nil {
		_ = p.stopLocked(context.Background())
		return fmt.Errorf("%w: firewall: %v", ErrProbe, err)
	}
	p.ss = ss
	p.started = true
	return nil
}

// Healthy реализует Probe.
func (p *NfqwsProbe) Healthy(ctx context.Context) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started || p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		return false
	}
	// Signal(0) на unix считает zombie «живым» — проверяем состояние явно.
	if !probeProcessRunning(p.cmd.Process) {
		return false
	}
	if p.ss == nil {
		return false
	}
	return p.ss.Check(ctx) == nil
}

// Reinstall восстанавливает только iptables-транспорт пробника. На Keenetic
// ndm может удалить его правила, пока процесс nfqws2 продолжает работать.
func (p *NfqwsProbe) Reinstall(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started || p.cmd == nil || p.cmd.Process == nil || p.ss == nil {
		return firewall.ErrTransportInactive
	}
	if !probeProcessRunning(p.cmd.Process) {
		return fmt.Errorf("%w: процесс nfqws2 завершился", ErrProbe)
	}
	if err := p.ss.Reinstall(ctx); err != nil {
		return fmt.Errorf("%w: восстановление firewall: %v", ErrProbe, err)
	}
	return nil
}

// Stop реализует Probe.
func (p *NfqwsProbe) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopLocked(context.Background())
}

func (p *NfqwsProbe) stopLocked(_ context.Context) error {
	// Всегда Background: canceled sweep-ctx обрывает iptables mid-cleanup.
	cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var errs []string
	if p.ss != nil {
		var last error
		for i := 0; i < 5; i++ {
			last = p.ss.ForceCleanup(cleanCtx)
			if last == nil {
				break
			}
			if !errors.Is(last, firewall.ErrCleanupIncomplete) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if last != nil {
			errs = append(errs, "firewall: "+last.Error())
		}
		p.ss = nil
	} else if p.cfg.Config != nil {
		// Сироты после потери in-memory SuperSweep (incomplete Stop / crash).
		if ssCfg, err := firewall.SuperSweepConfigFromGetter(p.cfg.Config.Get); err == nil && ssCfg.Backend != "" {
			if wsCfg, werr := firewall.WssizeConfigFromGetter(p.cfg.Config.Get, p.cfg.Paths.ZatorRoot); werr == nil && wsCfg.Active() {
				ssCfg.Wssize = &wsCfg
			}
			ssCfg.ProbeUID = p.cfg.UID
			ssCfg.TrafficUID = firewall.ResolveTrafficUID()
			if p.cfg.PortRangeFile != "" {
				ssCfg.PortRangeFile = p.cfg.PortRangeFile
			}
			if ss, err := firewall.NewSuperSweep(ssCfg, p.cfg.Runner); err == nil {
				var last error
				for i := 0; i < 5; i++ {
					last = ss.ForceCleanup(cleanCtx)
					if last == nil {
						break
					}
					if !errors.Is(last, firewall.ErrCleanupIncomplete) {
						break
					}
					time.Sleep(200 * time.Millisecond)
				}
				if last != nil {
					errs = append(errs, "orphan-firewall: "+last.Error())
				}
			}
		}
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = killProcessGroup(p.pid)
		done := make(chan struct{})
		go func() {
			_ = p.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = p.cmd.Process.Kill()
			<-done
		}
	}
	p.cmd = nil
	p.pid = 0
	p.started = false
	_ = os.Remove(filepath.Join(p.cfg.WorkDir, "nfqws2.pid"))
	if len(errs) > 0 {
		return fmt.Errorf("%w: %s", ErrProbe, strings.Join(errs, "; "))
	}
	return nil
}

func (p *NfqwsProbe) writeProbeLua(luaDir string) error {
	srcDir := p.cfg.Paths.LuaDir
	for _, name := range []string{"locked.lua", "strategy-lock-manager.lua"} {
		src := filepath.Join(srcDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			// Без файла — пропускаем (тесты без полного lua).
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		probeOrch := filepath.ToSlash(p.cfg.OrchestraDir)
		out := strings.ReplaceAll(string(data), productionOrchestraLuaPath, probeOrch)
		out = strings.ReplaceAll(out, "/tmp/auto_locked.tsv", filepath.ToSlash(filepath.Join(p.cfg.OrchestraDir, "auto_locked.tsv")))
		out = strings.ReplaceAll(out, "/tmp/z2r-auto-locked.reload", filepath.ToSlash(filepath.Join(p.cfg.OrchestraDir, "auto_locked.reload")))
		// На случай кастомных корней: замена state/orchestra и cache/orchestra путей.
		for _, old := range []string{
			filepath.ToSlash(filepath.Join(p.cfg.Paths.ExtraStratsDir, "cache", "orchestra")),
			filepath.ToSlash(p.cfg.Paths.StateOrchestraDir()),
		} {
			if old != "" && old != probeOrch {
				out = strings.ReplaceAll(out, old, probeOrch)
			}
		}
		if err := fsutil.AtomicWriteFile(filepath.Join(luaDir, name), []byte(out), 0o644); err != nil {
			return err
		}
	}
	// locked.lua resolves profile blob/clone settings beside its private lock
	// files. Snapshot the user's menu-16 overrides so an isolated run sees the
	// same blob, clone mode, SNI, and size as the production queue.
	for _, name := range []string{
		"blob_override.tsv", "mode_override.tsv", "sni_override.tsv", "clonesize.tsv",
	} {
		src := filepath.Join(p.cfg.Paths.StateOrchestraDir(), name)
		dst := filepath.Join(p.cfg.OrchestraDir, name)
		data, err := os.ReadFile(src)
		if os.IsNotExist(err) {
			if removeErr := os.Remove(dst); removeErr != nil && !os.IsNotExist(removeErr) {
				return removeErr
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("%w: read %s: %v", ErrProbe, src, err)
		}
		if err := fsutil.AtomicWriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("%w: snapshot %s: %v", ErrProbe, name, err)
		}
	}
	return nil
}

func (p *NfqwsProbe) buildArgs(luaDir string) ([]string, error) {
	userName := p.cfg.User
	if userName == "" {
		userName = fmt.Sprintf("%d", p.cfg.UID)
	}
	zapretLua := filepath.ToSlash(filepath.Join(p.cfg.Paths.ZapretRoot, "lua"))
	raw, ok := p.cfg.Config.NFQWS2Opt()
	if !ok {
		return nil, fmt.Errorf("%w: пустой NFQWS2_OPT", ErrProbe)
	}
	if p.profile < 1 || p.profile > 4 {
		return nil, fmt.Errorf("%w: некорректный профиль %d", ErrProbe, p.profile)
	}
	opt := []string{
		"--lua-init=@" + filepath.ToSlash(filepath.Join(luaDir, "locked.lua")),
		"--lua-init=@" + filepath.ToSlash(filepath.Join(p.cfg.Paths.LuaDir, "fake-adapt.lua")),
	}
	lines := strings.Split(raw, "\n")
	template, foundTemplate := false, false
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if !template && line == "--template=z2r_tcp_tls_common" {
			template, foundTemplate = true, true
		}
		if !template {
			if strings.HasPrefix(line, "--blob=") {
				opt = append(opt, line)
			}
			continue
		}
		opt = append(opt, line)
		if line == "--new" {
			break
		}
	}
	if !foundTemplate || len(opt) == 0 || opt[len(opt)-1] != "--new" {
		return nil, fmt.Errorf("%w: не найден общий TLS-шаблон", ErrProbe)
	}
	var wssizeArgs []string
	wssizeIPFile := ""
	if p.profile == 2 {
		ws, err := firewall.WssizeConfigFromGetter(p.cfg.Config.Get, p.cfg.Paths.ZatorRoot)
		if err != nil {
			return nil, fmt.Errorf("%w: WSSIZE config: %v", ErrProbe, err)
		}
		if ws.Active() {
			wssizeIPFile = filepath.ToSlash(ws.IPFile)
			mode := strings.ToLower(strings.TrimSpace(p.cfg.Config.GetDefault("Z2R_WSSIZE_RAMP_MODE", "lite")))
			wsize := p.cfg.Config.GetDefault("Z2R_WSSIZE_RAMP_WSIZE", "1")
			scale := p.cfg.Config.GetDefault("Z2R_WSSIZE_RAMP_SCALE", "1")
			open := p.cfg.Config.GetDefault("Z2R_WSSIZE_RAMP_OPEN", "1")
			wsizeN, wsizeErr := strconv.Atoi(wsize)
			scaleN, scaleErr := strconv.Atoi(scale)
			openN, openErr := strconv.Atoi(open)
			if (mode != "lite" && mode != "full") || wsizeErr != nil || scaleErr != nil || openErr != nil || wsizeN < 0 || wsizeN > 65535 || scaleN < 0 || scaleN > 14 || openN < 1 {
				return nil, fmt.Errorf("%w: invalid WSSIZE ramp parameters", ErrProbe)
			}
			opt = append(opt, "--lua-init=@"+filepath.ToSlash(filepath.Join(p.cfg.Paths.LuaDir, "wssize-ramp.lua")))
			wssizeArgs = []string{
				"--filter-l3=ipv4", "--filter-tcp=443",
				fmt.Sprintf("--filter-mark=0x%08x/0x%08x", ws.Marks.Wssize, ws.Marks.Wssize),
				"--payload=tls_client_hello,http_req,http_reply,unknown,tls_server_hello",
				"--out-range=-s34228", "--out-range=a", "--in-range=a", "--payload=all,empty",
				"--lua-desync=wssize_ramp:mode=" + mode + ":wsize=" + wsize + ":scale=" + scale + ":open=" + open,
				"--new",
			}
		}
	}
	opt = append(opt, wssizeArgs...)
	opt = append(opt, "--filter-tcp=443")
	if p.profile == 2 && len(wssizeArgs) != 0 {
		opt = append(opt, "--ipset="+wssizeIPFile)
	}
	opt = append(opt, "--filter-l7=tls",
		"--payload=tls_client_hello,http_req,http_reply,unknown,tls_server_hello",
		"--out-range=-s34228", "--in-range=-s32768",
		fmt.Sprintf("--lua-desync=circular_locked:key=%d:allow_nohost=1", p.profile))
	block, ok := p.cfg.Config.AutoStandardBlock(fmt.Sprintf("%d", p.profile))
	if !ok {
		return nil, fmt.Errorf("%w: не найден стандартный профиль %d", ErrProbe, p.profile)
	}
	for _, rawLine := range block.Lines {
		for _, arg := range strings.Fields(strings.TrimSpace(rawLine)) {
			if strings.HasPrefix(arg, "--import=") ||
				(strings.HasPrefix(arg, "--lua-desync=") && probeStrategyArg.MatchString(arg)) ||
				arg == "--in-range=x" || strings.HasPrefix(arg, "--payload=tls_client_hello,discord_ip_discovery") {
				opt = append(opt, arg)
			}
		}
	}
	probeConfig := config.Parse(p.cfg.Config.String())
	if err := probeConfig.SetNFQWS2Opt(strings.Join(opt, "\n")); err != nil {
		return nil, fmt.Errorf("%w: build isolated NFQWS2_OPT: %v", ErrProbe, err)
	}
	out := probeConfig.BuildNfqws2RuntimeArgs(zapretLua, userName, fmt.Sprintf("--qnum=%d", firewall.SuperSweepQueue))
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: пустой NFQWS2_OPT", ErrProbe)
	}
	return out, nil
}

func writeArgsFile(path string, args []string) error {
	var b strings.Builder
	for _, a := range args {
		b.WriteString(a)
		b.WriteByte('\n')
	}
	return fsutil.AtomicWriteFile(path, []byte(b.String()), 0o644)
}

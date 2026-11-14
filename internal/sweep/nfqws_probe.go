package sweep

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// NfqwsProbeConfig — параметры изолированного пробника nfqws2.
type NfqwsProbeConfig struct {
	Paths paths.Paths

	// WorkDir — каталог pid/args/lua (по умолчанию StateDir/supersweep-probe).
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
		cfg.WorkDir = filepath.Join(cfg.Paths.StateDir, "supersweep-probe")
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
	for _, name := range []string{"locked.tsv", "locked.manual.tsv"} {
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
			ssCfg.ProbeUID = p.cfg.UID
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
	return nil
}

func (p *NfqwsProbe) buildArgs(luaDir string) ([]string, error) {
	base := p.cfg.Config.BuildNfqws2Args()
	if len(base) == 0 {
		return nil, fmt.Errorf("%w: пустой NFQWS2_OPT", ErrProbe)
	}
	qnum := fmt.Sprintf("--qnum=%d", firewall.SuperSweepQueue)
	userName := p.cfg.User
	if userName == "" {
		userName = fmt.Sprintf("%d", p.cfg.UID)
	}
	userArg := "--user=" + userName

	// Как NFQWS2_OPT_BASE в zapret2 init.d: user + fwmark + zapret-lib/*.
	// Без zapret-lib.lua падает fake-adapt.lua (tls_dissect = nil).
	desyncMark := p.cfg.Config.GetDefault("DESYNC_MARK", "0x40000000")
	zapretLua := filepath.ToSlash(filepath.Join(p.cfg.Paths.ZapretRoot, "lua"))
	prefix := []string{
		userArg,
		"--fwmark=" + desyncMark,
		"--lua-init=@" + zapretLua + "/zapret-lib.lua",
		"--lua-init=@" + zapretLua + "/zapret-antidpi.lua",
		"--lua-init=@" + zapretLua + "/zapret-auto.lua",
		qnum,
	}

	out := make([]string, 0, len(base)+len(prefix))
	out = append(out, prefix...)
	for _, a := range base {
		switch {
		case strings.HasPrefix(a, "--qnum=") || a == "--qnum":
			continue // уже в prefix
		case strings.HasPrefix(a, "--user=") || a == "--user":
			continue
		case strings.HasPrefix(a, "--fwmark=") || a == "--fwmark":
			continue
		case strings.HasPrefix(a, "--lua-init=@"):
			path := strings.TrimPrefix(a, "--lua-init=@")
			baseName := filepath.Base(path)
			switch baseName {
			case "zapret-lib.lua", "zapret-antidpi.lua", "zapret-auto.lua":
				continue // уже в prefix
			case "locked.lua", "strategy-lock-manager.lua":
				out = append(out, "--lua-init=@"+filepath.ToSlash(filepath.Join(luaDir, baseName)))
			default:
				out = append(out, a)
			}
		default:
			out = append(out, a)
		}
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

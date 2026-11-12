package googlevideo

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Mode — источник DNS-событий слушателя.
type Mode string

// Режимы слушателя.
const (
	// ModeOff — слушатель отключён (центральный список остаётся единственным источником).
	ModeOff Mode = "off"
	// ModeUbus — `ubus subscribe dnsmasq.dns` (OpenWrt; dnsmasq-full не нужен).
	ModeUbus Mode = "ubus"
	// ModeSniffer — внешний C-сниффер AF_PACKET (tools/bin/googlevideo-dns-sniffer),
	// печатающий TSV `host<TAB>ipv4<TAB>ttl`.
	ModeSniffer Mode = "sniffer"
)

// DefaultSnifferRelPath — путь сниффера относительно корня установки.
const DefaultSnifferRelPath = "tools/bin/googlevideo-dns-sniffer"

// Ошибки слушателя.
var (
	// ErrListenerDisabled — режим off или источник не определён.
	ErrListenerDisabled = errors.New("googlevideo: слушатель отключён")
	// ErrListenerRunning — слушатель уже запущен.
	ErrListenerRunning = errors.New("googlevideo: слушатель уже запущен")
	// ErrBadMode — неизвестный режим слушателя.
	ErrBadMode = errors.New("googlevideo: неизвестный режим слушателя")
	// ErrEventFormat — строка не является событием DNS-ответа.
	ErrEventFormat = errors.New("googlevideo: строка не является DNS-событием")
)

// ParseMode разбирает «off|ubus|sniffer» (пусто — off).
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(s))); m {
	case "", ModeOff:
		return ModeOff, nil
	case ModeUbus, ModeSniffer:
		return m, nil
	}
	return ModeOff, fmt.Errorf("%w: %q", ErrBadMode, s)
}

// Observer принимает наблюдения; реализует *Collector.
type Observer interface {
	Observe(Observation) (ObserveResult, error)
}

var _ Observer = (*Collector)(nil)

// LineSource — источник строк-событий (процесс или тестовая заглушка). Run
// блокируется до отмены ctx либо до завершения источника; завершение без отмены
// возвращается как ошибка (nil — источник закончил штатно).
type LineSource interface {
	Run(ctx context.Context, emit func(line string)) error
}

// ExecSource запускает внешнюю программу и читает её stdout построчно.
type ExecSource struct {
	Path string
	Args []string
	// Stderr — необязательный приёмник stderr (журнал сниффера).
	Stderr io.Writer
	// Command — фабрика процесса (по умолчанию exec.CommandContext).
	Command func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// Run запускает процесс; при отмене ctx процесс убивается.
func (s ExecSource) Run(ctx context.Context, emit func(line string)) error {
	mk := s.Command
	if mk == nil {
		mk = exec.CommandContext
	}
	cmd := mk(ctx, s.Path, s.Args...)
	if s.Stderr != nil {
		cmd.Stderr = s.Stderr
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		emit(sc.Text())
	}
	scanErr := sc.Err()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil {
		return waitErr
	}
	return scanErr
}

// --- разбор строк ---

// ParseSnifferLine разбирает вывод сниффера `host<TAB>ipv4<TAB>ttl`.
func ParseSnifferLine(line string) (Observation, error) {
	f := strings.Split(strings.TrimRight(line, "\r\n"), "\t")
	if len(f) < 3 || f[0] == "" || f[1] == "" || f[2] == "" {
		return Observation{}, ErrEventFormat
	}
	ttl, err := ParseTTL(f[2])
	if err != nil {
		return Observation{}, err
	}
	return Observation{Host: f[0], IP: f[1], TTL: ttl, Type: "A"}, nil
}

var ubusFieldRe = map[string]*regexp.Regexp{
	"name":    regexp.MustCompile(`"name"\s*:\s*"([^"]*)"`),
	"type":    regexp.MustCompile(`"type"\s*:\s*"([^"]*)"`),
	"address": regexp.MustCompile(`"address"\s*:\s*"([^"]*)"`),
	"ttl":     regexp.MustCompile(`"ttl"\s*:\s*"?([0-9]+)"?`),
}

// ParseUbusEvent разбирает событие `ubus subscribe dnsmasq.dns`:
// {"dns_result":{"name":…,"type":"A","address":…,"ttl":N}}. Строки без
// dns_result и неполные события дают ErrEventFormat; тип не A — ErrRecordType.
func ParseUbusEvent(line string) (Observation, error) {
	if !strings.Contains(line, `"dns_result"`) {
		return Observation{}, ErrEventFormat
	}
	var name, typ, addr, ttl string
	var env struct {
		DNSResult *struct {
			Name    string          `json:"name"`
			Type    string          `json:"type"`
			Address string          `json:"address"`
			TTL     json.RawMessage `json:"ttl"`
		} `json:"dns_result"`
	}
	if err := json.Unmarshal([]byte(line), &env); err == nil && env.DNSResult != nil {
		r := env.DNSResult
		name, typ, addr = r.Name, r.Type, r.Address
		ttl = strings.Trim(strings.TrimSpace(string(r.TTL)), `"`)
	} else {
		get := func(k string) string {
			if m := ubusFieldRe[k].FindStringSubmatch(line); m != nil {
				return m[1]
			}
			return ""
		}
		name, typ, addr, ttl = get("name"), get("type"), get("address"), get("ttl")
	}
	if name == "" || typ == "" || addr == "" || ttl == "" {
		return Observation{}, ErrEventFormat
	}
	if typ != "A" {
		return Observation{}, ErrRecordType
	}
	n, err := ParseTTL(ttl)
	if err != nil {
		return Observation{}, err
	}
	return Observation{Host: name, IP: addr, TTL: n, Type: typ}, nil
}

// --- менеджер ---

// ListenerStatus — снимок состояния слушателя.
type ListenerStatus struct {
	Mode    Mode   `json:"mode"`
	Running bool   `json:"running"`
	Source  string `json:"source"`
	// Счётчики за время жизни Manager.
	Events   uint64 `json:"events"`   // разобранные события
	Accepted uint64 `json:"accepted"` // принятые коллектором
	Deduped  uint64 `json:"deduped"`  // отброшены дедупликацией в RAM
	Rejected uint64 `json:"rejected"` // отклонены правилами (имя/IP/тип/TTL)
	Ignored  uint64 `json:"ignored"`  // не события DNS
	Errors   uint64 `json:"errors"`   // ошибки записи состояния
	Restarts int    `json:"restarts"`
	// LastError — последняя ошибка источника или записи.
	LastError string    `json:"last_error,omitempty"`
	LastEvent time.Time `json:"last_event,omitempty"`
}

// Listener — управление слушателем (реализует *Manager).
type Listener interface {
	Start(ctx context.Context) error
	Stop() error
	Running() bool
	Status() ListenerStatus
}

// ManagerConfig — настройки менеджера слушателя.
type ManagerConfig struct {
	// Mode — off|ubus|sniffer.
	Mode Mode
	// SnifferPath — путь к бинарнику сниффера (режим sniffer).
	SnifferPath string
	// UbusPath — команда ubus (по умолчанию "ubus").
	UbusPath string
	// Observer — получатель наблюдений (обязателен; обычно *Collector).
	Observer Observer
	// Source — готовый источник строк (например, тестовый); перекрывает Mode.
	// Режим при этом определяет только разбор строк.
	Source LineSource
	// Restart — пауза перед перезапуском упавшего источника (по умолчанию 1 с).
	Restart time.Duration
	// DedupMax — размер дедупликации в RAM: после него таблица сбрасывается (по умолчанию 8192).
	DedupMax int
	// Stderr — приёмник stderr внешнего процесса.
	Stderr io.Writer
	// Command — фабрика процесса для ExecSource (по умолчанию exec.CommandContext).
	Command func(ctx context.Context, name string, args ...string) *exec.Cmd
	// Now — источник времени (по умолчанию time.Now).
	Now func() time.Time
	// Logf — необязательный журнал.
	Logf func(format string, args ...any)
}

// Manager запускает внешний источник DNS-событий и передаёт наблюдения
// коллектору; упавший источник перезапускается до Stop.
type Manager struct {
	cfg    ManagerConfig
	parse  func(string) (Observation, error)
	source LineSource

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	status ListenerStatus
	recent map[string]time.Time
}

var _ Listener = (*Manager)(nil)

// NewManager проверяет настройки и собирает источник строк по режиму.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.Observer == nil {
		return nil, fmt.Errorf("googlevideo: Manager требует Observer")
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeOff
	}
	if cfg.Restart <= 0 {
		cfg.Restart = time.Second
	}
	if cfg.DedupMax <= 0 {
		cfg.DedupMax = MaxStateRows
	}
	if cfg.UbusPath == "" {
		cfg.UbusPath = "ubus"
	}
	m := &Manager{cfg: cfg, recent: map[string]time.Time{}}
	m.status.Mode = cfg.Mode
	switch cfg.Mode {
	case ModeOff:
	case ModeUbus:
		m.parse = ParseUbusEvent
		m.source = ExecSource{Path: cfg.UbusPath, Args: []string{"subscribe", "dnsmasq.dns"}, Stderr: cfg.Stderr, Command: cfg.Command}
		m.status.Source = cfg.UbusPath + " subscribe dnsmasq.dns"
	case ModeSniffer:
		if cfg.SnifferPath == "" && cfg.Source == nil {
			return nil, fmt.Errorf("%w: не задан SnifferPath", ErrBadMode)
		}
		m.parse = ParseSnifferLine
		m.source = ExecSource{Path: cfg.SnifferPath, Stderr: cfg.Stderr, Command: cfg.Command}
		m.status.Source = cfg.SnifferPath
	default:
		return nil, fmt.Errorf("%w: %q", ErrBadMode, cfg.Mode)
	}
	if cfg.Source != nil && cfg.Mode != ModeOff {
		m.source = cfg.Source
		m.status.Source = "custom"
	}
	return m, nil
}

func (m *Manager) now() time.Time {
	if m.cfg.Now != nil {
		return m.cfg.Now()
	}
	return time.Now()
}

func (m *Manager) logf(format string, args ...any) {
	if m.cfg.Logf != nil {
		m.cfg.Logf(format, args...)
	}
}

// Mode возвращает режим менеджера.
func (m *Manager) Mode() Mode { return m.cfg.Mode }

// Running сообщает, что цикл слушателя работает.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.done != nil
}

// Status возвращает копию состояния.
func (m *Manager) Status() ListenerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.status
	st.Running = m.done != nil
	return st
}

// Start запускает слушатель в фоне. ErrListenerDisabled — режим off,
// ErrListenerRunning — уже запущен. Остановка — Stop или отмена ctx.
func (m *Manager) Start(ctx context.Context) error {
	if m.cfg.Mode == ModeOff || m.source == nil {
		return ErrListenerDisabled
	}
	m.mu.Lock()
	if m.done != nil {
		m.mu.Unlock()
		return ErrListenerRunning
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.cancel, m.done = cancel, done
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			m.cancel, m.done = nil, nil
			m.mu.Unlock()
			close(done)
		}()
		m.loop(runCtx)
	}()
	return nil
}

// Stop останавливает слушатель и дожидается завершения цикла. Повторный
// вызов и вызов у неработающего слушателя безвредны.
func (m *Manager) Stop() error {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	<-done
	return nil
}

// Wait блокируется, пока слушатель работает.
func (m *Manager) Wait() {
	m.mu.Lock()
	done := m.done
	m.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (m *Manager) loop(ctx context.Context) {
	for ctx.Err() == nil {
		err := m.source.Run(ctx, m.handleLine)
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		m.status.Restarts++
		if err != nil {
			m.status.LastError = err.Error()
		}
		m.mu.Unlock()
		m.logf("googlevideo listener: источник завершился (%v), перезапуск через %s", err, m.cfg.Restart)
		t := time.NewTimer(m.cfg.Restart)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return
		}
	}
}

func (m *Manager) handleLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	obs, err := m.parse(line)
	m.mu.Lock()
	switch {
	case errors.Is(err, ErrEventFormat):
		m.status.Ignored++
		m.mu.Unlock()
		return
	case err != nil:
		m.status.Rejected++
		m.mu.Unlock()
		return
	}
	m.status.Events++
	now := m.now()
	m.status.LastEvent = now
	key := strings.TrimSuffix(strings.ToLower(obs.Host), ".") + "|" + obs.IP
	if exp, ok := m.recent[key]; ok && exp.After(now) {
		m.status.Deduped++
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	if _, err := m.cfg.Observer.Observe(obs); err != nil {
		m.mu.Lock()
		if errors.Is(err, ErrRejected) {
			m.status.Rejected++
		} else {
			m.status.Errors++
			m.status.LastError = err.Error()
		}
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	m.status.Accepted++
	m.recent[key] = now.Add(time.Duration(obs.TTL) * time.Second)
	if len(m.recent) > m.cfg.DedupMax {
		m.recent = map[string]time.Time{}
	}
	m.mu.Unlock()
}

// --- определение режима ---

// DetectOptions — параметры DetectMode; нулевые поля берут значения по умолчанию.
type DetectOptions struct {
	// OpenWrtRelease — файл признака OpenWrt (по умолчанию /etc/openwrt_release).
	OpenWrtRelease string
	// UbusList возвращает вывод `ubus list` (по умолчанию запускает UbusPath).
	UbusList func(ctx context.Context) (string, error)
	// UbusPath — команда ubus (по умолчанию "ubus").
	UbusPath string
	// SnifferPath — путь сниффера; пусто — сниффер не рассматривается.
	SnifferPath string
}

// DetectMode выбирает источник так же, как слушатель оригинала: OpenWrt с
// объектом ubus dnsmasq.dns — ubus; иначе исполняемый сниффер — sniffer;
// иначе off.
func DetectMode(ctx context.Context, o DetectOptions) Mode {
	rel := o.OpenWrtRelease
	if rel == "" {
		rel = "/etc/openwrt_release"
	}
	if _, err := os.Stat(rel); err == nil {
		list := o.UbusList
		if list == nil {
			bin := o.UbusPath
			if bin == "" {
				bin = "ubus"
			}
			list = func(ctx context.Context) (string, error) {
				out, err := exec.CommandContext(ctx, bin, "list").Output()
				return string(out), err
			}
		}
		if out, err := list(ctx); err == nil {
			for _, l := range strings.Split(out, "\n") {
				if strings.TrimSpace(l) == "dnsmasq.dns" {
					return ModeUbus
				}
			}
		}
	}
	if SnifferAvailable(o.SnifferPath) {
		return ModeSniffer
	}
	return ModeOff
}

// SnifferAvailable сообщает, что path — обычный исполняемый файл (на
// Windows достаточно существования).
func SnifferAvailable(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return st.Mode().Perm()&0o111 != 0
}

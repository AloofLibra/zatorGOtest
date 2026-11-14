package googlevideo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- процесс-помощник: тестовый бинарник сам играет роль сниффера/ubus ---

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_GV_HELPER") != "1" {
		return
	}
	mode := os.Getenv("GV_HELPER_MODE")
	switch mode {
	case "sniffer":
		fmt.Printf("%s\t9.9.9.9\t300\n", hostA)
		fmt.Printf("%s\t8.8.4.4\t300\n", hostB)
		fmt.Printf("evil.example.com\t1.1.1.1\t300\n") // отклоняется коллектором
		fmt.Printf("garbage line\n")                   // не событие
		os.Exit(0)
	case "ubus":
		fmt.Printf(`{ "dns_result": { "name": "%s", "type": "A", "address": "9.9.9.9", "ttl": 120 } }`+"\n", hostA)
		fmt.Printf(`{ "dns_result": { "name": "%s", "type": "AAAA", "address": "::1", "ttl": 120 } }`+"\n", hostB)
		os.Exit(0)
	case "block":
		fmt.Printf("%s\t9.9.9.9\t300\n", hostA)
		_ = os.Stdout.Sync()
		time.Sleep(time.Minute)
		os.Exit(0)
	case "fail":
		fmt.Printf("%s\t9.9.9.9\t300\n", hostA)
		os.Exit(3)
	}
	os.Exit(0)
}

func helperCommand(mode string) func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperProcess$")
		cmd.Env = append(os.Environ(), "GO_WANT_GV_HELPER=1", "GV_HELPER_MODE="+mode)
		return cmd
	}
}

// --- фиктивный источник ---

type fakeSource struct {
	mu    sync.Mutex
	runs  int
	lines [][]string // строки по номеру запуска (последний набор повторяется)
	errs  []error    // ошибки по номеру запуска; nil = ждать отмены контекста
	ran   chan int
}

func (f *fakeSource) Run(ctx context.Context, emit func(string)) error {
	f.mu.Lock()
	n := f.runs
	f.runs++
	var lines []string
	if len(f.lines) > 0 {
		lines = f.lines[min(n, len(f.lines)-1)]
	}
	var err error
	if n < len(f.errs) {
		err = f.errs[n]
	}
	f.mu.Unlock()
	for _, l := range lines {
		emit(l)
	}
	if f.ran != nil {
		select {
		case f.ran <- n:
		default:
		}
	}
	if err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeSource) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"": ModeOff, "off": ModeOff, " UBUS ": ModeUbus, "ubus": ModeUbus, "Sniffer": ModeSniffer} {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseMode("netlink"); !errors.Is(err, ErrBadMode) {
		t.Errorf("unknown mode: %v", err)
	}
}

func TestParseSnifferLine(t *testing.T) {
	obs, err := ParseSnifferLine(hostA + "\t9.9.9.9\t300\r\n")
	if err != nil || obs.Host != hostA || obs.IP != "9.9.9.9" || obs.TTL != 300 || obs.Type != "A" {
		t.Errorf("good line: %+v %v", obs, err)
	}
	if obs, err := ParseSnifferLine(hostA + "\t9.9.9.9\t99999999"); err != nil || obs.TTL != MaxTTLSeconds {
		t.Errorf("long ttl: %+v %v", obs, err)
	}
	for _, bad := range []string{"", "x", "a\tb", "\t9.9.9.9\t300", hostA + "\t\t300", hostA + "\t9.9.9.9\t"} {
		if _, err := ParseSnifferLine(bad); !errors.Is(err, ErrEventFormat) {
			t.Errorf("ParseSnifferLine(%q) = %v, want ErrEventFormat", bad, err)
		}
	}
	for _, bad := range []string{hostA + "\t9.9.9.9\tabc", hostA + "\t9.9.9.9\t0"} {
		if _, err := ParseSnifferLine(bad); !errors.Is(err, ErrTTL) {
			t.Errorf("ParseSnifferLine(%q) = %v, want ErrTTL", bad, err)
		}
	}
}

func TestParseUbusEvent(t *testing.T) {
	good := []string{
		`{ "dns_result": { "name": "` + hostA + `", "type": "A", "address": "9.9.9.9", "ttl": 120 } }`,
		`{"dns_result":{"name":"` + hostA + `","type":"A","address":"9.9.9.9","ttl":120}}`,
		`{"dns_result":{"ttl":"120","address":"9.9.9.9","type":"A","name":"` + hostA + `"}}`,
		// не совсем валидный JSON — разбор по регулярным выражениям, как в оригинале
		`{ "dns_result": { "name": "` + hostA + `", "type": "A", "address": "9.9.9.9", "ttl": 120, } `,
	}
	for _, line := range good {
		obs, err := ParseUbusEvent(line)
		if err != nil || obs.Host != hostA || obs.IP != "9.9.9.9" || obs.TTL != 120 || obs.Type != "A" {
			t.Errorf("ParseUbusEvent(%q) = %+v, %v", line, obs, err)
		}
	}
	for _, line := range []string{
		``, `{}`, `{"other":{"name":"x"}}`, `not json`,
		`{"dns_result":{"name":"` + hostA + `","type":"A","address":"9.9.9.9"}}`,
		`{"dns_result":{"type":"A","address":"9.9.9.9","ttl":1}}`,
		`{"dns_result":{"name":"` + hostA + `","type":"A","ttl":1}}`,
	} {
		if _, err := ParseUbusEvent(line); !errors.Is(err, ErrEventFormat) {
			t.Errorf("ParseUbusEvent(%q) = %v, want ErrEventFormat", line, err)
		}
	}
	if _, err := ParseUbusEvent(`{"dns_result":{"name":"` + hostA + `","type":"AAAA","address":"::1","ttl":9}}`); !errors.Is(err, ErrRecordType) {
		t.Errorf("AAAA: %v", err)
	}
	if _, err := ParseUbusEvent(`{"dns_result":{"name":"` + hostA + `","type":"A","address":"9.9.9.9","ttl":0}}`); !errors.Is(err, ErrTTL) {
		t.Errorf("zero ttl: %v", err)
	}
}

func TestNewManager_Validation(t *testing.T) {
	col, _ := newCollector(t, nil)
	if _, err := NewManager(ManagerConfig{Mode: ModeUbus}); err == nil {
		t.Error("Observer is required")
	}
	if _, err := NewManager(ManagerConfig{Mode: "bogus", Observer: col}); !errors.Is(err, ErrBadMode) {
		t.Errorf("bad mode: %v", err)
	}
	if _, err := NewManager(ManagerConfig{Mode: ModeSniffer, Observer: col}); !errors.Is(err, ErrBadMode) {
		t.Errorf("sniffer without path: %v", err)
	}
	m, err := NewManager(ManagerConfig{Mode: ModeSniffer, Observer: col, Source: &fakeSource{}})
	if err != nil || m.Mode() != ModeSniffer {
		t.Errorf("sniffer with custom source: %v", err)
	}
	m, err = NewManager(ManagerConfig{Mode: ModeUbus, Observer: col})
	if err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); st.Mode != ModeUbus || st.Running || st.Source != "ubus subscribe dnsmasq.dns" {
		t.Errorf("status = %+v", st)
	}
	m, err = NewManager(ManagerConfig{Observer: col})
	if err != nil || m.Mode() != ModeOff {
		t.Fatalf("default mode: %v", err)
	}
	if err := m.Start(context.Background()); !errors.Is(err, ErrListenerDisabled) {
		t.Errorf("off mode start: %v", err)
	}
	if m.Running() || m.Stop() != nil {
		t.Error("off listener: Stop must be a no-op")
	}
}

func TestManager_ProcessesEventsAndDeduplicates(t *testing.T) {
	col, clk := newCollector(t, nil)
	src := &fakeSource{lines: [][]string{{
		hostA + "\t9.9.9.9\t300",
		hostA + "\t9.9.9.9\t300", // дубль в пределах TTL
		strings.ToUpper(hostA) + "\t9.9.9.9\t300",
		hostB + "\t8.8.4.4\t300",
		"evil.example.com\t1.1.1.1\t300", // чужой домен — Rejected
		hostC + "\t10.0.0.1\t300",        // непубличный — Rejected
		hostC + "\t1.1.1.1\t0",           // нулевой TTL — Rejected
		"garbage",                        // Ignored
		"",
	}}, ran: make(chan int, 1)}
	m, err := NewManager(ManagerConfig{Mode: ModeSniffer, Observer: col, Source: src, Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); !errors.Is(err, ErrListenerRunning) {
		t.Errorf("second Start: %v", err)
	}
	<-src.ran
	if !m.Running() {
		t.Error("Running")
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if m.Running() || m.Stop() != nil {
		t.Error("Stop must be idempotent")
	}
	st := m.Status()
	// Разобрано 6 событий (нулевой TTL отсеян ещё при разборе строки).
	if st.Running || st.Events != 6 {
		t.Errorf("status = %+v", st)
	}
	if st.Accepted != 2 || st.Deduped != 2 || st.Rejected != 3 || st.Ignored != 1 {
		t.Errorf("counters = %+v", st)
	}
	if st.LastEvent.IsZero() || st.Errors != 0 {
		t.Errorf("status = %+v", st)
	}
	if got := len(col.Local()); got != 2 {
		t.Errorf("local rows = %d, want 2", got)
	}
	if got := len(col.Pending()); got != 2 {
		t.Errorf("pending rows = %d, want 2", got)
	}
}

func TestManager_DedupExpiresWithTTL(t *testing.T) {
	col, clk := newCollector(t, nil)
	src := &fakeSource{ran: make(chan int, 4)}
	m, _ := NewManager(ManagerConfig{Mode: ModeSniffer, Observer: col, Source: src, Now: clk.Now})
	line := hostA + "\t9.9.9.9\t60"

	m.handleLine(line)
	clk.Advance(30 * time.Second)
	m.handleLine(line)
	if st := m.Status(); st.Accepted != 1 || st.Deduped != 1 {
		t.Fatalf("within ttl: %+v", st)
	}
	clk.Advance(31 * time.Second)
	m.handleLine(line)
	if st := m.Status(); st.Accepted != 2 || st.Deduped != 1 {
		t.Errorf("after ttl: %+v", st)
	}
}

func TestManager_DedupTableReset(t *testing.T) {
	col, clk := newCollector(t, nil)
	m, _ := NewManager(ManagerConfig{Mode: ModeSniffer, Observer: col, Source: &fakeSource{}, Now: clk.Now, DedupMax: 2})
	for i := 1; i <= 3; i++ {
		m.handleLine(fmt.Sprintf("rr1---sn-h%d.googlevideo.com\t11.0.0.%d\t600", i, i))
	}
	if len(m.recent) != 0 {
		t.Errorf("table must reset after exceeding DedupMax, has %d", len(m.recent))
	}
	// После сброса пара принимается заново (Observer сам дедуплицирует очередь).
	m.handleLine("rr1---sn-h1.googlevideo.com\t11.0.0.1\t600")
	if st := m.Status(); st.Accepted != 4 || st.Deduped != 0 {
		t.Errorf("status = %+v", st)
	}
	if len(col.Pending()) != 3 {
		t.Errorf("collector must still hold three pairs: %d", len(col.Pending()))
	}
}

func TestManager_UbusModeParsesEvents(t *testing.T) {
	col, clk := newCollector(t, nil)
	src := &fakeSource{lines: [][]string{{
		`{ "dns_result": { "name": "` + hostA + `", "type": "A", "address": "9.9.9.9", "ttl": 120 } }`,
		`{ "dns_result": { "name": "` + hostB + `", "type": "AAAA", "address": "::1", "ttl": 120 } }`,
		`{ "other": 1 }`,
	}}, ran: make(chan int, 1)}
	m, _ := NewManager(ManagerConfig{Mode: ModeUbus, Observer: col, Source: src, Now: clk.Now})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-src.ran
	_ = m.Stop()
	st := m.Status()
	if st.Accepted != 1 || st.Rejected != 1 || st.Ignored != 1 || st.Source != "custom" {
		t.Errorf("status = %+v", st)
	}
	if p := col.Pending(); len(p) != 1 || p[0].IP != "9.9.9.9" {
		t.Errorf("pending = %+v", p)
	}
}

func TestManager_RestartsFailedSource(t *testing.T) {
	col, clk := newCollector(t, nil)
	src := &fakeSource{
		lines: [][]string{{hostA + "\t9.9.9.9\t300"}, {hostB + "\t8.8.4.4\t300"}},
		errs:  []error{errors.New("sniffer crashed")},
		ran:   make(chan int, 4),
	}
	var logs []string
	var lmu sync.Mutex
	m, _ := NewManager(ManagerConfig{
		Mode: ModeSniffer, Observer: col, Source: src, Now: clk.Now, Restart: time.Millisecond,
		Logf: func(f string, a ...any) { lmu.Lock(); logs = append(logs, fmt.Sprintf(f, a...)); lmu.Unlock() },
	})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restart", func() bool { return src.runCount() >= 2 })
	waitFor(t, "second source events", func() bool { return m.Status().Accepted == 2 })
	_ = m.Stop()
	st := m.Status()
	if st.Restarts != 1 || st.LastError != "sniffer crashed" {
		t.Errorf("status = %+v", st)
	}
	lmu.Lock()
	defer lmu.Unlock()
	if len(logs) == 0 || !strings.Contains(logs[0], "перезапуск") {
		t.Errorf("logs = %v", logs)
	}
}

func TestManager_StopInterruptsRestartPause(t *testing.T) {
	col, _ := newCollector(t, nil)
	src := &fakeSource{errs: []error{errors.New("boom"), errors.New("boom"), errors.New("boom")}}
	m, _ := NewManager(ManagerConfig{Mode: ModeSniffer, Observer: col, Source: src, Restart: time.Hour})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first run", func() bool { return src.runCount() == 1 })
	done := make(chan struct{})
	go func() { _ = m.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop must interrupt the restart delay")
	}
	if src.runCount() != 1 {
		t.Errorf("runs = %d", src.runCount())
	}
}

func TestManager_ParentContextStops(t *testing.T) {
	col, _ := newCollector(t, nil)
	src := &fakeSource{}
	m, _ := NewManager(ManagerConfig{Mode: ModeSniffer, Observer: col, Source: src})
	ctx, cancel := context.WithCancel(context.Background())
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "run", func() bool { return src.runCount() == 1 })
	cancel()
	waitFor(t, "stopped", func() bool { return !m.Running() })
	m.Wait() // не блокируется после остановки
	// Менеджер можно запустить заново.
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("restart after stop: %v", err)
	}
	_ = m.Stop()
}

// failingObserver возвращает не-отклоняющую ошибку записи.
type failingObserver struct{}

func (failingObserver) Observe(Observation) (ObserveResult, error) {
	return ObserveResult{}, errors.New("disk full")
}

func TestManager_ObserverWriteErrorsAreCounted(t *testing.T) {
	m, _ := NewManager(ManagerConfig{Mode: ModeSniffer, Observer: failingObserver{}, Source: &fakeSource{}})
	m.handleLine(hostA + "\t9.9.9.9\t300")
	m.handleLine(hostA + "\t9.9.9.9\t300")
	st := m.Status()
	if st.Errors != 2 || st.Accepted != 0 || st.Rejected != 0 || st.LastError != "disk full" {
		t.Errorf("status = %+v", st)
	}
	// Ошибка записи не включает дедупликацию: пара будет повторена.
	if st.Deduped != 0 {
		t.Errorf("deduped = %d", st.Deduped)
	}
}

func TestExecSource_SnifferProcess(t *testing.T) {
	col, clk := newCollector(t, nil)
	m, err := NewManager(ManagerConfig{
		Mode: ModeSniffer, SnifferPath: "unused", Observer: col, Now: clk.Now,
		Command: helperCommand("sniffer"), Restart: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "sniffer lines", func() bool { st := m.Status(); return st.Accepted == 2 && st.Rejected == 1 && st.Ignored == 1 })
	waitFor(t, "process exit noticed", func() bool { return m.Status().Restarts == 1 })
	_ = m.Stop()
	if p := col.Pending(); len(p) != 2 || p[0].Host != hostA || p[1].IP != "8.8.4.4" {
		t.Errorf("pending = %+v", p)
	}
}

func TestExecSource_UbusProcess(t *testing.T) {
	col, clk := newCollector(t, nil)
	m, _ := NewManager(ManagerConfig{Mode: ModeUbus, Observer: col, Now: clk.Now, Command: helperCommand("ubus"), Restart: time.Hour})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ubus events", func() bool { st := m.Status(); return st.Accepted == 1 && st.Rejected == 1 })
	_ = m.Stop()
}

func TestExecSource_StopKillsProcess(t *testing.T) {
	col, clk := newCollector(t, nil)
	m, _ := NewManager(ManagerConfig{Mode: ModeSniffer, SnifferPath: "x", Observer: col, Now: clk.Now, Command: helperCommand("block")})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first event", func() bool { return m.Status().Accepted == 1 })
	done := make(chan struct{})
	go func() { _ = m.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop must kill the child process")
	}
}

func TestExecSource_ExitCodeAndStartErrors(t *testing.T) {
	var got []string
	emit := func(l string) { got = append(got, l) }

	err := ExecSource{Command: helperCommand("fail")}.Run(context.Background(), emit)
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 || len(got) != 1 {
		t.Errorf("exit code: err=%v lines=%v", err, got)
	}
	got = nil
	if err := (ExecSource{Command: helperCommand("sniffer")}).Run(context.Background(), emit); err != nil || len(got) != 4 {
		t.Errorf("clean exit: err=%v lines=%d", err, len(got))
	}
	if err := (ExecSource{Path: filepath.Join(t.TempDir(), "no-such-binary")}).Run(context.Background(), emit); err == nil {
		t.Error("missing binary must fail to start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (ExecSource{Command: helperCommand("block")}).Run(ctx, emit); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

func TestDetectMode(t *testing.T) {
	dir := t.TempDir()
	release := filepath.Join(dir, "openwrt_release")
	sniffer := filepath.Join(dir, "sniffer")
	if err := os.WriteFile(sniffer, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	ubusWith := func(out string, err error) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return out, err }
	}
	ctx := context.Background()

	// Не OpenWrt, сниффера нет.
	if got := DetectMode(ctx, DetectOptions{OpenWrtRelease: release}); got != ModeOff {
		t.Errorf("nothing available: %s", got)
	}
	// Не OpenWrt, есть сниффер.
	if got := DetectMode(ctx, DetectOptions{OpenWrtRelease: release, SnifferPath: sniffer}); got != ModeSniffer {
		t.Errorf("sniffer only: %s", got)
	}
	// OpenWrt с dnsmasq.dns — ubus важнее сниффера.
	if err := os.WriteFile(release, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := DetectOptions{OpenWrtRelease: release, SnifferPath: sniffer, UbusList: ubusWith("network\ndnsmasq.dns\nsystem\n", nil)}
	if got := DetectMode(ctx, opts); got != ModeUbus {
		t.Errorf("ubus: %s", got)
	}
	// OpenWrt без dnsmasq.dns — запасной сниффер; ошибка ubus — тоже.
	opts.UbusList = ubusWith("network\ndnsmasq\n", nil)
	if got := DetectMode(ctx, opts); got != ModeSniffer {
		t.Errorf("no dnsmasq.dns: %s", got)
	}
	opts.UbusList = ubusWith("", errors.New("ubus: not found"))
	if got := DetectMode(ctx, opts); got != ModeSniffer {
		t.Errorf("ubus error: %s", got)
	}
	opts.SnifferPath = ""
	if got := DetectMode(ctx, opts); got != ModeOff {
		t.Errorf("no sources: %s", got)
	}
	// Реальный вызов ubus отсутствующей командой — тоже off.
	if got := DetectMode(ctx, DetectOptions{OpenWrtRelease: release, UbusPath: filepath.Join(dir, "no-ubus")}); got != ModeOff {
		t.Errorf("missing ubus binary: %s", got)
	}
}

func TestSnifferAvailable(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "sniffer")
	if err := os.WriteFile(file, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !SnifferAvailable(file) {
		t.Error("regular executable file")
	}
	if SnifferAvailable(dir) || SnifferAvailable(filepath.Join(dir, "none")) || SnifferAvailable("") {
		t.Error("directory/missing/empty path must be unavailable")
	}
	if DefaultSnifferRelPath != "tools/bin/googlevideo-dns-sniffer" {
		t.Error("sniffer path must match the reference layout")
	}
}

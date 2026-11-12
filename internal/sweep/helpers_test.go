package sweep

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/AloofLibra/zator/internal/netcheck"
	"github.com/AloofLibra/zator/internal/orchestra"
)

// --- конструкторы результатов проверки ---

func vd(code int) netcheck.VersionDetail {
	return netcheck.VersionDetail{Code: code, Proto: "HTTP/2", Time: "0.1", IP: "1.1.1.1", State: netcheck.VersionState(code, netcheck.ErrNone)}
}

func vdFail() netcheck.VersionDetail {
	return netcheck.VersionDetail{Proto: "-", Time: "-", IP: "-", State: netcheck.StateTLS, Err: netcheck.ErrTLS}
}

func dlRes(size int, t string) *netcheck.Download {
	return &netcheck.Download{Code: 206, Size: size, Time: t, State: netcheck.DLOK}
}

// resGood — обе TLS 2xx, докачка 64 КБ за t секунд (скорость обратно пропорциональна t).
func resGood(t string) netcheck.TargetResult {
	return netcheck.TargetResult{TLS12: 1, TLS13: 1, TLS12Detail: vd(200), TLS13Detail: vd(200), Download: dlRes(65536, t)}
}

// resOnly12 — отвечает только TLS 1.2 (warn при any/без требований).
func resOnly12() netcheck.TargetResult {
	return netcheck.TargetResult{TLS12: 1, TLS12Detail: vd(200), TLS13Detail: vdFail(), Download: dlRes(65536, "1.0")}
}

func resFail() netcheck.TargetResult {
	return netcheck.TargetResult{TLS12Detail: vdFail(), TLS13Detail: vdFail()}
}

func resCut() netcheck.TargetResult {
	return netcheck.TargetResult{
		TLS12: 1, TLS13: 1, TLS12Detail: vd(200), TLS13Detail: vd(200),
		Download: &netcheck.Download{Code: 206, Size: 1000, Time: "1.0", State: netcheck.DLCut, Err: netcheck.ErrTimeout},
	}
}

// --- скриптовый проверяльщик ---

// scripted решает результат по текущему локу цели в хранилище: так проба
// видит ровно ту стратегию, которую только что поставил координатор.
type scripted struct {
	store *orchestra.Store
	scope string

	mu     sync.Mutex
	keyFor map[string]string // URL -> ключ лока (профиль или домен)
	decide func(key string, strategy int) netcheck.TargetResult
	hook   func(call int, key string, strategy int, ctx context.Context)
	calls  int
	log    []string
}

func newScripted(st *orchestra.Store, keyFor map[string]string, decide func(key string, strategy int) netcheck.TargetResult) *scripted {
	return &scripted{store: st, scope: "default", keyFor: keyFor, decide: decide}
}

func (s *scripted) CheckTarget(ctx context.Context, label, url string) netcheck.TargetResult {
	s.mu.Lock()
	s.calls++
	call := s.calls
	key := s.keyFor[url]
	s.mu.Unlock()
	strat, _ := s.store.GetLock(s.scope, key, "tls")
	n := int(strat)
	s.mu.Lock()
	s.log = append(s.log, key+":"+strconv.Itoa(n))
	s.mu.Unlock()
	if s.hook != nil {
		s.hook(call, key, n, ctx)
	}
	res := s.decide(key, n)
	res.Label, res.Target = label, url
	return res
}

func (s *scripted) callLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.log...)
}

// --- хранилище ---

func newStore(t *testing.T) *orchestra.Store {
	t.Helper()
	return orchestra.NewStore(filepath.Join(t.TempDir(), "orch"))
}

func mustGet(t *testing.T, st *orchestra.Store, profile, proto string) orchestra.Strategy {
	t.Helper()
	v, err := st.GetLock("default", profile, proto)
	if err != nil {
		t.Fatalf("GetLock(%s,%s): %v", profile, proto, err)
	}
	return v
}

func mustSet(t *testing.T, st Locker, profile, proto string, s int) {
	t.Helper()
	if err := st.SetLock("default", profile, proto, orchestra.Strategy(s)); err != nil {
		t.Fatalf("SetLock(%s,%s,%d): %v", profile, proto, s, err)
	}
}

// failingLocker — Locker, у которого SetLock падает после n успешных вызовов.
type failingLocker struct {
	Locker
	okCalls int
	calls   int
}

func (f *failingLocker) SetLock(scope, profile, proto string, s orchestra.Strategy) error {
	f.calls++
	if f.calls > f.okCalls {
		return errors.New("disk full")
	}
	return f.Locker.SetLock(scope, profile, proto, s)
}

// noSleep запоминает паузы и не спит; отмену контекста учитывает.
type sleepRec struct {
	mu sync.Mutex
	ds []time.Duration
}

func (r *sleepRec) sleep(ctx context.Context, d time.Duration) error {
	r.mu.Lock()
	r.ds = append(r.ds, d)
	r.mu.Unlock()
	return ctx.Err()
}

func (r *sleepRec) list() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.ds...)
}

// recordingProbe — Probe, записывающий вызовы.
type recordingProbe struct {
	mu       sync.Mutex
	starts   []int
	stops    int
	startErr map[int]error
	healthy  func(call int) bool
	hcalls   int
}

func (p *recordingProbe) Start(ctx context.Context, profile int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts = append(p.starts, profile)
	return p.startErr[profile]
}

func (p *recordingProbe) Healthy(ctx context.Context) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hcalls++
	if p.healthy == nil {
		return true
	}
	return p.healthy(p.hcalls)
}

func (p *recordingProbe) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stops++
	return nil
}

func (p *recordingProbe) startList() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.starts...)
}

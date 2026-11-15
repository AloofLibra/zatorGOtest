package googlevideo

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SeedStatus — снимок фонового автопробоя для UI.
type SeedStatus struct {
	State      string `json:"state"` // idle|probing|disabled
	HostKey    string `json:"host_key,omitempty"`
	Strategy   int    `json:"strategy,omitempty"`
	LastHost   string `json:"last_host,omitempty"`
	LastStrat  int    `json:"last_strategy,omitempty"`
	LastResult string `json:"last_result,omitempty"` // ok|fail|seeded|unknown|error
	LastError  string `json:"last_error,omitempty"`
	UpdatedAt  int64  `json:"updated_at,omitempty"`
	Pending    int    `json:"pending"`
}

// ProbeFunc пробует стратегию против FQDN; ok=true если TLS зелёный.
type ProbeFunc func(ctx context.Context, fqdn string, strategy int) (ok bool, detail string, err error)

// SeederConfig — параметры фонового seed.
type SeederConfig struct {
	// LivePath — /tmp/auto_locked.tsv
	LivePath string
	// StatePath — backup state/orchestra/auto_locked.tsv
	StatePath string
	// ListHosts — текущий googlevideo-hosts.txt
	ListHosts func() ([]string, error)
	// Probe — изолированная/живая TLS-проба (обязательна для реального пробоя;
	// если nil — только transfer-seed без сети).
	Probe ProbeFunc
	// DefaultStrat / MaxStrat / TailMax — кандидаты.
	DefaultStrat int
	MaxStrat     int
	TailMax      int
	// Interval между циклами.
	Interval time.Duration
	// Cooldown после обработки одного sn.
	Cooldown time.Duration
	// OnSeeded — после успешной записи (опционально: refresh ipv4/wssize).
	OnSeeded func(hostKey string, strategy int)
}

// Seeder — фоновый автопробой новых sn-*.
type Seeder struct {
	cfg SeederConfig

	mu     sync.Mutex
	status SeedStatus
	stop   context.CancelFunc
}

// NewSeeder создаёт seeder (ещё не запущен).
func NewSeeder(cfg SeederConfig) *Seeder {
	if cfg.DefaultStrat < 1 {
		cfg.DefaultStrat = 41
	}
	if cfg.MaxStrat < 1 {
		cfg.MaxStrat = 44
	}
	if cfg.TailMax < 0 {
		cfg.TailMax = 8
	}
	if cfg.TailMax == 0 {
		cfg.TailMax = 8
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 45 * time.Second
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 3 * time.Second
	}
	return &Seeder{
		cfg:    cfg,
		status: SeedStatus{State: "idle"},
	}
}

// Status возвращает копию статуса.
func (s *Seeder) Status() SeedStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *Seeder) setStatus(mut func(*SeedStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mut(&s.status)
	s.status.UpdatedAt = time.Now().Unix()
}

// Start запускает цикл в фоне. Повторный Start — no-op.
func (s *Seeder) Start(ctx context.Context) {
	s.mu.Lock()
	if s.stop != nil {
		s.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.stop = cancel
	s.mu.Unlock()
	go s.loop(runCtx)
}

// Stop останавливает цикл.
func (s *Seeder) Stop() {
	s.mu.Lock()
	cancel := s.stop
	s.stop = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.setStatus(func(st *SeedStatus) { st.State = "idle"; st.HostKey = ""; st.Strategy = 0 })
}

func (s *Seeder) loop(ctx context.Context) {
	t := time.NewTimer(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
			t.Reset(s.cfg.Interval)
		}
	}
}

func (s *Seeder) loadLocks() []AutoLock {
	live, _ := ParseAutoLockedTSV(s.cfg.LivePath)
	backup, _ := ParseAutoLockedTSV(s.cfg.StatePath)
	locks := FilterAskey(MergeAutoLocked(live, backup), "2")
	defaultScope := make([]AutoLock, 0, len(locks))
	for _, lock := range locks {
		if lock.Scope == "" || lock.Scope == "default" {
			defaultScope = append(defaultScope, lock)
		}
	}
	return defaultScope
}

func (s *Seeder) tick(ctx context.Context) {
	if s.cfg.ListHosts == nil {
		return
	}
	hosts, err := s.cfg.ListHosts()
	if err != nil || len(hosts) == 0 {
		return
	}
	locks := s.loadLocks()
	missing := MissingHostKeys(hosts, locks)
	s.setStatus(func(st *SeedStatus) {
		st.Pending = len(missing)
		if st.State != "probing" {
			st.State = "idle"
		}
	})
	if len(missing) == 0 {
		return
	}

	// 1) Transfer-seed без сети, если уже есть устойчивый majority.
	if transfer, n := MostCommonStrategy(locks); n >= 2 {
		for _, hk := range missing {
			if ctx.Err() != nil {
				return
			}
			if err := s.commit(hk, transfer, SourceSeed); err != nil {
				s.setStatus(func(st *SeedStatus) {
					st.LastResult = "error"
					st.LastError = err.Error()
					st.LastHost = hk
				})
				continue
			}
			s.setStatus(func(st *SeedStatus) {
				st.LastHost = hk
				st.LastStrat = transfer
				st.LastResult = "seeded"
				st.LastError = ""
				st.Pending = st.Pending - 1
			})
			if s.cfg.OnSeeded != nil {
				s.cfg.OnSeeded(hk, transfer)
			}
		}
		return
	}

	// 2) Узкий probe по кандидатам (один sn за тик).
	hk := missing[0]
	fqdn := PickFQDNForHostKey(hosts, hk)
	if fqdn == "" {
		return
	}
	cands := CandidateStrategies(locks, s.cfg.DefaultStrat, s.cfg.MaxStrat, s.cfg.TailMax)
	if s.cfg.Probe == nil {
		// Never turn an untested default into a lock or transfer evidence.
		s.setStatus(func(st *SeedStatus) {
			st.LastHost = hk
			st.LastStrat = 0
			st.LastResult = "unknown"
			st.LastError = "probe_unavailable"
		})
		return
	}

	for _, strat := range cands {
		if ctx.Err() != nil {
			return
		}
		s.setStatus(func(st *SeedStatus) {
			st.State = "probing"
			st.HostKey = hk
			st.Strategy = strat
		})
		ok, detail, err := s.cfg.Probe(ctx, fqdn, strat)
		if err != nil {
			s.setStatus(func(st *SeedStatus) {
				st.State = "idle"
				st.HostKey = ""
				st.Strategy = 0
				st.LastHost = hk
				st.LastStrat = strat
				st.LastResult = "error"
				st.LastError = err.Error()
			})
			return
		}
		if !ok {
			s.setStatus(func(st *SeedStatus) {
				st.LastHost = hk
				st.LastStrat = strat
				st.LastResult = "fail"
				st.LastError = detail
			})
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.cfg.Cooldown):
			}
			continue
		}
		if err := s.commit(hk, strat, SourceLearned); err != nil {
			s.setStatus(func(st *SeedStatus) {
				st.State = "idle"
				st.LastResult = "error"
				st.LastError = err.Error()
			})
			return
		}
		s.setStatus(func(st *SeedStatus) {
			st.State = "idle"
			st.HostKey = ""
			st.Strategy = 0
			st.LastHost = hk
			st.LastStrat = strat
			st.LastResult = "ok"
			st.LastError = ""
		})
		if s.cfg.OnSeeded != nil {
			s.cfg.OnSeeded(hk, strat)
		}
		return
	}
	s.setStatus(func(st *SeedStatus) {
		st.State = "idle"
		st.HostKey = ""
		st.Strategy = 0
		st.LastHost = hk
		st.LastResult = "fail"
		st.LastError = fmt.Sprintf("нет зелёной стратегии среди %d кандидатов", len(cands))
	})
}

func (s *Seeder) commit(hostKey string, strategy int, source string) error {
	lock := AutoLock{
		Askey:    "2",
		Scope:    "default",
		Host:     hostKey,
		Strategy: strategy,
		Source:   source,
	}
	if s.cfg.StatePath != "" {
		if err := UpsertAutoLock(s.cfg.StatePath, lock); err != nil {
			return fmt.Errorf("persist auto-lock state: %w", err)
		}
	}
	if err := UpsertAutoLock(s.cfg.LivePath, lock); err != nil {
		return fmt.Errorf("persist live auto-lock: %w", err)
	}
	return nil
}

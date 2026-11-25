package sweep

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/AloofLibra/zator/internal/netcheck"
	"github.com/AloofLibra/zator/internal/orchestra"
)

func TestCoordinator_QuickProfile5_QUIC(t *testing.T) {
	var sawRounds int
	e := newCoordEnv(t, fullScenario, func(c *Config) {
		c.MaxStrategy = func(p int) int { return 3 }
		c.QUICProbe = func(_ context.Context, strategy int, rounds int) netcheck.TargetResult {
			sawRounds = rounds
			if strategy == 2 {
				return netcheck.TargetResult{
					Verdict:  netcheck.VerdictOK,
					Text:     "HTTP/3 OK (5/5)",
					Download: &netcheck.Download{Size: 5 << 20, Time: "1.000"},
				}
			}
			return netcheck.TargetResult{
				Verdict: netcheck.VerdictFail,
				Text:    "timeout",
			}
		}
	})
	// Pre-lock profile 5 to strategy 1; winner 2 must replace it cleanly.
	mustSet(t, e.st, "5", "udp", 1)

	rep, err := e.c.Run(context.Background(), Params{QuickProfile: 5, QUICRounds: 3})
	if err != nil {
		t.Fatalf("QuickProfile=5 failed: %v", err)
	}
	if rep.State != StateDone {
		t.Fatalf("state = %s, want done", rep.State)
	}
	if sawRounds != 3 {
		t.Errorf("rounds = %d, want 3", sawRounds)
	}
	if len(e.probe.starts) != 0 {
		t.Errorf("QUIC phase must run live without starting isolated NfqwsProbe, starts=%v", e.probe.starts)
	}
	e.wantLock("5", "udp", 2)
	q := worker(t, rep, WorkerQUIC)
	if q.Profile != 5 || q.OK != 1 || q.Applied != "2" {
		t.Errorf("unexpected QUIC worker report: %+v", q)
	}
}

func TestCoordinator_QUICCancelRestoresPrevLock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var tried atomic.Int32
	e := newCoordEnv(t, fullScenario, func(c *Config) {
		c.MaxStrategy = func(p int) int { return 4 }
		c.QUICProbe = func(_ context.Context, strategy int, _ int) netcheck.TargetResult {
			tried.Add(1)
			if strategy == 2 {
				cancel()
			}
			return netcheck.TargetResult{Verdict: netcheck.VerdictFail, Text: "fail"}
		}
	})
	mustSet(t, e.st, "5", "udp", 7)

	rep, err := e.c.Run(ctx, Params{QuickProfile: 5})
	if err != nil {
		t.Fatalf("cancelled run err = %v", err)
	}
	if rep.State != StateCancelled {
		t.Fatalf("state = %s, want cancelled", rep.State)
	}
	if tried.Load() < 2 {
		t.Fatalf("expected at least 2 tries before cancel, got %d", tried.Load())
	}
	// Cancel handler must restore previous P5 lock via defer.
	e.wantLock("5", "udp", 7)
}

func TestCoordinator_QUICNoWinnerRestoresAuto(t *testing.T) {
	e := newCoordEnv(t, fullScenario, func(c *Config) {
		c.MaxStrategy = func(p int) int { return 2 }
		c.QUICProbe = func(context.Context, int, int) netcheck.TargetResult {
			return netcheck.TargetResult{Verdict: netcheck.VerdictWarn, Text: "partial"}
		}
	})
	rep, err := e.c.Run(context.Background(), Params{QuickProfile: 5})
	if err != nil {
		t.Fatalf("run err = %v", err)
	}
	if rep.State != StateDone {
		t.Fatalf("state = %s", rep.State)
	}
	// Previous lock was unset (auto), warn must not stay locked.
	if got := e.lock("5", "udp"); got != orchestra.StrategyAuto {
		t.Errorf("expected auto lock restored when no green winner, got %v", got)
	}
}

func TestCoordinator_IncludeQUIC_FullSweep(t *testing.T) {
	e := newCoordEnv(t, fullScenario, func(c *Config) {
		c.MaxStrategy = tinyMax
		c.QUICProbe = func(context.Context, int, int) netcheck.TargetResult {
			return netcheck.TargetResult{Verdict: netcheck.VerdictOK, Text: "ok"}
		}
	})
	p := fastParams()
	p.IncludeQUIC = true
	rep, err := e.c.Run(context.Background(), p)
	if err != nil {
		t.Fatalf("IncludeQUIC run failed: %v", err)
	}
	if len(rep.Workers) != 5 {
		t.Fatalf("expected 5 workers (yt, gv, ds, rkn, quic), got %d", len(rep.Workers))
	}
	e.wantLock("5", "udp", 1)
}

package sweep

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AloofLibra/zator/internal/netcheck"
)

func autoEnv(t *testing.T, rec *sleepRec, decide func(key string, strategy int) netcheck.TargetResult) (*Env, *scripted) {
	t.Helper()
	st := newStore(t)
	chk := newScripted(st, map[string]string{"u1": "1"}, decide)
	return &Env{Locks: st, Checker: chk, Sleep: rec.sleep}, chk
}

func TestAutoSweep_PicksFastestGreen(t *testing.T) {
	rec := &sleepRec{}
	times := map[int]string{1: "", 2: "2.0", 3: "0.5", 4: "1.0"}
	env, _ := autoEnv(t, rec, func(_ string, s int) netcheck.TargetResult {
		if times[s] == "" {
			return resFail()
		}
		return resGood(times[s])
	})
	var tried []int
	var rows []ProgressRow
	res, err := env.AutoSweepProfile(context.Background(), ProfileSweepConfig{
		Profile: 1, Label: "YT", URL: "u1", Max: 4,
		OnTry: func(s int) { tried = append(tried, s) },
		OnRow: func(r ProgressRow) { rows = append(rows, r) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Best != 3 || res.BestKind != BestKindBest {
		t.Errorf("best = %d (%s), want 3", res.Best, res.BestKind)
	}
	if !reflect.DeepEqual(res.Greens, []int{2, 3, 4}) || !reflect.DeepEqual(res.Fulls, []int{2, 3, 4}) {
		t.Errorf("greens = %v fulls = %v", res.Greens, res.Fulls)
	}
	if res.NOK != 3 || res.NFail != 1 || res.NWarn != 0 || res.Interrupted {
		t.Errorf("counters: %+v", res)
	}
	if !reflect.DeepEqual(tried, []int{1, 2, 3, 4}) || len(rows) != 4 || len(res.Rows) != 4 {
		t.Errorf("tried = %v rows = %d", tried, len(rows))
	}
	if rows[0].Token != TokenFail || rows[2].Token != TokenOK || rows[2].Target != "1" || rows[2].Strategy != 3 {
		t.Errorf("rows = %+v", rows)
	}
}

func TestAutoSweep_FirstGreenWithoutDownload(t *testing.T) {
	env, _ := autoEnv(t, &sleepRec{}, func(_ string, s int) netcheck.TargetResult {
		if s < 2 {
			return resFail()
		}
		r := resGood("1")
		r.Download = nil
		return r
	})
	res, err := env.AutoSweepProfile(context.Background(), ProfileSweepConfig{Profile: 1, URL: "u1", Max: 3})
	if err != nil {
		t.Fatal(err)
	}
	if res.Best != 2 || res.BestKind != BestKindBest || !strings.Contains(res.BestShort, "без докачки") {
		t.Errorf("best = %d %s %q", res.Best, res.BestKind, res.BestShort)
	}
}

func TestAutoSweep_WarnFallback(t *testing.T) {
	env, _ := autoEnv(t, &sleepRec{}, func(_ string, s int) netcheck.TargetResult {
		if s == 2 {
			return resOnly12()
		}
		return resFail()
	})
	res, err := env.AutoSweepProfile(context.Background(), ProfileSweepConfig{Profile: 1, URL: "u1", Max: 3, TLSPref: netcheck.PrefBoth})
	if err != nil {
		t.Fatal(err)
	}
	if res.Best != 2 || res.BestKind != BestKindWarn || !reflect.DeepEqual(res.Warns, []int{2}) || res.NWarn != 1 {
		t.Errorf("warn fallback: %+v", res)
	}
}

func TestAutoSweep_NoWinner(t *testing.T) {
	env, _ := autoEnv(t, &sleepRec{}, func(string, int) netcheck.TargetResult { return resFail() })
	res, err := env.AutoSweepProfile(context.Background(), ProfileSweepConfig{Profile: 1, URL: "u1", Max: 3})
	if err != nil {
		t.Fatal(err)
	}
	if res.Best != 0 || res.BestKind != "" || res.NFail != 3 || res.Interrupted {
		t.Errorf("no winner: %+v", res)
	}
}

func TestAutoSweep_TLSPrefBothDemotesSingleVersion(t *testing.T) {
	env, _ := autoEnv(t, &sleepRec{}, func(string, int) netcheck.TargetResult { return resOnly12() })
	any, _ := env.AutoSweepProfile(context.Background(), ProfileSweepConfig{Profile: 1, URL: "u1", Max: 1})
	both, _ := env.AutoSweepProfile(context.Background(), ProfileSweepConfig{Profile: 1, URL: "u1", Max: 1, TLSPref: netcheck.PrefBoth})
	if any.NOK != 1 || both.NOK != 0 || both.NWarn != 1 {
		t.Errorf("any=%+v both=%+v", any, both)
	}
	if len(any.Fulls) != 0 {
		t.Errorf("only TLS1.2 is not a full green: %v", any.Fulls)
	}
}

func TestAutoSweep_MarkUnknown(t *testing.T) {
	env, _ := autoEnv(t, &sleepRec{}, func(_ string, s int) netcheck.TargetResult {
		if s == 1 {
			return resFail()
		}
		return resGood("1")
	})
	res, err := env.AutoSweepProfile(context.Background(), ProfileSweepConfig{Profile: 1, URL: "u1", Max: 3, MarkUnknown: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.NUnknown != 2 || res.NFail != 1 || res.NOK != 0 || res.Best != 0 {
		t.Errorf("unknown run must not produce a winner: %+v", res)
	}
}

func TestAutoSweep_PausesAndGreenPause(t *testing.T) {
	rec := &sleepRec{}
	env, _ := autoEnv(t, rec, func(_ string, s int) netcheck.TargetResult {
		switch s {
		case 1:
			return resFail()
		case 2:
			return resGood("1")
		default:
			return resOnly12() // жёлтая при both, паузу не удлиняет
		}
	})
	env.Settle = 2 * time.Second
	var pacing []time.Duration
	_, err := env.AutoSweepProfile(context.Background(), ProfileSweepConfig{
		Profile: 1, URL: "u1", Max: 4, TLSPref: netcheck.PrefBoth,
		Pause: 5 * time.Second, GreenPause: 9 * time.Second,
		OnPacing: func(_ int, d time.Duration) { pacing = append(pacing, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	// Три паузы (после стратегий 1..3); после последней не спим. Settle входит в Pause.
	want := []time.Duration{3 * time.Second, 9 * time.Second, 3 * time.Second}
	if !reflect.DeepEqual(pacing, want) {
		t.Errorf("pacing = %v, want %v", pacing, want)
	}
	// Всего sleep: 4 settle + 3 пауз.
	var sleeps int
	for _, d := range rec.list() {
		if d == 2*time.Second {
			sleeps++
		}
	}
	if sleeps != 4 {
		t.Errorf("settle sleeps = %d, want 4 (%v)", sleeps, rec.list())
	}
}

func TestAutoSweep_PauseNeverNegative(t *testing.T) {
	rec := &sleepRec{}
	env, _ := autoEnv(t, rec, func(string, int) netcheck.TargetResult { return resFail() })
	env.Settle = 5 * time.Second
	var pacing []time.Duration
	_, _ = env.AutoSweepProfile(context.Background(), ProfileSweepConfig{
		Profile: 1, URL: "u1", Max: 2, Pause: time.Second,
		OnPacing: func(_ int, d time.Duration) { pacing = append(pacing, d) },
	})
	if len(pacing) != 1 || pacing[0] != 0 {
		t.Errorf("pacing = %v", pacing)
	}
}

func TestAutoSweep_InterruptedByCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env, chk := autoEnv(t, &sleepRec{}, func(string, int) netcheck.TargetResult { return resGood("1") })
	chk.hook = func(call int, _ string, _ int, _ context.Context) {
		if call == 2 {
			cancel()
		}
	}
	res, err := env.AutoSweepProfile(ctx, ProfileSweepConfig{Profile: 1, URL: "u1", Max: 5})
	if err != nil {
		t.Fatalf("cancel is not an error: %v", err)
	}
	if !res.Interrupted {
		t.Error("must be interrupted")
	}
	if len(res.Rows) != 1 {
		t.Errorf("rows = %d, want 1 (cancelled probe discarded)", len(res.Rows))
	}
}

func TestAutoSweep_InterruptedOnLastStrategy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env, chk := autoEnv(t, &sleepRec{}, func(string, int) netcheck.TargetResult { return resGood("1") })
	chk.hook = func(call int, _ string, _ int, _ context.Context) {
		if call == 2 {
			cancel()
		}
	}
	res, _ := env.AutoSweepProfile(ctx, ProfileSweepConfig{Profile: 1, URL: "u1", Max: 2})
	if !res.Interrupted {
		t.Error("cancel on the last strategy must still mark the pass incomplete")
	}
}

func TestAutoSweep_CancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env, chk := autoEnv(t, &sleepRec{}, func(string, int) netcheck.TargetResult { return resGood("1") })
	res, err := env.AutoSweepProfile(ctx, ProfileSweepConfig{Profile: 1, URL: "u1", Max: 3})
	if err != nil || !res.Interrupted || len(chk.callLog()) != 0 {
		t.Errorf("res=%+v err=%v calls=%v", res, err, chk.callLog())
	}
}

func TestAutoSweep_LockErrorStops(t *testing.T) {
	st := newStore(t)
	chk := newScripted(st, map[string]string{"u1": "1"}, func(string, int) netcheck.TargetResult { return resGood("1") })
	env := &Env{Locks: &failingLocker{Locker: st, okCalls: 2}, Checker: chk, Sleep: (&sleepRec{}).sleep}
	// Профиль 1: два протокола на стратегию — первая стратегия проходит, вторая падает.
	res, err := env.AutoSweepProfile(context.Background(), ProfileSweepConfig{Profile: 1, URL: "u1", Max: 3})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
	if len(res.Rows) != 1 || !res.Interrupted {
		t.Errorf("rows = %d interrupted = %v", len(res.Rows), res.Interrupted)
	}
}

func TestAutoSweep_Validation(t *testing.T) {
	env, _ := autoEnv(t, &sleepRec{}, func(string, int) netcheck.TargetResult { return resGood("1") })
	if _, err := env.AutoSweepProfile(context.Background(), ProfileSweepConfig{Profile: 1, URL: "u1", Max: 0}); err == nil || !strings.Contains(err.Error(), "стратег") {
		t.Errorf("Max=0: %v", err)
	}
	if _, err := (&Env{}).AutoSweepProfile(context.Background(), ProfileSweepConfig{Profile: 1, Max: 1}); err == nil {
		t.Error("empty env must fail")
	}
}

func TestProgressRow_TSV(t *testing.T) {
	r := ProgressRow{Epoch: 1700000000, Target: "1", Strategy: 7, Token: TokenOK, TLS12: "ok", TLS13: "ok", DLBytes: "65536", DLTime: "0.4", Short: "данные\tидут\n"}
	f := strings.Split(r.TSV(), "\t")
	if len(f) != 9 || f[0] != "1700000000" || f[2] != "7" || f[3] != "ok" || f[8] != "данные идут" {
		t.Errorf("TSV = %q (%d fields)", r.TSV(), len(f))
	}
	empty := ProgressRow{Epoch: 1, Strategy: 1, Token: TokenFail}.TSV()
	if got := strings.Count(empty, "\t"); got != 8 || strings.Contains(empty, "\t\t") {
		t.Errorf("empty fields must be dashes: %q", empty)
	}
}

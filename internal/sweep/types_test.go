package sweep

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AloofLibra/zator/internal/netcheck"
)

func TestState_Predicates(t *testing.T) {
	all := []State{StateIdle, StateStarting, StateRunning, StateApplying, StateDone, StateCancelled, StateInterrupted}
	for _, s := range all {
		if !s.Valid() {
			t.Errorf("%s must be valid", s)
		}
	}
	if State("").Valid() || State("bogus").Valid() {
		t.Error("unknown states must be invalid")
	}
	for _, s := range []State{StateStarting, StateRunning, StateApplying} {
		if !s.InProgress() || s.Terminal() {
			t.Errorf("%s: in-progress", s)
		}
	}
	for _, s := range []State{StateDone, StateCancelled, StateInterrupted} {
		if s.InProgress() || !s.Terminal() {
			t.Errorf("%s: terminal", s)
		}
	}
	if StateIdle.InProgress() || StateIdle.Terminal() {
		t.Error("idle is neither")
	}
}

func TestCanTransition(t *testing.T) {
	ok := [][2]State{
		{StateIdle, StateStarting},
		{StateStarting, StateRunning}, {StateStarting, StateCancelled}, {StateStarting, StateInterrupted},
		{StateRunning, StateApplying}, {StateRunning, StateCancelled}, {StateRunning, StateInterrupted},
		{StateApplying, StateDone}, {StateApplying, StateCancelled}, {StateApplying, StateInterrupted},
		{StateDone, StateStarting}, {StateCancelled, StateStarting}, {StateInterrupted, StateStarting},
		{StateRunning, StateRunning}, // обновление статуса без смены состояния
	}
	for _, p := range ok {
		if !CanTransition(p[0], p[1]) {
			t.Errorf("%s -> %s must be allowed", p[0], p[1])
		}
	}
	bad := [][2]State{
		{StateIdle, StateRunning}, {StateIdle, StateDone},
		{StateStarting, StateDone}, {StateStarting, StateApplying},
		{StateRunning, StateDone}, {StateRunning, StateStarting},
		{StateDone, StateRunning}, {StateDone, StateCancelled},
		{StateCancelled, StateDone}, {StateInterrupted, StateRunning},
		{State("x"), State("x")},
	}
	for _, p := range bad {
		if CanTransition(p[0], p[1]) {
			t.Errorf("%s -> %s must be rejected", p[0], p[1])
		}
	}
}

func TestRun_SetStateRejectsBadTransition(t *testing.T) {
	r := &run{state: StateIdle}
	err := r.setState(StateDone)
	if !errors.Is(err, ErrBadTransition) || r.state != StateIdle {
		t.Errorf("err = %v state = %s", err, r.state)
	}
}

func TestDefaultParams(t *testing.T) {
	p := DefaultParams()
	if p.Pause != 5*time.Second || p.DSPause != 15*time.Second || p.RKNPause != 60*time.Second ||
		p.PhasePause != 30*time.Second || p.Settle != 2*time.Second || p.RKNPar != 2 ||
		p.GreenPause != 5*time.Second || p.TLSPref != netcheck.PrefAny || p.Scope != "default" {
		t.Errorf("defaults = %+v", p)
	}
	if len(p.Domains) != 3 || p.Domains[0] != "xhamster.com" {
		t.Errorf("domains = %v", p.Domains)
	}
	// Копия: изменение не должно портить глобальный список.
	p.Domains[0] = "changed"
	if DefaultRKNDomains[0] != "xhamster.com" {
		t.Error("DefaultParams must copy DefaultRKNDomains")
	}
}

func TestParams_Normalize(t *testing.T) {
	p := Params{Pause: 10 * time.Second, DSPause: time.Second, RKNPause: -time.Second, Settle: -1, RKNPar: 0, TLSPref: "weird"}.Normalize()
	if p.DSPause != 10*time.Second {
		t.Errorf("DSPause must be >= Pause: %v", p.DSPause)
	}
	if p.RKNPause != 0 || p.Settle != 0 {
		t.Errorf("negatives zeroed: %+v", p)
	}
	if p.RKNPar != DefaultRKNPar || p.Scope != "default" {
		t.Errorf("RKNPar/Scope: %+v", p)
	}
	if p.TLSPref != netcheck.PrefAny {
		t.Errorf("TLSPref = %q", p.TLSPref)
	}
	if got := (Params{TLSPref: "both"}).Normalize().TLSPref; got != netcheck.PrefBoth {
		t.Errorf("both kept: %q", got)
	}
}

func TestEstimateTotal(t *testing.T) {
	p := Params{Pause: 5 * time.Second, DSPause: 15 * time.Second, RKNPause: 60 * time.Second, PhasePause: 30 * time.Second}
	got := EstimateTotal(10, 10, 5, 4, 3, p)
	want := 10*11*time.Second + 10*11*time.Second + 5*21*time.Second + 12*66*time.Second + 90*time.Second
	if got != want {
		t.Errorf("estimate = %v, want %v", got, want)
	}
}

func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), 0); err != nil {
		t.Errorf("zero sleep: %v", err)
	}
	if err := sleepCtx(context.Background(), 5*time.Millisecond); err != nil {
		t.Errorf("short sleep: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled sleep: %v", err)
	}
	if err := sleepCtx(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("zero sleep on cancelled ctx: %v", err)
	}
}

func TestNoopProbe(t *testing.T) {
	var p Probe = NoopProbe{}
	if err := p.Start(context.Background(), 1); err != nil || !p.Healthy(context.Background()) || p.Stop() != nil {
		t.Error("NoopProbe must always succeed")
	}
}

func TestFileHelpers(t *testing.T) {
	if !isPositiveInt("7") || isPositiveInt("0") || isPositiveInt("-1") || isPositiveInt("07") || isPositiveInt("a") || isPositiveInt("") {
		t.Error("isPositiveInt")
	}
	if atoi("12") != 12 || atoi(" 5\n") != 5 || atoi("-3") != 0 || atoi("x") != 0 {
		t.Error("atoi")
	}
	if got := joinInts([]int{1, 20, 3}); got != "1 20 3" {
		t.Errorf("joinInts = %q", got)
	}
	if got := intList("1 x 3  4"); len(got) != 3 || got[2] != 4 {
		t.Errorf("intList = %v", got)
	}
	if joinInts(nil) != "" || len(intList("")) != 0 {
		t.Error("empty lists")
	}
	if cleanField("") != "-" || cleanField("  a\tb\n") != "a b" {
		t.Errorf("cleanField: %q %q", cleanField(""), cleanField("  a\tb\n"))
	}
	if numOrEmpty(0) != "" || numOrEmpty(-1) != "" || numOrEmpty(4) != "4" {
		t.Error("numOrEmpty")
	}
}

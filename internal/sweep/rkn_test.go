package sweep

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/AloofLibra/zator/internal/netcheck"
)

func TestNormalizeTestURL(t *testing.T) {
	cases := []struct {
		in, url, domain string
		ok              bool
	}{
		{"example.com", "https://example.com/", "example.com", true},
		{"  Example.COM  ", "https://example.com/", "example.com", true},
		{"example.com.", "https://example.com/", "example.com", true},
		{"https://example.com/x?y=1", "https://example.com/x?y=1", "example.com", true},
		{"http://Example.com", "http://example.com/", "example.com", true},
		{"https://example.com:8443", "https://example.com:8443/", "example.com", true},
		{"example.com/path", "https://example.com/path", "example.com", true},
		{"1", "", "", false},
		{"bad", "", "", false},
		{"", "", "", false},
		{"ftp://example.com", "", "", false},
		{"http://user:pw@example.com", "", "", false},
		{"exa mple.com", "", "", false},
		{"1.2.3.4", "", "", false}, // нет букв
		{"-bad-.com", "", "", false},
		{"ex'ample.com", "", "", false},
	}
	for _, tc := range cases {
		u, d, ok := NormalizeTestURL(tc.in)
		if ok != tc.ok || u != tc.url || d != tc.domain {
			t.Errorf("NormalizeTestURL(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.in, u, d, ok, tc.url, tc.domain, tc.ok)
		}
	}
}

func TestSanitizeDomains(t *testing.T) {
	got, dropped := SanitizeDomains([]string{"a.com b.org", "bad", "https://a.com/new", "", "C.net"})
	want := []DomainTarget{{"b.org", "https://b.org/"}, {"a.com", "https://a.com/new"}, {"c.net", "https://c.net/"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("targets = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(dropped, []string{"bad"}) {
		t.Errorf("dropped = %v", dropped)
	}
	if g, d := SanitizeDomains(nil); len(g) != 0 || len(d) != 0 {
		t.Errorf("nil input: %v %v", g, d)
	}
}

func TestCoverageRow_TSVRoundTrip(t *testing.T) {
	row := CoverageRow{Epoch: 100, Domain: "a.com", Strategy: 3, Token: TokenOK, DLSize: "65536", Speed: 12345}
	got := parseCoverage([][]string{splitTab(row.TSV()), {"short"}, {"1", "a.com", "x", "ok", "-", "0"}})
	if len(got) != 1 || got[0] != row {
		t.Errorf("round trip: %+v", got)
	}
	if line := (CoverageRow{Domain: "a.com", Strategy: 1, Token: TokenFail}).TSV(); line != "0\ta.com\t1\tfail\t-\t0" {
		t.Errorf("TSV = %q", line)
	}
}

func splitTab(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\t' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func TestRKNWinners(t *testing.T) {
	rows := []CoverageRow{
		{Domain: "a", Strategy: 5, Token: TokenOK, Speed: 100},
		{Domain: "b", Strategy: 5, Token: TokenOK, Speed: 100},
		{Domain: "a", Strategy: 2, Token: TokenOK, Speed: 900}, // быстрее, но покрывает один домен
		{Domain: "a", Strategy: 7, Token: TokenWarn, Speed: 10},
		{Domain: "b", Strategy: 7, Token: TokenWarn, Speed: 10},
		{Domain: "a", Strategy: 9, Token: TokenFail},
	}
	w := RKNWinners(2, rows)
	if w.OK != 5 || w.OKCover != 2 || w.Total != 2 || w.Warn != 7 || w.WarnCover != 2 {
		t.Errorf("winner = %+v", w)
	}

	// Равное покрытие — решает суммарная скорость, затем меньший номер.
	w = RKNWinners(1, []CoverageRow{
		{Domain: "a", Strategy: 4, Token: TokenOK, Speed: 50},
		{Domain: "a", Strategy: 3, Token: TokenOK, Speed: 50},
		{Domain: "a", Strategy: 6, Token: TokenOK, Speed: 80},
	})
	if w.OK != 6 {
		t.Errorf("speed tiebreak: %+v", w)
	}
	w = RKNWinners(1, []CoverageRow{
		{Domain: "a", Strategy: 4, Token: TokenOK, Speed: 50},
		{Domain: "a", Strategy: 3, Token: TokenOK, Speed: 50},
	})
	if w.OK != 3 {
		t.Errorf("number tiebreak: %+v", w)
	}

	if w := RKNWinners(3, []CoverageRow{{Domain: "a", Strategy: 1, Token: TokenFail}}); w.OK != 0 || w.Warn != 0 {
		t.Errorf("only red: %+v", w)
	}
}

func TestDomainWinners(t *testing.T) {
	rows := []CoverageRow{
		{Domain: "a", Strategy: 1, Token: TokenFail},
		{Domain: "b", Strategy: 1, Token: TokenWarn, Speed: 5},
		{Domain: "a", Strategy: 2, Token: TokenWarn, Speed: 10},
		{Domain: "a", Strategy: 3, Token: TokenOK, Speed: 1},
		{Domain: "a", Strategy: 4, Token: TokenOK, Speed: 7},
		{Domain: "c", Strategy: 9, Token: TokenFail},
		{Domain: "c", Strategy: 4, Token: TokenFail},
		{Domain: "b", Strategy: 2, Token: TokenWarn, Speed: 5},
	}
	got := DomainWinners(rows)
	want := []DomainWinner{{"a", 4, 0}, {"b", 1, 1}, {"c", 4, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("winners = %+v, want %+v", got, want)
	}
	if got[0].Verdict() != TokenOK || got[1].Verdict() != TokenWarn || got[2].Verdict() != TokenFail {
		t.Errorf("verdict names: %v", got)
	}
}

func TestOrderStrategies(t *testing.T) {
	got := OrderStrategies(6, map[int]string{1: TokenFail, 2: TokenOK, 3: TokenWarn, 4: TokenFail, 6: TokenOK})
	// ok: 2,6; warn/неизвестные: 3,5; красные: 1,4.
	if want := []int{2, 6, 3, 5, 1, 4}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if got := OrderStrategies(3, nil); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Errorf("nil yt: %v", got)
	}
	if got := OrderStrategies(0, nil); len(got) != 0 {
		t.Errorf("max 0: %v", got)
	}
}

func rknEnv(t *testing.T, rec *sleepRec, decide func(domain string, s int) netcheck.TargetResult) (*Env, *scripted) {
	t.Helper()
	st := newStore(t)
	chk := newScripted(st, map[string]string{"https://a.com/": "a.com", "https://b.org/": "b.org"}, decide)
	return &Env{Locks: st, Checker: chk, Sleep: rec.sleep}, chk
}

var rknTargets = []DomainTarget{{"a.com", "https://a.com/"}, {"b.org", "https://b.org/"}}

func TestSweepRKN_SequentialDomainsAndWinner(t *testing.T) {
	rec := &sleepRec{}
	env, chk := rknEnv(t, rec, func(d string, s int) netcheck.TargetResult {
		switch {
		case s == 2:
			return resGood("1")
		case s == 3 && d == "a.com":
			return resGood("0.5")
		}
		return resFail()
	})
	var tries []string
	res, err := env.SweepRKN(context.Background(), RKNSweepConfig{
		Targets: rknTargets, Max: 3, Pause: 60 * time.Second, GreenPause: 10 * time.Second,
		OnTry: func(d string, s int) { tries = append(tries, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Winner.OK != 2 || res.Winner.OKCover != 2 || res.Winner.Total != 2 {
		t.Errorf("winner = %+v", res.Winner)
	}
	if res.Reference != "a.com" || res.Interrupted || len(res.Coverage) != 6 || len(res.Rows) != 6 {
		t.Errorf("res: ref=%s intr=%v cov=%d", res.Reference, res.Interrupted, len(res.Coverage))
	}
	// Домены идут по одному: сначала все стратегии a.com, затем b.org.
	wantTries := []string{"a.com", "a.com", "a.com", "b.org", "b.org", "b.org"}
	if !reflect.DeepEqual(tries, wantTries) {
		t.Errorf("tries = %v", tries)
	}
	if got := chk.callLog(); got[0] != "a.com:1" || got[3] != "b.org:1" {
		t.Errorf("lock log = %v", got)
	}
	// Паузы: после последней проверки пауза не нужна.
	var pauses int
	for _, d := range rec.list() {
		if d == 60*time.Second || d == 10*time.Second {
			pauses++
		}
	}
	// 5 пауз между проверками (6-1), в том числе между доменами.
	if pauses != 5 {
		t.Errorf("pauses = %d (%v)", pauses, rec.list())
	}
}

func TestSweepRKN_CustomOrderAndPartial(t *testing.T) {
	env, chk := rknEnv(t, &sleepRec{}, func(string, int) netcheck.TargetResult { return resGood("1") })
	res, err := env.SweepRKN(context.Background(), RKNSweepConfig{
		Targets: rknTargets[:1], Max: 3, Order: []int{3, 1, 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := chk.callLog(); !reflect.DeepEqual(got, []string{"a.com:3", "a.com:1", "a.com:2"}) {
		t.Errorf("order = %v", got)
	}
	// Все стратегии зелёные с равной скоростью — побеждает меньший номер.
	if res.Winner.OK != 1 {
		t.Errorf("winner = %+v", res.Winner)
	}
}

func TestSweepRKN_Interrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env, chk := rknEnv(t, &sleepRec{}, func(string, int) netcheck.TargetResult { return resGood("1") })
	chk.hook = func(call int, _ string, _ int, _ context.Context) {
		if call == 2 {
			cancel()
		}
	}
	res, err := env.SweepRKN(ctx, RKNSweepConfig{Targets: rknTargets, Max: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Interrupted {
		t.Error("expected interrupted")
	}
	if len(res.Coverage) != 1 {
		t.Errorf("coverage = %d", len(res.Coverage))
	}
}

func TestSweepRKN_Validation(t *testing.T) {
	env, _ := rknEnv(t, &sleepRec{}, func(string, int) netcheck.TargetResult { return resGood("1") })
	if _, err := env.SweepRKN(context.Background(), RKNSweepConfig{Max: 3}); err != ErrNoDomains {
		t.Errorf("no domains: %v", err)
	}
	if _, err := env.SweepRKN(context.Background(), RKNSweepConfig{Targets: rknTargets}); err == nil {
		t.Error("Max=0 must fail")
	}
	if _, err := (&Env{}).SweepRKN(context.Background(), RKNSweepConfig{Targets: rknTargets, Max: 1}); err == nil {
		t.Error("empty env must fail")
	}
}

func TestSweepRKN_UnknownBecomesFail(t *testing.T) {
	// В РКН «unknown» не бывает: всё кроме ok/warn — красное.
	env, _ := rknEnv(t, &sleepRec{}, func(string, int) netcheck.TargetResult { return resCut() })
	res, err := env.SweepRKN(context.Background(), RKNSweepConfig{Targets: rknTargets[:1], Max: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Coverage {
		if c.Token != TokenFail {
			t.Errorf("token = %s", c.Token)
		}
	}
	if res.Winner.OK != 0 {
		t.Errorf("winner = %+v", res.Winner)
	}
}

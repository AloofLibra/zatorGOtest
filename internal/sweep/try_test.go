package sweep

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AloofLibra/zator/internal/netcheck"
	"github.com/AloofLibra/zator/internal/orchestra"
)

func newEnv(t *testing.T, decide func(key string, strategy int) netcheck.TargetResult) (*Env, *orchestra.Store, *scripted) {
	t.Helper()
	st := newStore(t)
	chk := newScripted(st, map[string]string{"u1": "1", "u2": "2", "u4": "4", "d.example.com": "d.example.com"}, decide)
	return &Env{Locks: st, Checker: chk, Sleep: (&sleepRec{}).sleep}, st, chk
}

func TestTry_SetsLocksAndBuildsVerdict(t *testing.T) {
	env, st, _ := newEnv(t, func(string, int) netcheck.TargetResult { return resGood("0.5") })
	res, err := env.Try(context.Background(), TryRequest{Profile: 1, Strategy: 5, Label: "YouTube", URL: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, st, "1", "tls"); got != 5 {
		t.Errorf("tls lock = %v, want 5", got)
	}
	if got := mustGet(t, st, "1", "http"); got != 5 {
		t.Errorf("http lock = %v, want 5", got)
	}
	if res.Verdict != TokenOK || !res.Q12 || !res.Q13 || !res.Full {
		t.Errorf("unexpected result: %+v", res)
	}
	if res.Strategy != 5 || res.DLBytes != "65536" || res.DLTime != "0.5" {
		t.Errorf("download fields: %+v", res)
	}
	if res.Speed < 131000 || res.Speed > 131100 {
		t.Errorf("speed = %v, want ~131072", res.Speed)
	}
	if res.Target.Label != "YouTube" || res.Target.Target != "u1" {
		t.Errorf("target = %+v", res.Target)
	}
}

func TestTry_ExplicitProtosAndProfileDefaults(t *testing.T) {
	env, st, _ := newEnv(t, func(string, int) netcheck.TargetResult { return resGood("1.0") })
	if _, err := env.Try(context.Background(), TryRequest{Profile: 2, Strategy: 3, URL: "u2"}); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, st, "2", "tls"); got != 3 {
		t.Errorf("profile 2 tls lock = %v", got)
	}
	// Явный список протоколов ограничивает запись локов.
	if _, err := env.Try(context.Background(), TryRequest{Profile: 1, Protos: []string{"tls"}, Strategy: 7, URL: "u1"}); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, st, "1", "tls"); got != 7 {
		t.Errorf("tls = %v", got)
	}
	if got := mustGet(t, st, "1", "http"); got.IsAuto() == false {
		t.Errorf("http lock must stay unset, got %v", got)
	}
}

func TestTry_DomainLockIsTLSOnly(t *testing.T) {
	env, st, _ := newEnv(t, func(string, int) netcheck.TargetResult { return resGood("1.0") })
	if _, err := env.Try(context.Background(), TryRequest{Domain: "d.example.com", Strategy: 4, URL: "d.example.com"}); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, st, "d.example.com", "tls"); got != 4 {
		t.Errorf("domain lock = %v", got)
	}
	_, err := env.Try(context.Background(), TryRequest{Domain: "d.example.com", Protos: []string{"http"}, Strategy: 4, URL: "d.example.com"})
	if err == nil || !errors.Is(err, orchestra.ErrInvalid) {
		t.Errorf("http for domain: err = %v, want ErrInvalid", err)
	}
}

func TestTry_Verdicts(t *testing.T) {
	cases := []struct {
		name string
		res  netcheck.TargetResult
		pref netcheck.TLSPref
		mark bool
		want string
	}{
		{"good", resGood("1.0"), "", false, TokenOK},
		{"only12-any", resOnly12(), netcheck.PrefAny, false, TokenOK},
		{"only12-both", resOnly12(), netcheck.PrefBoth, false, TokenWarn},
		{"only12-pref13", resOnly12(), netcheck.Pref13, false, TokenWarn},
		{"only12-pref12", resOnly12(), netcheck.Pref12, false, TokenOK},
		{"fail", resFail(), "", false, TokenFail},
		{"cut", resCut(), "", false, TokenFail},
		{"unknown-good", resGood("1.0"), "", true, TokenUnknown},
		{"unknown-keeps-fail", resFail(), "", true, TokenFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, _, _ := newEnv(t, func(string, int) netcheck.TargetResult { return tc.res })
			res, err := env.Try(context.Background(), TryRequest{Profile: 1, Strategy: 1, URL: "u1", TLSPref: tc.pref, MarkUnknown: tc.mark})
			if err != nil {
				t.Fatal(err)
			}
			if res.Verdict != tc.want {
				t.Errorf("verdict = %q (%s), want %q", res.Verdict, res.Short, tc.want)
			}
			if tc.want == TokenUnknown && !strings.Contains(res.Short, "TLS/CDN") {
				t.Errorf("unknown short must mention TLS/CDN: %q", res.Short)
			}
		})
	}
}

func TestTry_NoDownloadFields(t *testing.T) {
	r := resGood("1.0")
	r.Download = nil
	env, _, _ := newEnv(t, func(string, int) netcheck.TargetResult { return r })
	res, err := env.Try(context.Background(), TryRequest{Profile: 1, Strategy: 1, URL: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.DLBytes != "-" || res.DLTime != "-" || res.Speed != 0 {
		t.Errorf("no download: %+v", res)
	}
}

func TestTry_Validation(t *testing.T) {
	env, _, _ := newEnv(t, func(string, int) netcheck.TargetResult { return resGood("1") })
	if _, err := env.Try(context.Background(), TryRequest{Profile: 1, Strategy: 0, URL: "u1"}); !errors.Is(err, orchestra.ErrInvalid) {
		t.Errorf("strategy 0: %v", err)
	}
	if _, err := env.Try(context.Background(), TryRequest{Profile: 99, Strategy: 1, URL: "u1"}); !errors.Is(err, ErrConfig) {
		t.Errorf("unknown profile (no protos): %v", err)
	}
	if _, err := (&Env{Checker: env.Checker}).Try(context.Background(), TryRequest{Profile: 1, Strategy: 1}); !errors.Is(err, ErrConfig) {
		t.Errorf("nil Locks: %v", err)
	}
	if _, err := (&Env{Locks: env.Locks}).Try(context.Background(), TryRequest{Profile: 1, Strategy: 1}); !errors.Is(err, ErrConfig) {
		t.Errorf("nil Checker: %v", err)
	}
}

func TestTry_SettleAndCancel(t *testing.T) {
	rec := &sleepRec{}
	env, _, chk := newEnv(t, func(string, int) netcheck.TargetResult { return resGood("1") })
	env.Sleep, env.Settle = rec.sleep, 1500*time.Millisecond
	if _, err := env.Try(context.Background(), TryRequest{Profile: 2, Strategy: 1, URL: "u2"}); err != nil {
		t.Fatal(err)
	}
	if l := rec.list(); len(l) != 1 || l[0] != 1500*time.Millisecond {
		t.Errorf("settle sleeps = %v", l)
	}

	// Контекст уже отменён — проба не стартует.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := chk.callLog()
	if _, err := env.Try(ctx, TryRequest{Profile: 2, Strategy: 2, URL: "u2"}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx: %v", err)
	}
	if len(chk.callLog()) != len(calls) {
		t.Error("checker must not run after cancel")
	}

	// Отмена во время проверки — результат отбрасывается.
	ctx2, cancel2 := context.WithCancel(context.Background())
	chk.hook = func(int, string, int, context.Context) { cancel2() }
	if _, err := env.Try(ctx2, TryRequest{Profile: 2, Strategy: 3, URL: "u2"}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel during check: %v", err)
	}
}

func TestTry_LockWriteError(t *testing.T) {
	env, st, _ := newEnv(t, func(string, int) netcheck.TargetResult { return resGood("1") })
	env.Locks = &failingLocker{Locker: st, okCalls: 0}
	_, err := env.Try(context.Background(), TryRequest{Profile: 1, Strategy: 1, URL: "u1"})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("err = %v", err)
	}
}

func TestTry_WithRealChecker(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer srv.Close()

	opts := netcheck.DefaultOptions()
	opts.ConnectTimeout, opts.MaxTime = 2*time.Second, 5*time.Second
	opts.WaitBoth = true // иначе вторая версия TLS прерывается после первого ответа
	st := newStore(t)
	env := &Env{Locks: st, Checker: netcheck.NewChecker(opts), Sleep: (&sleepRec{}).sleep}
	res, err := env.Try(context.Background(), TryRequest{Profile: 2, Strategy: 2, Label: "local", URL: srv.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != TokenOK || !res.Full {
		t.Errorf("real checker: %+v / %s", res, res.Short)
	}
	if mustGet(t, st, "2", "tls") != 2 {
		t.Error("lock not written")
	}
}

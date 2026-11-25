package netcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConvertClusterCodename(t *testing.T) {
	cases := map[string]string{
		"uzpkfa50": "rr1---sn-01234567.googlevideo.com",
		"-":        "rr1---sn--.googlevideo.com",
		"u-z":      "rr1---sn-0-1.googlevideo.com",
		"":         "rr1---sn-.googlevideo.com",
	}
	for in, want := range cases {
		if got := ConvertClusterCodename(in); got != want {
			t.Errorf("ConvertClusterCodename(%q) = %q, want %q", in, got, want)
		}
	}
	// Позиции карты: v→8 q→9 l→a g→b b→c 6→d 1→e w→f r→g m→h h→i c→j 7→k 2→l x→m s→n n→o i→p d→q 8→r 3→s y→t t→u o→v j→w e→x 9→y 4→z
	if got := ConvertClusterCodename("vqlgb61wrmhc72xsnid83ytoje94"); got != "rr1---sn-89abcdefghijklmnopqrstuvwxyz.googlevideo.com" {
		t.Errorf("полная карта: %q", got)
	}
}

func TestParseClusterCodename(t *testing.T) {
	cases := []struct{ in, want string }{
		{"192.0.2.1 => uzpkfa50 (cluster)", "uzpkfa50"},
		{"junk\nmap => abc-def:7\n", "abc-def"},
		{"a => b => c d", "c"},
		{"no arrow here", ""},
		{"x => \n", ""},
		{"x => bad/host.evil", ""},
	}
	for _, c := range cases {
		if got := parseClusterCodename(c.in); got != c.want {
			t.Errorf("parseClusterCodename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClusterDomain_FromRedirector(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/report_mapping" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte("10.0.0.1 => uzpkfa50 (x)\n"))
	}))
	defer srv.Close()
	c := NewChecker(DefaultOptions())
	c.RedirectorURL = srv.URL + "/report_mapping?di=no"
	if got := c.ClusterDomain(context.Background()); got != "rr1---sn-01234567.googlevideo.com" {
		t.Errorf("ClusterDomain = %q", got)
	}
	if hits != 1 {
		t.Errorf("успешный ответ не должен повторяться, запросов: %d", hits)
	}
}

func TestClusterDomain_RetryThenFallback(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte("nothing useful"))
	}))
	defer srv.Close()
	c := NewChecker(DefaultOptions())
	c.RedirectorURL = srv.URL
	if got := c.ClusterDomain(context.Background()); got != FallbackClusterDomain {
		t.Errorf("fallback = %q", got)
	}
	if hits != 2 {
		t.Errorf("ожидали 2 попытки, было %d", hits)
	}
}

func TestClusterDomain_Unreachable(t *testing.T) {
	c := NewChecker(DefaultOptions())
	c.RedirectorURL = "http://127.0.0.1:1/"
	if got := c.ClusterDomain(context.Background()); got != FallbackClusterDomain {
		t.Errorf("fallback = %q", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := c.ClusterDomain(ctx); got != FallbackClusterDomain {
		t.Errorf("canceled = %q", got)
	}
}

func clusterServer(t *testing.T) *Checker {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("=> uzpkfa50"))
	}))
	t.Cleanup(srv.Close)
	c := NewChecker(DefaultOptions())
	c.RedirectorURL = srv.URL
	return c
}

func TestProfileTargets(t *testing.T) {
	c := clusterServer(t)
	ctx := context.Background()
	want := map[int]Target{
		1: {LabelYouTube, "https://www.youtube.com/"},
		2: {LabelGooglevideo, "https://rr1---sn-01234567.googlevideo.com"},
		3: {LabelBlockedSites, "https://meduza.io"},
		4: {LabelDiscord, "https://discord.com/"},
	}
	for p, w := range want {
		got, ok := c.ProfileTarget(ctx, p)
		if !ok || got != w {
			t.Errorf("ProfileTarget(%d) = %+v,%v, want %+v", p, got, ok, w)
		}
	}
	for _, p := range []int{0, 5, 6, 7, 8, 9, 10, 11} {
		if _, ok := c.ProfileTarget(ctx, p); ok {
			t.Errorf("ProfileTarget(%d) не должен определяться", p)
		}
	}
}

func TestDefaultTargets(t *testing.T) {
	c := clusterServer(t)
	ts := c.DefaultTargets(context.Background())
	labels := []string{LabelYouTube, LabelGooglevideo, LabelBlockedSites, LabelInstagram}
	if len(ts) != 4 {
		t.Fatalf("целей: %d", len(ts))
	}
	for i, l := range labels {
		if ts[i].Label != l {
			t.Errorf("цель %d = %q, want %q", i, ts[i].Label, l)
		}
	}
	if !strings.HasPrefix(ts[1].URL, "https://rr1---sn-") {
		t.Errorf("googlevideo URL = %q", ts[1].URL)
	}
}

func TestCheckProfile_NoTLSProfiles(t *testing.T) {
	c := NewChecker(testOpts())
	for _, p := range []int{6} {
		r := c.CheckProfile(context.Background(), p)
		if len(r.Results) != 0 || r.Results == nil || r.Message != MessageUDPProfile {
			t.Errorf("profile %d: %+v", p, r)
		}
	}
	for _, p := range []int{7, 8, 9, 0, 42} {
		r := c.CheckProfile(context.Background(), p)
		if r.Results == nil || len(r.Results) != 0 || r.Message != MessageNoTLSProfile {
			t.Errorf("profile %d: %+v", p, r)
		}
	}
}

func TestCheckProfile_DNSProfile(t *testing.T) {
	c := NewChecker(testOpts())
	c.DNS = DNSSeriesConfig{
		Interval: -1,
		Lookup: func(ctx context.Context, domain, server string) ([]string, error) {
			return []string{"204.8.99.144"}, nil
		},
	}
	r := c.CheckProfile(context.Background(), 10)
	if len(r.Results) != 1 {
		t.Fatalf("results: %+v", r)
	}
	res := r.Results[0]
	if res.Label != LabelDNS || res.Verdict != "ok" || res.Target != "nslookup deb.torproject.org @ 8.8.8.8" {
		t.Errorf("dns result: %+v", res)
	}
	if !strings.Contains(res.Text, "3 из 3") {
		t.Errorf("text = %q", res.Text)
	}
}

func TestCheckGooglevideoAndYouTube_Helpers(t *testing.T) {
	// Серверный отклик — на 127.0.0.1; проверяем только что helpers используют CheckTarget.
	srv := newTLSServer(t, http.NotFoundHandler(), tlsOpts{})
	c := NewChecker(testOpts())
	res := c.CheckTarget(context.Background(), LabelGooglevideo, srv.URL)
	if res.Verdict != VerdictOK {
		t.Errorf("404 на корне googlevideo — норма: %+v", res)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := c.CheckYouTube(ctx); r.Label != LabelYouTube || r.Verdict != VerdictFail {
		t.Errorf("YouTube при отменённом контексте: %+v", r)
	}
	if r := c.CheckGooglevideo(ctx); r.Label != LabelGooglevideo || !strings.Contains(r.Target, FallbackClusterDomain) {
		t.Errorf("Googlevideo при отменённом контексте: %+v", r)
	}
}

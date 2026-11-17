package telemetry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AloofLibra/zator/internal/orchestra"
	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/provider"
)

func newClient(t *testing.T) (*Client, paths.Paths) {
	t.Helper()
	p := paths.New(t.TempDir(), t.TempDir())
	c := New(p)
	c.Token = "test-token"
	c.OS = func() string { return "openwrt" }
	c.WebUI = func() bool { return true }
	return c, p
}

func TestNewUsesBashRecommendationsSourceAndAllowsOverride(t *testing.T) {
	t.Setenv(EnvRecsURL, "")
	if got := New(paths.Paths{}).RecsURL; got != LegacyRecsURL {
		t.Fatalf("default recommendations URL = %q, want %q", got, LegacyRecsURL)
	}
	t.Setenv(EnvRecsURL, "https://example.test/recs.tsv")
	if got := New(paths.Paths{}).RecsURL; got != "https://example.test/recs.tsv" {
		t.Fatalf("override recommendations URL = %q", got)
	}
}

func setProvider(t *testing.T, p paths.Paths, name string) {
	t.Helper()
	d := provider.New(p)
	if _, err := d.Set(name, ""); err != nil {
		t.Fatal(err)
	}
}

// ---- согласие и UUID ----

func TestSettingsRoundTrip(t *testing.T) {
	s := ParseSettings("tel_enabled=1\ntel_uuid=abcd1234\ntel_channel_id=chan\n")
	if !s.Decided || !s.Enabled || s.UUID != "abcd1234" || s.ChannelID != "chan" {
		t.Fatalf("parsed = %+v", s)
	}
	if got := s.String(); got != "tel_enabled=1\ntel_uuid=abcd1234\ntel_channel_id=chan\n" {
		t.Errorf("String = %q", got)
	}
	if got := (Settings{Decided: true, UUID: "x"}).String(); got != "tel_enabled=0\ntel_uuid=x\n" {
		t.Errorf("disabled String = %q", got)
	}
	if got := (Settings{}).String(); strings.Contains(got, "tel_enabled") {
		t.Errorf("undecided must not write tel_enabled: %q", got)
	}
	// Кавычки, CRLF, комментарии.
	s = ParseSettings("# c\r\ntel_enabled=\"0\"\r\ntel_uuid='deadbeef'\r\n")
	if !s.Decided || s.Enabled || s.UUID != "deadbeef" {
		t.Errorf("parsed quoted = %+v", s)
	}
	if s := ParseSettings("tel_enabled=\n"); s.Decided {
		t.Error("empty tel_enabled means undecided")
	}
}

func TestGenerateUUID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		u, err := GenerateUUID()
		if err != nil {
			t.Fatal(err)
		}
		if len(u) != 8 || !ValidUUID(u) {
			t.Fatalf("bad uuid %q", u)
		}
		seen[u] = true
	}
	if len(seen) < 45 {
		t.Errorf("uuids not random enough: %d unique of 50", len(seen))
	}
	for _, bad := range []string{"", "abc", "ABCDEF12", "zzzzzzzz", "123456789"} {
		if ValidUUID(bad) {
			t.Errorf("ValidUUID(%q) = true", bad)
		}
	}
}

func TestEnsureUUIDPersists(t *testing.T) {
	c, _ := newClient(t)
	u1, err := c.EnsureUUID()
	if err != nil || len(u1) != 8 {
		t.Fatalf("EnsureUUID = %q, %v", u1, err)
	}
	u2, err := c.EnsureUUID()
	if err != nil || u2 != u1 {
		t.Fatalf("second EnsureUUID = %q, %v; want %q", u2, err, u1)
	}
	s, _ := c.LoadSettings()
	if s.UUID != u1 {
		t.Errorf("persisted = %q", s.UUID)
	}
	// Повреждённый UUID пересоздаётся.
	os.WriteFile(c.ConfigPath(), []byte("tel_enabled=1\ntel_uuid=BAD\n"), 0o600)
	u3, err := c.EnsureUUID()
	if err != nil || !ValidUUID(u3) {
		t.Fatalf("regenerated = %q, %v", u3, err)
	}
	if s, _ := c.LoadSettings(); !s.Enabled || s.UUID != u3 {
		t.Errorf("settings after regen = %+v", s)
	}
}

func TestSetEnabled(t *testing.T) {
	c, _ := newClient(t)
	s, err := c.SetEnabled(true)
	if err != nil || !s.Decided || !s.Enabled || !ValidUUID(s.UUID) {
		t.Fatalf("SetEnabled(true) = %+v, %v", s, err)
	}
	uuid := s.UUID
	s, err = c.SetEnabled(false)
	if err != nil || !s.Decided || s.Enabled || s.UUID != uuid {
		t.Fatalf("SetEnabled(false) = %+v, %v", s, err)
	}
	// Файл лежит в ConfigDir под именем telemetry.config.
	if filepath.Base(c.ConfigPath()) != "telemetry.config" {
		t.Errorf("ConfigPath = %s", c.ConfigPath())
	}
	raw, _ := os.ReadFile(c.ConfigPath())
	if !strings.Contains(string(raw), "tel_enabled=0") {
		t.Errorf("config = %q", raw)
	}
}

func TestLoadSettingsMissingFile(t *testing.T) {
	c, _ := newClient(t)
	s, err := c.LoadSettings()
	if err != nil || s.Decided || s.Enabled || s.UUID != "" {
		t.Errorf("LoadSettings = %+v, %v", s, err)
	}
}

func TestNewReadsTokenFromEnv(t *testing.T) {
	t.Setenv(EnvStatsToken, "env-secret")
	t.Setenv(EnvStatsEndpoint, "http://example.invalid/t")
	c := New(paths.New(t.TempDir(), t.TempDir()))
	if c.Token != "env-secret" || c.Endpoint != "http://example.invalid/t" {
		t.Errorf("token/endpoint = %q / %q", c.Token, c.Endpoint)
	}
	t.Setenv(EnvStatsToken, "")
	if c := New(paths.New(t.TempDir(), t.TempDir())); c.Token != "" {
		t.Errorf("token must default to empty, got %q", c.Token)
	}
}

// ---- отправка статистики ----

type capture struct {
	mu    sync.Mutex
	forms []url.Values
	ctype string
	meth  string
}

func (cp *capture) handler(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		cp.mu.Lock()
		cp.forms = append(cp.forms, r.PostForm)
		cp.ctype = r.Header.Get("Content-Type")
		cp.meth = r.Method
		cp.mu.Unlock()
		w.WriteHeader(status)
	}
}

func (cp *capture) n() int {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return len(cp.forms)
}

func TestSendStatsPostsProviderAndLocks(t *testing.T) {
	c, p := newClient(t)
	cp := &capture{}
	srv := httptest.NewServer(cp.handler(http.StatusOK))
	defer srv.Close()
	c.Endpoint = srv.URL
	c.HTTP = srv.Client()

	setProvider(t, p, "MTS - Moscow")
	if _, err := c.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	st := orchestra.NewStore(p.StateOrchestraDir())
	for _, l := range []struct {
		profile, proto string
		n              orchestra.Strategy
	}{
		{"1", "tls", 7}, {"2", "tls", 3}, {"3", "tls", 11}, {"4", "tls", 2},
		{"6", "udp", 1}, {"8", "tls", 4}, {"9", "http", 6},
	} {
		if err := st.SetLock(orchestra.ScopeDefault, l.profile, l.proto, l.n); err != nil {
			t.Fatalf("SetLock %+v: %v", l, err)
		}
	}
	if err := st.SetOverride(orchestra.OverrideMode, "1", "clone"); err != nil {
		t.Fatal(err)
	}

	if err := c.SendStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cp.n() != 1 || cp.meth != http.MethodPost {
		t.Fatalf("requests = %d, method %s", cp.n(), cp.meth)
	}
	if !strings.HasPrefix(cp.ctype, "application/x-www-form-urlencoded") {
		t.Errorf("content-type = %q", cp.ctype)
	}
	f := cp.forms[0]
	s, _ := c.LoadSettings()
	want := map[string]string{
		"token": "test-token", "uuid": s.UUID, "isp": "MTS - Moscow",
		"tcp": "7", "gv": "3", "rkn": "11", "ds_tls": "2",
		"udp": "0", "voice_udp": "1", "games_udp": "0", "fb_tls": "4", "fb_http": "6",
		"mode_1": "clone", "mode_2": "classic", "mode_8": "classic",
		"os": "openwrt", "webui": "1",
	}
	for k, v := range want {
		if got := f.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	for _, k := range []string{"blob_global", "blob_1", "blob_2", "blob_3", "blob_4", "blob_8", "mode_3", "mode_4"} {
		if _, ok := f[k]; !ok {
			t.Errorf("field %s missing", k)
		}
	}
}

func TestSendStatsReadsBlobFromConfigAndOverrides(t *testing.T) {
	c, p := newClient(t)
	cp := &capture{}
	srv := httptest.NewServer(cp.handler(http.StatusOK))
	defer srv.Close()
	c.Endpoint, c.HTTP = srv.URL, srv.Client()

	if err := os.MkdirAll(p.ZapretRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "--lua-desync=fake:blob=fake_default_tls:strategy=1\n"
	os.WriteFile(p.ZapretConfig, []byte(cfg), 0o644)
	c.SetEnabled(true)
	st := orchestra.NewStore(p.StateOrchestraDir())
	if err := st.SetOverride(orchestra.OverrideBlob, "2", "maxru"); err != nil {
		t.Fatal(err)
	}
	if err := c.SendStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := cp.forms[0]
	if f.Get("blob_2") != "maxru" {
		t.Errorf("blob_2 = %q", f.Get("blob_2"))
	}
	if f.Get("blob_global") != f.Get("blob_1") {
		t.Errorf("blob_1 (%q) must inherit blob_global (%q)", f.Get("blob_1"), f.Get("blob_global"))
	}
}

func TestSendStatsGuards(t *testing.T) {
	c, _ := newClient(t)
	cp := &capture{}
	srv := httptest.NewServer(cp.handler(http.StatusOK))
	defer srv.Close()
	c.Endpoint, c.HTTP = srv.URL, srv.Client()

	// Нет согласия.
	if err := c.SendStats(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Errorf("undecided err = %v", err)
	}
	c.SetEnabled(false)
	if err := c.SendStats(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Errorf("disabled err = %v", err)
	}
	// Нет токена.
	c.SetEnabled(true)
	c.Token = ""
	if err := c.SendStats(context.Background()); !errors.Is(err, ErrNoToken) {
		t.Errorf("no token err = %v", err)
	}
	if cp.n() != 0 {
		t.Errorf("no request must be sent, got %d", cp.n())
	}
}

func TestSendStatsHTTPError(t *testing.T) {
	c, _ := newClient(t)
	cp := &capture{}
	srv := httptest.NewServer(cp.handler(http.StatusInternalServerError))
	defer srv.Close()
	c.Endpoint, c.HTTP = srv.URL, srv.Client()
	c.SetEnabled(true)
	err := c.SendStats(context.Background())
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v", err)
	}
}

func TestSendStatsGeneratesMissingUUID(t *testing.T) {
	c, _ := newClient(t)
	cp := &capture{}
	srv := httptest.NewServer(cp.handler(http.StatusOK))
	defer srv.Close()
	c.Endpoint, c.HTTP = srv.URL, srv.Client()
	os.MkdirAll(filepath.Dir(c.ConfigPath()), 0o755)
	os.WriteFile(c.ConfigPath(), []byte("tel_enabled=1\n"), 0o600)
	if err := c.SendStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ := c.LoadSettings()
	if !ValidUUID(s.UUID) || cp.forms[0].Get("uuid") != s.UUID {
		t.Errorf("uuid = %q, sent %q", s.UUID, cp.forms[0].Get("uuid"))
	}
}

func TestSendSnapshot(t *testing.T) {
	c, _ := newClient(t)
	cp := &capture{}
	srv := httptest.NewServer(cp.handler(http.StatusNoContent))
	defer srv.Close()
	c.Endpoint, c.HTTP = srv.URL, srv.Client()
	if err := c.SendSnapshot(context.Background(), Stats{}); !errors.Is(err, ErrNoUUID) {
		t.Errorf("err = %v", err)
	}
	if err := c.SendSnapshot(context.Background(), Stats{UUID: "aabbccdd", ISP: "X", TCP: 3, WebUI: false}); err != nil {
		t.Fatal(err)
	}
	f := cp.forms[0]
	if f.Get("tcp") != "3" || f.Get("webui") != "0" || f.Get("token") != "test-token" {
		t.Errorf("form = %v", f)
	}
	c.Token = ""
	if err := c.SendSnapshot(context.Background(), Stats{UUID: "aabbccdd"}); !errors.Is(err, ErrNoToken) {
		t.Errorf("err = %v", err)
	}
}

func TestSendStatsUnknownProvider(t *testing.T) {
	c, _ := newClient(t)
	cp := &capture{}
	srv := httptest.NewServer(cp.handler(http.StatusOK))
	defer srv.Close()
	c.Endpoint, c.HTTP = srv.URL, srv.Client()
	c.SetEnabled(true)
	if err := c.SendStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := cp.forms[0].Get("isp"); got != "Unknown" {
		t.Errorf("isp = %q, want Unknown", got)
	}
}

func TestISPForSend(t *testing.T) {
	if ISPForSend("  \r\n ") != "Unknown" {
		t.Error("blank")
	}
	if got := ISPForSend(strings.Repeat("я", 100)); len([]rune(got)) != MaxISPLen {
		t.Errorf("len = %d", len([]rune(got)))
	}
	if ISPForSend("MTS\r\n") != "MTS" {
		t.Error("newline strip")
	}
}

func TestInitFlow(t *testing.T) {
	c, _ := newClient(t)
	cp := &capture{}
	srv := httptest.NewServer(cp.handler(http.StatusOK))
	defer srv.Close()
	c.Endpoint, c.HTTP = srv.URL, srv.Client()
	ctx := context.Background()

	// Нет выбора и нет вопроса — ничего не происходит.
	s, err := c.Init(ctx, nil)
	if err != nil || s.Decided {
		t.Fatalf("Init(nil) = %+v, %v", s, err)
	}
	if cp.n() != 0 {
		t.Fatal("must not send without consent")
	}

	// Отказ: сохраняется, отправки нет.
	s, err = c.Init(ctx, func() bool { return false })
	if err != nil || !s.Decided || s.Enabled || cp.n() != 0 {
		t.Fatalf("Init(no) = %+v, %v, sent=%d", s, err, cp.n())
	}
	// Повторный вызов не спрашивает.
	asked := false
	c.Init(ctx, func() bool { asked = true; return true })
	if asked {
		t.Error("must not ask twice")
	}

	// Новая установка с согласием: UUID + первичная отправка + сохранение канала.
	c2, _ := newClient(t)
	c2.Endpoint, c2.HTTP = srv.URL, srv.Client()
	s, err = c2.Init(ctx, func() bool { return true })
	if err != nil || !s.Enabled || !ValidUUID(s.UUID) || s.ChannelID != DefaultChannelID {
		t.Fatalf("Init(yes) = %+v, %v", s, err)
	}
	if cp.n() != 1 {
		t.Fatalf("initial send count = %d", cp.n())
	}
	// Канал совпадает — повторной отправки нет.
	if _, err := c2.Init(ctx, nil); err != nil || cp.n() != 1 {
		t.Errorf("second Init: err=%v sent=%d", err, cp.n())
	}
	// Смена канала — снова отправка.
	c2.ChannelID = "new-channel"
	s, err = c2.Init(ctx, nil)
	if err != nil || s.ChannelID != "new-channel" || cp.n() != 2 {
		t.Errorf("channel change: %+v, %v, sent=%d", s, err, cp.n())
	}
}

func TestInitSendFailureStillSavesChannel(t *testing.T) {
	c, _ := newClient(t)
	cp := &capture{}
	srv := httptest.NewServer(cp.handler(http.StatusBadGateway))
	defer srv.Close()
	c.Endpoint, c.HTTP = srv.URL, srv.Client()
	s, err := c.Init(context.Background(), func() bool { return true })
	if err == nil {
		t.Error("send error must be reported")
	}
	if s.ChannelID != DefaultChannelID {
		t.Errorf("channel = %q", s.ChannelID)
	}
	if loaded, _ := c.LoadSettings(); loaded.ChannelID != DefaultChannelID || !loaded.Enabled {
		t.Errorf("persisted = %+v", loaded)
	}
}

// ---- подсказки ----

const testProvider = "MTS - Moscow"

func recsTSV(provider string, total int) string {
	return strings.Join([]string{
		"meta\t1\t" + provider + "\t" + itoa(total) + "\t1700000000",
		"profile\t1\t" + itoa(total) + "\t1\t40.5\t62.5",
		"profile\t2\t" + itoa(total) + "\t0\t-\t-",
		"profile\t3\t" + itoa(total) + "\t0\t10\t5",
		"profile\t4\t0\t0\t-\t-",
		"strategy\t1\t7\t88.5\t" + itoa(total) + "\tclone",
		"strategy\t1\t3\t70\t4\tclassic",
		"strategy\t2\t2\t55\t5\tmixed",
		"",
	}, "\n")
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestParseRecommendationsValid(t *testing.T) {
	rec, err := ParseRecommendations([]byte(recsTSV(testProvider, 12)), testProvider)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusReady || rec.Samples != 12 || rec.GeneratedAt != 1700000000 {
		t.Errorf("rec = %+v", rec)
	}
	p1 := rec.Profiles[1]
	if !p1.CloneRecommended || p1.ClassicPct == nil || *p1.ClassicPct != 40.5 || *p1.ClonePct != 62.5 {
		t.Errorf("profile 1 = %+v", p1)
	}
	if rec.Profiles[2].ClassicPct != nil {
		t.Error("'-' must map to nil")
	}
	top := rec.Top(1)
	if len(top) != 2 || top[0].Strategy != 7 || top[0].Mode != "clone" || top[0].SuccessPct != 88.5 {
		t.Errorf("top(1) = %+v", top)
	}
	if len(rec.Top(4)) != 0 {
		t.Error("top(4) must be empty")
	}
	var nilRec *Recommendations
	if nilRec.Top(1) != nil {
		t.Error("nil Top")
	}
}

func TestParseRecommendationsInsufficient(t *testing.T) {
	data := strings.Join([]string{
		"meta\t1\t" + testProvider + "\t3\t1700000000",
		"profile\t1\t3\t0\t-\t-",
		"profile\t2\t3\t0\t-\t-",
		"profile\t3\t0\t0\t-\t-",
		"profile\t4\t0\t0\t-\t-",
	}, "\n")
	rec, err := ParseRecommendations([]byte(data), testProvider)
	if err != nil || rec.Status != StatusInsufficient {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestParseRecommendationsRejects(t *testing.T) {
	valid := recsTSV(testProvider, 12)
	mut := func(old, new string) string { return strings.Replace(valid, old, new, 1) }
	cases := map[string]string{
		"empty":             "",
		"other provider":    mut(testProvider, "Beeline"),
		"bad version":       mut("meta\t1\t", "meta\t2\t"),
		"first not meta":    "profile\t1\t1\t0\t-\t-\n" + valid,
		"duplicate profile": mut("profile\t2\t", "profile\t1\t"),
		"unknown profile":   mut("profile\t4\t0", "profile\t11\t0"),
		"samples > total":   mut("profile\t3\t12", "profile\t3\t13"),
		"bad clone flag":    mut("\t1\t40.5", "\t2\t40.5"),
		"pct > 100":         mut("\t88.5\t", "\t100.5\t"),
		"pct garbage":       mut("\t88.5\t", "\tabc\t"),
		"clone not better":  mut("40.5\t62.5", "62.5\t40.5"),
		"clone dash":        mut("40.5\t62.5", "-\t62.5"),
		"bad mode":          mut("\tmixed", "\tfancy"),
		"strategy zero":     mut("strategy\t1\t7", "strategy\t1\t0"),
		"dup strategy":      mut("strategy\t1\t3", "strategy\t1\t7"),
		"unknown row":       mut("strategy\t2\t2", "bogus\t2\t2"),
		"missing profile 4": strings.Replace(valid, "profile\t4\t0\t0\t-\t-\n", "", 1),
		"too many lines":    valid + strings.Repeat("strategy\t3\t1\t50\t5\tclassic\n", 0) + strings.Repeat("profile\t1\t1\t0\t-\t-\n", 20),
		"too big":           valid + strings.Repeat("#", MaxRecsBytes),
		"wrong columns":     mut("meta\t1\t"+testProvider+"\t12\t1700000000", "meta\t1\t"+testProvider+"\t12"),
		"zero generated_at": mut("1700000000", "0"),
	}
	for name, data := range cases {
		if _, err := ParseRecommendations([]byte(data), testProvider); !errors.Is(err, ErrInvalidRecs) {
			t.Errorf("%s: err = %v, want ErrInvalidRecs", name, err)
		}
	}
	// 4-я стратегия профиля — превышение лимита.
	extra := strings.Replace(valid, "strategy\t2\t2\t55\t5\tmixed\n",
		"strategy\t1\t9\t50\t5\tclassic\nstrategy\t1\t8\t50\t5\tclassic\nstrategy\t2\t2\t55\t5\tmixed\n", 1)
	if _, err := ParseRecommendations([]byte(extra), testProvider); !errors.Is(err, ErrInvalidRecs) {
		t.Errorf("4 strategies per profile: err = %v", err)
	}
	// Мало данных для стратегий при total < 10.
	small := strings.ReplaceAll(strings.ReplaceAll(valid, "\t12\t", "\t05\t"), "\t12\n", "\t05\n")
	if _, err := ParseRecommendations([]byte(small), testProvider); !errors.Is(err, ErrInvalidRecs) {
		t.Errorf("strategies with total<10: err = %v", err)
	}
}

type recsServer struct {
	hits     int32
	body     atomic.Value // string
	status   int32
	gotQuery atomic.Value // string
}

func newRecsServer(t *testing.T, body string) (*recsServer, *httptest.Server) {
	t.Helper()
	rs := &recsServer{status: 200}
	rs.body.Store(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&rs.hits, 1)
		rs.gotQuery.Store(r.URL.Query().Get("provider"))
		if st := int(atomic.LoadInt32(&rs.status)); st != 200 {
			http.Error(w, "x", st)
			return
		}
		w.Write([]byte(rs.body.Load().(string)))
	}))
	t.Cleanup(srv.Close)
	return rs, srv
}

func TestFetchRecommendationsCaches24h(t *testing.T) {
	c, p := newClient(t)
	rs, srv := newRecsServer(t, recsTSV(testProvider, 12))
	c.RecsURL, c.HTTP = srv.URL+"/recs.tsv", srv.Client()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }
	ctx := context.Background()

	// Провайдер неизвестен — запроса нет.
	rec, err := c.FetchRecommendations(ctx)
	if err != nil || rec.Status != StatusUnknownProvider || rs.hits != 0 {
		t.Fatalf("unknown provider: %+v, %v, hits=%d", rec, err, rs.hits)
	}

	setProvider(t, p, testProvider)
	rec, err = c.FetchRecommendations(ctx)
	if err != nil || rec.Status != StatusReady || rs.hits != 1 {
		t.Fatalf("first fetch: %+v, %v, hits=%d", rec, err, rs.hits)
	}
	if rs.gotQuery.Load() != testProvider {
		t.Errorf("provider query = %v", rs.gotQuery.Load())
	}
	if _, err := os.Stat(c.RecsFile()); err != nil {
		t.Errorf("cache file missing: %v", err)
	}

	// В течение суток — из кэша.
	now = now.Add(23 * time.Hour)
	rec, _ = c.FetchRecommendations(ctx)
	if rs.hits != 1 || rec.Status != StatusReady {
		t.Errorf("within TTL: hits=%d status=%s", rs.hits, rec.Status)
	}

	// Через сутки — новый запрос.
	now = now.Add(2 * time.Hour)
	rec, _ = c.FetchRecommendations(ctx)
	if rs.hits != 2 || rec.Status != StatusReady {
		t.Errorf("after TTL: hits=%d status=%s", rs.hits, rec.Status)
	}

	// Смена провайдера — запрос немедленно, даже в пределах суток.
	setProvider(t, p, "Beeline")
	rs.body.Store(recsTSV("Beeline", 15))
	rec, _ = c.FetchRecommendations(ctx)
	if rs.hits != 3 || rec.Provider != "Beeline" || rec.Samples != 15 {
		t.Errorf("provider change: hits=%d rec=%+v", rs.hits, rec)
	}
}

func TestFetchRecommendationsFailureIsCachedAndStale(t *testing.T) {
	c, p := newClient(t)
	rs, srv := newRecsServer(t, recsTSV(testProvider, 12))
	c.RecsURL, c.HTTP = srv.URL, srv.Client()
	now := time.Now()
	c.Now = func() time.Time { return now }
	setProvider(t, p, testProvider)
	ctx := context.Background()

	if rec, _ := c.FetchRecommendations(ctx); rec.Status != StatusReady {
		t.Fatalf("status = %s", rec.Status)
	}
	// Сервер падает после суток: остаётся старый кэш со статусом stale.
	atomic.StoreInt32(&rs.status, 503)
	now = now.Add(25 * time.Hour)
	rec, err := c.FetchRecommendations(ctx)
	if err != nil || rec.Status != StatusStale || rec.Samples != 12 {
		t.Fatalf("after failure: %+v, %v", rec, err)
	}
	hits := atomic.LoadInt32(&rs.hits)
	// Ошибка тоже кэшируется на сутки: повторного запроса нет.
	now = now.Add(time.Hour)
	rec, _ = c.FetchRecommendations(ctx)
	if atomic.LoadInt32(&rs.hits) != hits || rec.Status != StatusStale {
		t.Errorf("failure must be cached: hits %d→%d status=%s", hits, rs.hits, rec.Status)
	}
}

func TestFetchRecommendationsUnavailable(t *testing.T) {
	c, p := newClient(t)
	rs, srv := newRecsServer(t, "garbage\n")
	c.RecsURL, c.HTTP = srv.URL, srv.Client()
	setProvider(t, p, testProvider)
	rec, err := c.FetchRecommendations(context.Background())
	if err != nil || rec.Status != StatusUnavailable {
		t.Fatalf("invalid body: %+v, %v", rec, err)
	}
	if _, err := os.Stat(c.RecsFile()); err == nil {
		t.Error("invalid data must not be cached")
	}
	if rs.hits != 1 {
		t.Errorf("hits = %d", rs.hits)
	}
}

func TestFetchRecommendationsRejectsOversizedBody(t *testing.T) {
	c, p := newClient(t)
	_, srv := newRecsServer(t, recsTSV(testProvider, 12)+strings.Repeat("x", MaxRecsBytes))
	c.RecsURL, c.HTTP = srv.URL, srv.Client()
	setProvider(t, p, testProvider)
	rec, err := c.FetchRecommendations(context.Background())
	if err != nil || rec.Status != StatusUnavailable {
		t.Fatalf("oversized: %+v, %v", rec, err)
	}
}

func TestLoadRecommendationsOffline(t *testing.T) {
	c, p := newClient(t)
	rec, err := c.LoadRecommendations()
	if err != nil || rec.Status != StatusUnknownProvider {
		t.Fatalf("no provider: %+v, %v", rec, err)
	}
	setProvider(t, p, testProvider)
	if rec, _ := c.LoadRecommendations(); rec.Status != StatusUnavailable {
		t.Errorf("no cache status = %s", rec.Status)
	}
	os.MkdirAll(filepath.Dir(c.RecsFile()), 0o755)
	os.WriteFile(c.RecsFile(), []byte(recsTSV(testProvider, 12)), 0o644)
	if rec, _ := c.LoadRecommendations(); rec.Status != StatusReady {
		t.Errorf("status = %s", rec.Status)
	}
	// Кэш другого провайдера не используется.
	setProvider(t, p, "Other")
	if rec, _ := c.LoadRecommendations(); rec.Status != StatusUnavailable {
		t.Errorf("foreign cache status = %s", rec.Status)
	}
}

func TestKnownProvider(t *testing.T) {
	for _, s := range []string{"", "  ", "Unknown", provider.Unknown} {
		if KnownProvider(s) {
			t.Errorf("KnownProvider(%q) = true", s)
		}
	}
	if !KnownProvider("MTS") {
		t.Error("MTS must be known")
	}
}

func TestNoHardcodedTokenInSource(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "telemetry.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "TzeiCfYn5DUIwjHJ6dPa4bSKrkFRZqts3BGWpA9l") {
		t.Fatal("stats token must not be hardcoded")
	}
}

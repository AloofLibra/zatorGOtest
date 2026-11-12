package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AloofLibra/zator/internal/paths"
)

const (
	ipwhoOK    = `{"success":true,"city":"Moscow","connection":{"asn":8359,"isp":"PJSC MTS"}}`
	ipwhoFail  = `{"success":false,"message":"rate limited"}`
	ipinfoOK   = `{"org":"AS12389 PJSC Rostelecom","city":"Kazan"}`
	ipinfoNone = `{"bogon":true}`
	ipapiOK    = `{"isp":"Some Telecom LLC","city":"Omsk","as":"AS64500 Some Telecom"}`
)

type fixture struct {
	d    *Detector
	p    paths.Paths
	srv  *httptest.Server
	hits map[string]*int32
	resp map[string]string // path -> body ("" => 500)
}

func newFixture(t *testing.T, resp map[string]string) *fixture {
	t.Helper()
	f := &fixture{resp: resp, hits: map[string]*int32{}}
	for k := range resp {
		f.hits[k] = new(int32)
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := f.hits[r.URL.Path]; ok {
			atomic.AddInt32(c, 1)
		}
		body, ok := f.resp[r.URL.Path]
		if !ok || body == "" {
			http.Error(w, "fail", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(f.srv.Close)
	f.p = paths.New(t.TempDir(), t.TempDir())
	f.d = New(f.p)
	f.d.HTTP = f.srv.Client()
	f.d.IPWhoURL = f.srv.URL + "/ipwho"
	f.d.IPInfoURL = f.srv.URL + "/ipinfo"
	f.d.IPAPIURL = f.srv.URL + "/ipapi"
	f.d.ASNRemoteURL = ""
	return f
}

func (f *fixture) count(path string) int { return int(atomic.LoadInt32(f.hits[path])) }

func TestDetectFirstServiceWins(t *testing.T) {
	f := newFixture(t, map[string]string{"/ipwho": ipwhoOK, "/ipinfo": ipinfoOK, "/ipapi": ipapiOK})
	res, err := f.d.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Result{ASN: 8359, ISP: "PJSC MTS", City: "Moscow", Brand: "MTS", Display: "MTS - Moscow", Source: "ipwho.is"}
	if res != want {
		t.Errorf("Result = %+v, want %+v", res, want)
	}
	if f.count("/ipinfo") != 0 || f.count("/ipapi") != 0 {
		t.Error("fallback services must not be queried when ipwho.is gives ASN")
	}
	if s, ok := f.d.Get(); !ok || s != "MTS - Moscow" {
		t.Errorf("Get = %q, %v", s, ok)
	}
	raw, _ := os.ReadFile(CachePath(f.p))
	if string(raw) != "MTS - Moscow\n" {
		t.Errorf("cache file = %q", raw)
	}
}

func TestDetectFallsBackToIPInfo(t *testing.T) {
	f := newFixture(t, map[string]string{"/ipwho": ipwhoFail, "/ipinfo": ipinfoOK, "/ipapi": ipapiOK})
	res, err := f.d.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.ASN != 12389 || res.Brand != "Rostelecom" || res.Display != "Rostelecom - Kazan" || res.Source != "ipinfo.io" {
		t.Errorf("Result = %+v", res)
	}
	if f.count("/ipapi") != 0 {
		t.Error("ip-api must not be queried")
	}
}

func TestDetectFallsBackToIPAPIUnknownASN(t *testing.T) {
	f := newFixture(t, map[string]string{"/ipwho": "", "/ipinfo": ipinfoNone, "/ipapi": ipapiOK})
	res, err := f.d.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.ASN != 64500 || res.Brand != "Some Telecom LLC (AS64500)" || res.Display != "Some Telecom LLC (AS64500) - Omsk" {
		t.Errorf("Result = %+v", res)
	}
	if res.Source != "ip-api.com" {
		t.Errorf("Source = %q", res.Source)
	}
}

func TestDetectASNOnlyWithoutISP(t *testing.T) {
	f := newFixture(t, map[string]string{
		"/ipwho":  `{"success":true,"connection":{"asn":64511}}`,
		"/ipinfo": "", "/ipapi": "",
	})
	res, err := f.d.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Brand != "AS64511" || res.Display != "AS64511" {
		t.Errorf("Result = %+v", res)
	}
}

func TestDetectISPWithoutASN(t *testing.T) {
	f := newFixture(t, map[string]string{"/ipwho": "", "/ipinfo": "", "/ipapi": `{"isp":"Lone ISP","city":"Lone ISP"}`})
	res, err := f.d.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Город совпадает с брендом — не дублируется.
	if res.Brand != "Lone ISP" || res.Display != "Lone ISP" || res.ASN != 0 {
		t.Errorf("Result = %+v", res)
	}
}

func TestDetectNothing(t *testing.T) {
	f := newFixture(t, map[string]string{"/ipwho": "", "/ipinfo": "", "/ipapi": ""})
	if _, err := f.d.Detect(context.Background()); !errors.Is(err, ErrNotDetected) {
		t.Fatalf("err = %v, want ErrNotDetected", err)
	}
	if _, ok := f.d.Get(); ok {
		t.Error("cache must stay empty after a failed detection")
	}
	if got := f.d.Menu(); got != Unknown {
		t.Errorf("Menu = %q", got)
	}
}

func TestDetectContextCancelled(t *testing.T) {
	f := newFixture(t, map[string]string{"/ipwho": ipwhoOK})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.d.Detect(ctx); !errors.Is(err, ErrNotDetected) {
		t.Fatalf("err = %v", err)
	}
}

func TestDetectSanitizesFields(t *testing.T) {
	long := strings.Repeat("Я", 100)
	f := newFixture(t, map[string]string{
		"/ipwho": `{"success":true,"city":"  Mos\tcow\r\n ","connection":{"asn":64512,"isp":"` + long + `"}}`,
	})
	res, err := f.d.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.City != "Moscow" {
		t.Errorf("City = %q", res.City)
	}
	if n := len([]rune(res.ISP)); n != MaxFieldLen {
		t.Errorf("ISP length = %d, want %d", n, MaxFieldLen)
	}
}

func TestSetGetClearManual(t *testing.T) {
	f := newFixture(t, map[string]string{})
	if _, ok := f.d.Get(); ok {
		t.Fatal("Get on empty cache")
	}
	if _, err := f.d.Set("  ", "x"); !errors.Is(err, ErrEmpty) {
		t.Errorf("Set empty err = %v", err)
	}
	got, err := f.d.Set("MTS", "Samara")
	if err != nil || got != "MTS - Samara" {
		t.Fatalf("Set = %q, %v", got, err)
	}
	if s, _ := f.d.Get(); s != "MTS - Samara" {
		t.Errorf("Get = %q", s)
	}
	if got, _ := f.d.Set("Beeline", ""); got != "Beeline" {
		t.Errorf("Set no city = %q", got)
	}
	if f.d.Menu() != "Beeline" {
		t.Errorf("Menu = %q", f.d.Menu())
	}
	if s, ok := ReadCache(f.p); !ok || s != "Beeline" {
		t.Errorf("ReadCache = %q, %v", s, ok)
	}
	if err := f.d.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Clear(); err != nil {
		t.Errorf("second Clear = %v", err)
	}
	if _, ok := f.d.Get(); ok {
		t.Error("cache not cleared")
	}
}

func TestInitUsesCacheThenDetects(t *testing.T) {
	f := newFixture(t, map[string]string{"/ipwho": ipwhoOK})
	s, err := f.d.Init(context.Background())
	if err != nil || s != "MTS - Moscow" {
		t.Fatalf("Init = %q, %v", s, err)
	}
	s, err = f.d.Init(context.Background())
	if err != nil || s != "MTS - Moscow" {
		t.Fatalf("Init(cached) = %q, %v", s, err)
	}
	if f.count("/ipwho") != 1 {
		t.Errorf("ipwho hits = %d, want 1", f.count("/ipwho"))
	}

	g := newFixture(t, map[string]string{"/ipwho": "", "/ipinfo": "", "/ipapi": ""})
	if s, err := g.d.Init(context.Background()); err == nil || s != Unknown {
		t.Errorf("Init failing = %q, %v", s, err)
	}
}

func TestRedetectReplacesCache(t *testing.T) {
	f := newFixture(t, map[string]string{"/ipwho": ipwhoOK})
	f.d.Set("Manual", "")
	res, err := f.d.Redetect(context.Background())
	if err != nil || res.Display != "MTS - Moscow" {
		t.Fatalf("Redetect = %+v, %v", res, err)
	}
	if s, _ := f.d.Get(); s != "MTS - Moscow" {
		t.Errorf("cache = %q", s)
	}
}

func TestParseDatabase(t *testing.T) {
	good := "# c\n1:A:\n2:B:x,y\n3:C\n4:D:\n5:E:z\n\n"
	rows, err := ParseDatabase(good)
	if err != nil || len(rows) != 5 {
		t.Fatalf("ParseDatabase = %v, %v", rows, err)
	}
	if !reflect.DeepEqual(rows[1].Aliases, []string{"x", "y"}) || rows[1].Brand != "B" {
		t.Errorf("row[1] = %+v", rows[1])
	}
	if _, err := ParseDatabase("1:A:\n2:B:\n3:C:\n4:D:\n"); !errors.Is(err, ErrInvalidDB) {
		t.Errorf("too few rows err = %v", err)
	}
	if _, err := ParseDatabase(good + "garbage line\n"); !errors.Is(err, ErrInvalidDB) {
		t.Errorf("garbage err = %v", err)
	}
	if _, err := ParseDatabase(good + "x:A:\n"); !errors.Is(err, ErrInvalidDB) {
		t.Errorf("non-numeric ASN err = %v", err)
	}
}

func TestBuiltinTable(t *testing.T) {
	tb := BuiltinTable()
	if tb.Source != SourceBuiltin || tb.Len() != 16 {
		t.Fatalf("builtin = %s/%d", tb.Source, tb.Len())
	}
	if b, ok := tb.Lookup(8359); !ok || b != "MTS" {
		t.Errorf("Lookup(8359) = %q, %v", b, ok)
	}
	if _, ok := tb.Lookup(1); ok {
		t.Error("Lookup(1) must fail")
	}
	if got := tb.BrandAliases("Dom.ru"); !reflect.DeepEqual(got, []string{"ER-Telecom"}) {
		t.Errorf("BrandAliases(Dom.ru) = %v", got)
	}
	if got := tb.BrandAliases("Nope"); got != nil {
		t.Errorf("BrandAliases(Nope) = %v", got)
	}
	var nilT *Table
	if nilT.Len() != 0 || nilT.Entries() != nil || nilT.BrandAliases("x") != nil {
		t.Error("nil table must be safe")
	}
}

func TestLoadTablePriorityAndMerge(t *testing.T) {
	f := newFixture(t, nil)
	if _, src := f.d.LoadTable(); src != SourceBuiltin {
		t.Errorf("source = %s, want builtin", src)
	}

	// assets/data/providers/asn.txt: переопределяет бренд и добавляет новые ASN.
	dbFile := "# ver\n8359:MTS-Custom:\n900001:Alpha:a1,a2\n900002:Beta:\n900003:Gamma:\n900004:Delta:\n"
	if err := os.MkdirAll(filepath.Dir(f.d.ASNDBFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.d.ASNDBFile, []byte(dbFile), 0o644); err != nil {
		t.Fatal(err)
	}
	tb, src := f.d.LoadTable()
	if src != SourceFile || tb.Source != SourceFile {
		t.Fatalf("source = %s", src)
	}
	if b, _ := tb.Lookup(8359); b != "MTS-Custom" {
		t.Errorf("file must override builtin, got %q", b)
	}
	if b, ok := tb.Lookup(900001); !ok || b != "Alpha" {
		t.Errorf("new ASN missing: %q %v", b, ok)
	}
	if b, ok := tb.Lookup(12389); !ok || b != "Rostelecom" {
		t.Errorf("builtin ASN lost: %q %v", b, ok)
	}
	if b, al, ok := f.d.Lookup(900001); !ok || b != "Alpha" || !reflect.DeepEqual(al, []string{"a1", "a2"}) {
		t.Errorf("Detector.Lookup = %q %v %v", b, al, ok)
	}

	// Кэш приоритетнее файла.
	cache := "8359:MTS-Cache:\n900001:AlphaCache:\n900002:B:\n900003:C:\n900004:D:\n"
	if err := os.MkdirAll(filepath.Dir(f.d.ASNCacheFile), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(f.d.ASNCacheFile, []byte(cache), 0o644)
	tb, src = f.d.LoadTable()
	if src != SourceCache {
		t.Fatalf("source = %s, want cache", src)
	}
	if b, _ := tb.Lookup(8359); b != "MTS-Cache" {
		t.Errorf("cache must win, got %q", b)
	}

	// Битый кэш игнорируется.
	os.WriteFile(f.d.ASNCacheFile, []byte("broken\n"), 0o644)
	if _, src = f.d.LoadTable(); src != SourceFile {
		t.Errorf("source = %s, want file after broken cache", src)
	}
}

func TestDetectUsesDatabaseBrand(t *testing.T) {
	f := newFixture(t, map[string]string{"/ipwho": `{"success":true,"city":"Perm","connection":{"asn":900001,"isp":"Alpha LLC"}}`})
	os.MkdirAll(filepath.Dir(f.d.ASNDBFile), 0o755)
	os.WriteFile(f.d.ASNDBFile, []byte("900001:Alpha:\n900002:B:\n900003:C:\n900004:D:\n900005:E:\n"), 0o644)
	res, err := f.d.Detect(context.Background())
	if err != nil || res.Brand != "Alpha" || res.Display != "Alpha - Perm" {
		t.Fatalf("Result = %+v, %v", res, err)
	}
}

func TestDefaultsFromPaths(t *testing.T) {
	p := paths.New(t.TempDir(), t.TempDir())
	d := New(p)
	if d.ASNDBFile != filepath.Join(p.DataDir, "providers", "asn.txt") {
		t.Errorf("ASNDBFile = %s", d.ASNDBFile)
	}
	if filepath.Dir(d.CacheFile()) != filepath.Join(p.StateDir, "cache") {
		t.Errorf("CacheFile = %s", d.CacheFile())
	}
	if d.ASNRemoteURL != DefaultASNRemoteURL || d.ASNTTL != DefaultASNTTL {
		t.Error("defaults")
	}
}

func TestBundledASNFileIsValid(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "assets", "data", "providers", "asn.txt"))
	if err != nil {
		t.Skipf("asset not present: %v", err)
	}
	rows, err := ParseDatabase(string(data))
	if err != nil {
		t.Fatalf("bundled asn.txt invalid: %v", err)
	}
	if len(rows) < minDBRows {
		t.Errorf("rows = %d", len(rows))
	}
	// Файл из assets подхватывается детектором как источник "file".
	f := newFixture(t, nil)
	f.d.ASNDBFile = filepath.Join("..", "..", "assets", "data", "providers", "asn.txt")
	tb, src := f.d.LoadTable()
	if b, _ := tb.Lookup(8359); src != SourceFile || b != "MTS" {
		t.Errorf("bundled table = %s / %q", src, b)
	}
}

func TestUpdateDatabase(t *testing.T) {
	var hits int32
	body := "1:A:\n2:B:\n3:C:\n4:D:\n5:E:x\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte(body))
		case "/bad":
			w.Write([]byte("not a table\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := paths.New(t.TempDir(), t.TempDir())
	d := New(p)
	d.HTTP = srv.Client()
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	d.Now = func() time.Time { return now }

	// Отключено.
	d.ASNRemoteURL = ""
	if err := d.UpdateDatabase(context.Background()); err != nil || hits != 0 {
		t.Fatalf("disabled: err=%v hits=%d", err, hits)
	}

	// Невалидная таблица и 404 отвергаются, кэш не создаётся.
	for _, path := range []string{"/bad", "/missing"} {
		d.ASNRemoteURL = srv.URL + path
		if err := d.UpdateDatabase(context.Background()); err == nil {
			t.Errorf("%s: expected error", path)
		}
		if _, err := os.Stat(d.ASNCacheFile); err == nil {
			t.Errorf("%s: cache must not be created", path)
		}
	}

	// Успех.
	d.ASNRemoteURL = srv.URL + "/ok"
	if err := d.UpdateDatabase(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(d.ASNCacheFile)
	if string(got) != body {
		t.Fatalf("cache = %q", got)
	}
	if _, src := d.LoadTable(); src != SourceCache {
		t.Errorf("source = %s", src)
	}

	// Свежий кэш — повторного запроса нет.
	before := atomic.LoadInt32(&hits)
	now = now.Add(24 * time.Hour)
	// mtime файла — «сейчас» реальное время; сдвигаем его к нашему now.
	os.Chtimes(d.ASNCacheFile, now.Add(-time.Hour), now.Add(-time.Hour))
	if err := d.UpdateDatabase(context.Background()); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&hits) != before {
		t.Error("fresh cache must not be refetched")
	}

	// Просроченный — перезагружается.
	now = now.Add(DefaultASNTTL + time.Hour)
	if err := d.UpdateDatabase(context.Background()); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&hits) != before+1 {
		t.Errorf("stale cache must be refetched, hits=%d before=%d", hits, before)
	}
}

func TestClean(t *testing.T) {
	if got := Clean("  a\tb\r\n "); got != "ab" {
		t.Errorf("Clean = %q", got)
	}
	if got := Clean(strings.Repeat("x", 80)); len(got) != MaxFieldLen {
		t.Errorf("len = %d", len(got))
	}
}

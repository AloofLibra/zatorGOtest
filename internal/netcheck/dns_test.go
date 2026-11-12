package netcheck

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- ProbeDNS / серия ---

func lookupSeq(results ...func() ([]string, error)) (LookupFunc, *int32) {
	var n int32
	return func(ctx context.Context, domain, server string) ([]string, error) {
		i := int(atomic.AddInt32(&n, 1)) - 1
		if i >= len(results) {
			i = len(results) - 1
		}
		return results[i]()
	}, &n
}

func ret(addrs ...string) func() ([]string, error) {
	return func() ([]string, error) { return addrs, nil }
}

func fail(err error) func() ([]string, error) {
	return func() ([]string, error) { return nil, err }
}

var nxErr = &net.DNSError{Err: "no such host", IsNotFound: true}

func TestProbeDNS_States(t *testing.T) {
	cases := []struct {
		name   string
		addrs  []string
		err    error
		state  string
		reason string
	}{
		{"match v4", []string{"204.8.99.144"}, nil, "ok", ReasonMatch},
		{"match via v6", []string{"1.2.3.4", "2a01:4f9:c010:19eb::1"}, nil, "ok", ReasonMatch},
		{"rotate", []string{"1.2.3.4"}, nil, "warn", ReasonRotate},
		{"v6 only", []string{"2a01:4f9:c010:19eb::1"}, nil, "fail", ReasonEmpty},
		{"nxdomain", nil, nxErr, "fail", ReasonNXDomain},
		{"timeout", nil, &net.DNSError{Err: "i/o timeout", IsTimeout: true}, "fail", ReasonNoAnswer},
		{"other err", nil, errors.New("boom"), "fail", ReasonNoAnswer},
		{"empty", nil, nil, "fail", ReasonEmpty},
		{"garbage", []string{"not-an-ip"}, nil, "fail", ReasonEmpty},
	}
	for _, c := range cases {
		cfg := DNSSeriesConfig{Lookup: func(ctx context.Context, d, s string) ([]string, error) { return c.addrs, c.err }}
		p := ProbeDNS(context.Background(), cfg)
		if p.State != c.state || p.Reason != c.reason {
			t.Errorf("%s: got %s/%s, want %s/%s", c.name, p.State, p.Reason, c.state, c.reason)
		}
	}
}

func TestProbeDNS_PassesDomainAndServer(t *testing.T) {
	var gd, gs string
	cfg := DNSSeriesConfig{Domain: "a.example", Server: "9.9.9.9", Lookup: func(ctx context.Context, d, s string) ([]string, error) {
		gd, gs = d, s
		return nil, nil
	}}
	ProbeDNS(context.Background(), cfg)
	if gd != "a.example" || gs != "9.9.9.9" {
		t.Errorf("got %q @ %q", gd, gs)
	}
	ProbeDNS(context.Background(), DNSSeriesConfig{Lookup: cfg.Lookup})
	if gd != DefaultDNSCheckDomain || gs != DefaultDNSCheckServer {
		t.Errorf("defaults: %q @ %q", gd, gs)
	}
}

func TestRunDNSSeries_Aggregation(t *testing.T) {
	noSleep := func(ctx context.Context, d time.Duration) error { return nil }
	cases := []struct {
		name    string
		seq     []func() ([]string, error)
		state   string
		reason  string
		ok, w   int
		fl      int
		textHas string
	}{
		{"one real is enough", []func() ([]string, error){fail(nxErr), ret("204.8.99.146"), fail(nxErr)},
			"ok", ReasonMatch, 1, 0, 2, "резолв настоящий в 1 из 3"},
		{"all match", []func() ([]string, error){ret("95.216.163.36")},
			"ok", ReasonMatch, 3, 0, 0, "3 из 3"},
		{"rotate", []func() ([]string, error){fail(nxErr), ret("5.5.5.5"), fail(nxErr)},
			"warn", ReasonRotate, 0, 1, 2, "не из эталонного набора"},
		{"spoof nxdomain", []func() ([]string, error){fail(nxErr)},
			"fail", ReasonNXDomain, 0, 0, 3, "подмена DNS во всех 3 проверках"},
		{"noanswer", []func() ([]string, error){fail(errors.New("t/o"))},
			"fail", ReasonNoAnswer, 0, 0, 3, "DNS не отвечает ни в одной из 3"},
		{"first fail reason wins", []func() ([]string, error){fail(errors.New("x")), fail(nxErr), ret()},
			"fail", ReasonNoAnswer, 0, 0, 3, "DNS не отвечает"},
		{"empty", []func() ([]string, error){ret()},
			"fail", ReasonEmpty, 0, 0, 3, "без IPv4-адресов"},
	}
	for _, c := range cases {
		lk, n := lookupSeq(c.seq...)
		r := RunDNSSeries(context.Background(), DNSSeriesConfig{Lookup: lk, Sleep: noSleep})
		if r.State != c.state || r.Reason != c.reason || r.OK != c.ok || r.Warn != c.w || r.Fail != c.fl {
			t.Errorf("%s: %+v", c.name, r)
		}
		if !strings.Contains(r.Text, c.textHas) {
			t.Errorf("%s: text = %q", c.name, r.Text)
		}
		if len(r.States) != 3 || atomic.LoadInt32(n) != 3 {
			t.Errorf("%s: states=%v calls=%d", c.name, r.States, *n)
		}
	}
}

func TestRunDNSSeries_StatesList(t *testing.T) {
	lk, _ := lookupSeq(fail(nxErr), ret("5.5.5.5"), ret("204.8.99.144"))
	r := RunDNSSeries(context.Background(), DNSSeriesConfig{Lookup: lk, Interval: -1})
	want := []string{"fail/nxdomain", "warn/rotate", "ok/match"}
	if !reflect.DeepEqual(r.States, want) {
		t.Errorf("states = %v, want %v", r.States, want)
	}
	if !reflect.DeepEqual(r.V4, []string{"204.8.99.144"}) || !reflect.DeepEqual(r.Hits, []string{"204.8.99.144"}) {
		t.Errorf("best: v4=%v hits=%v", r.V4, r.Hits)
	}
}

func TestRunDNSSeries_IntervalAndCancel(t *testing.T) {
	var sleeps []time.Duration
	lk, _ := lookupSeq(ret())
	RunDNSSeries(context.Background(), DNSSeriesConfig{
		Lookup: lk, Tries: 3, Interval: 750 * time.Millisecond,
		Sleep: func(ctx context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil },
	})
	if len(sleeps) != 2 || sleeps[0] != 750*time.Millisecond {
		t.Errorf("sleeps = %v (между 3 пробами должно быть 2 паузы)", sleeps)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	r := RunDNSSeries(ctx, DNSSeriesConfig{
		Lookup:   func(ctx context.Context, d, s string) ([]string, error) { calls++; cancel(); return nil, nil },
		Interval: time.Hour,
	})
	if calls != 1 || len(r.States) != 1 {
		t.Errorf("после отмены серия должна остановиться: calls=%d states=%v", calls, r.States)
	}
}

func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), 0); err != nil {
		t.Error(err)
	}
	if err := sleepCtx(context.Background(), 5*time.Millisecond); err != nil {
		t.Error(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); err == nil {
		t.Error("ожидали ошибку отмены")
	}
}

// --- DNS wire ---

func TestBuildDNSQuery(t *testing.T) {
	q, err := BuildDNSQuery("Example.COM.", 0x1234)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0,
		7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0,
		0, 1, 0, 1,
	}
	if !reflect.DeepEqual(q, want) {
		t.Errorf("query = %v\nwant    %v", q, want)
	}
	for _, bad := range []string{"", "a..b", "-x.com", "x.com;rm", strings.Repeat("a", 64) + ".com", "sp ace.com"} {
		if _, err := BuildDNSQuery(bad, 0); err == nil {
			t.Errorf("BuildDNSQuery(%q) должна вернуть ошибку", bad)
		}
	}
}

// dnsResponse строит ответ: вопрос + A-записи со ссылкой-сжатием на имя вопроса.
func dnsResponse(id uint16, rcode int, ips ...string) []byte {
	q, _ := BuildDNSQuery("example.com", id)
	msg := append([]byte{}, q...)
	msg[2], msg[3] = 0x81, byte(0x80|rcode)
	binary.BigEndian.PutUint16(msg[6:], uint16(len(ips)))
	for _, s := range ips {
		msg = append(msg, 0xC0, 0x0C, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
		msg = append(msg, net.ParseIP(s).To4()...)
	}
	return msg
}

func TestParseDNSAnswerA(t *testing.T) {
	ips, rcode, err := ParseDNSAnswerA(dnsResponse(0, 0, "1.2.3.4", "5.6.7.8"))
	if err != nil || rcode != 0 || !reflect.DeepEqual(ips, []string{"1.2.3.4", "5.6.7.8"}) {
		t.Fatalf("ips=%v rcode=%d err=%v", ips, rcode, err)
	}
	ips, rcode, err = ParseDNSAnswerA(dnsResponse(0, 3))
	if err != nil || rcode != 3 || len(ips) != 0 {
		t.Fatalf("nxdomain: ips=%v rcode=%d err=%v", ips, rcode, err)
	}

	// CNAME перед A — пропускается.
	msg := dnsResponse(0, 0)
	binary.BigEndian.PutUint16(msg[6:], 2)
	msg = append(msg, 0xC0, 0x0C, 0, 5, 0, 1, 0, 0, 0, 60, 0, 2, 0xC0, 0x0C) // CNAME -> имя вопроса
	msg = append(msg, 0xC0, 0x0C, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 9, 9, 9, 9)
	if ips, _, err := ParseDNSAnswerA(msg); err != nil || !reflect.DeepEqual(ips, []string{"9.9.9.9"}) {
		t.Errorf("cname+a: %v %v", ips, err)
	}

	full := dnsResponse(0, 0, "1.2.3.4")
	for _, n := range []int{0, 5, 11, 20, len(full) - 1} {
		if _, _, err := ParseDNSAnswerA(full[:n]); err == nil {
			t.Errorf("усечение до %d байт должно давать ошибку", n)
		}
	}
	bad := append([]byte{}, full...)
	bad[12] = 0x80 // недопустимый тип метки
	if _, _, err := ParseDNSAnswerA(bad); err == nil {
		t.Error("недопустимая метка должна давать ошибку")
	}
}

// --- DoH ---

func jsonDoH(ips ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type ans struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		}
		resp := struct {
			Status int   `json:"Status"`
			Answer []ans `json:"Answer"`
		}{}
		resp.Answer = append(resp.Answer, ans{5, "alias.example."}, ans{28, "2606::1"})
		for _, ip := range ips {
			resp.Answer = append(resp.Answer, ans{1, ip})
		}
		json.NewEncoder(w).Encode(resp)
	})
}

func wireDoH(t *testing.T, ips ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/dns-message" {
			t.Errorf("wire DoH: %s %q", r.Method, r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		want, _ := BuildDNSQuery("example.com", 0)
		if !reflect.DeepEqual(body, want) {
			t.Errorf("wire DoH query = %v", body)
		}
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(dnsResponse(0, 0, ips...))
	})
}

func TestDoH_JSON(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		jsonDoH("8.8.4.4", "1.1.1.1", "1.1.1.1").ServeHTTP(w, r)
	}))
	defer srv.Close()
	d := &DoH{Sources: []DoHSource{{Name: "t", URL: srv.URL + "/resolve", Kind: DoHJSON}}}
	ans, err := d.LookupA(context.Background(), "Example.COM.")
	if err != nil {
		t.Fatal(err)
	}
	if ans.Source != "t" || !reflect.DeepEqual(ans.IPs, []string{"1.1.1.1", "8.8.4.4"}) {
		t.Errorf("answer: %+v", ans)
	}
	if !strings.Contains(gotQuery, "name=example.com") || !strings.Contains(gotQuery, "type=A") {
		t.Errorf("query = %q", gotQuery)
	}
}

func TestDoH_Wire(t *testing.T) {
	srv := httptest.NewServer(wireDoH(t, "93.184.216.34"))
	defer srv.Close()
	d := &DoH{Sources: []DoHSource{{Name: "w", URL: srv.URL, Kind: DoHWire}}}
	ans, err := d.LookupA(context.Background(), "example.com")
	if err != nil || ans.Source != "w" || !reflect.DeepEqual(ans.IPs, []string{"93.184.216.34"}) {
		t.Fatalf("answer=%+v err=%v", ans, err)
	}
}

func TestDoH_CascadeFallsThrough(t *testing.T) {
	var calls []string
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "bad")
		http.Error(w, "x", http.StatusInternalServerError)
	}))
	defer bad.Close()
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "garbage")
		io.WriteString(w, "not json")
	}))
	defer garbage.Close()
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "empty")
		jsonDoH().ServeHTTP(w, r)
	}))
	defer empty.Close()
	good := httptest.NewServer(wireDoH(t, "7.7.7.7"))
	defer good.Close()
	late := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "late")
	}))
	defer late.Close()

	d := &DoH{Sources: []DoHSource{
		{Name: "bad", URL: bad.URL, Kind: DoHJSON},
		{Name: "garbage", URL: garbage.URL, Kind: DoHJSON},
		{Name: "empty", URL: empty.URL, Kind: DoHJSON},
		{Name: "good", URL: good.URL, Kind: DoHWire},
		{Name: "late", URL: late.URL, Kind: DoHJSON},
	}}
	ans, err := d.LookupA(context.Background(), "example.com")
	if err != nil || ans.Source != "good" {
		t.Fatalf("answer=%+v err=%v", ans, err)
	}
	if !reflect.DeepEqual(calls, []string{"bad", "garbage", "empty"}) {
		t.Errorf("calls = %v (после успеха остальные источники не опрашиваются)", calls)
	}
}

func TestDoH_AllFailAndBadInput(t *testing.T) {
	d := &DoH{Sources: []DoHSource{{Name: "dead", URL: "http://127.0.0.1:1/", Kind: DoHJSON}}, Timeout: time.Second}
	if _, err := d.LookupA(context.Background(), "example.com"); !errors.Is(err, ErrDoHUnavailable) {
		t.Errorf("err = %v", err)
	}
	if _, err := d.LookupA(context.Background(), "bad domain;"); err == nil || errors.Is(err, ErrDoHUnavailable) {
		t.Errorf("некорректное имя должно давать отдельную ошибку: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.LookupA(ctx, "example.com"); !errors.Is(err, ErrDoHUnavailable) {
		t.Errorf("canceled: %v", err)
	}
}

func TestDoH_CustomClientAndDefaults(t *testing.T) {
	if got := DefaultDoHSources(); len(got) != 3 || got[0].Kind != DoHJSON || !got[1].Insecure || got[2].Kind != DoHWire {
		t.Errorf("каскад по умолчанию: %+v", got)
	}
	srv := httptest.NewTLSServer(jsonDoH("4.4.4.4"))
	defer srv.Close()
	d := &DoH{Client: srv.Client(), Sources: []DoHSource{{Name: "tls", URL: srv.URL, Kind: DoHJSON}}}
	ans, err := d.LookupA(context.Background(), "example.com")
	if err != nil || len(ans.IPs) != 1 {
		t.Fatalf("answer=%+v err=%v", ans, err)
	}
	// Источник по IP с самоподписанным сертификатом работает только с Insecure.
	d2 := &DoH{Sources: []DoHSource{{Name: "strict", URL: srv.URL, Kind: DoHJSON}}}
	if _, err := d2.LookupA(context.Background(), "example.com"); err == nil {
		t.Error("без Insecure самоподписанный сертификат должен отвергаться")
	}
	d3 := &DoH{Sources: []DoHSource{{Name: "insecure", URL: srv.URL, Kind: DoHJSON, Insecure: true}}}
	if _, err := d3.LookupA(context.Background(), "example.com"); err != nil {
		t.Errorf("с Insecure: %v", err)
	}
}

func TestValidDomain(t *testing.T) {
	for d, want := range map[string]bool{
		"example.com": true, "a.b-c.d1": true, "localhost": true, "rr1---sn-abc.googlevideo.com": true,
		"": false, "-a.com": false, "a-.com": false, "a b.com": false, "a..com": false, "EXAMPLE.com": false,
		strings.Repeat("a", 254): false,
	} {
		if got := ValidDomain(d); got != want {
			t.Errorf("ValidDomain(%q) = %v, want %v", d, got, want)
		}
	}
}

// --- Гейт ---

func gateWith(t *testing.T, ref []string, sys func() ([]string, error), udp func() ([]string, error)) *SpoofGate {
	g := &SpoofGate{
		SystemLookup: func(ctx context.Context, d, s string) ([]string, error) { return sys() },
		UDPLookup: func(ctx context.Context, d, s string) ([]string, error) {
			if udp == nil {
				return nil, errors.New("udp off")
			}
			return udp()
		},
	}
	if ref != nil {
		srv := httptest.NewServer(jsonDoH(ref...))
		t.Cleanup(srv.Close)
		g.DoH = &DoH{Sources: []DoHSource{{Name: "Google DoH (test)", URL: srv.URL, Kind: DoHJSON}}}
	} else {
		g.DoH = &DoH{Sources: []DoHSource{{Name: "dead", URL: "http://127.0.0.1:1/", Kind: DoHJSON}}, Timeout: time.Second}
	}
	return g
}

func TestSpoofGate_Clean(t *testing.T) {
	g := gateWith(t, []string{"104.21.32.39", "172.67.1.1"}, ret("172.67.1.1"), nil)
	r := g.Check(context.Background())
	if r.Code != GateClean || r.Verdict != GateVerdictClean || r.Spoofed() {
		t.Fatalf("result: %+v", r)
	}
	if r.RefSource != "Google DoH (test)" || len(r.Hits) != 1 || r.Domain != DefaultRefDomain {
		t.Errorf("result: %+v", r)
	}
}

func TestSpoofGate_CleanViaKnownAddrs(t *testing.T) {
	// Эталон не совпал (CDN ротировал), но адрес в списке известных.
	g := gateWith(t, []string{"9.9.9.9"}, ret("172.67.182.196"), nil)
	if r := g.Check(context.Background()); r.Code != GateClean {
		t.Fatalf("result: %+v", r)
	}
	g.KnownAddrs = []string{"1.2.3.4"}
	if r := g.Check(context.Background()); r.Code != GateSpoofed {
		t.Fatalf("с пустым пересечением ожидали spoofed: %+v", r)
	}
}

func TestSpoofGate_SpoofedStub(t *testing.T) {
	g := gateWith(t, []string{"104.21.32.39"}, ret("10.10.10.10"), nil)
	r := g.Check(context.Background())
	if r.Code != GateSpoofed || !r.Spoofed() || r.Verdict != GateVerdictSpoofed {
		t.Fatalf("result: %+v", r)
	}
	if !strings.Contains(r.Message, "МИМО ЭТАЛОНА") || !strings.Contains(r.Message, "10.10.10.10") {
		t.Errorf("message = %q", r.Message)
	}
}

func TestSpoofGate_NXDOMAIN(t *testing.T) {
	g := gateWith(t, []string{"104.21.32.39"}, fail(nxErr), nil)
	r := g.Check(context.Background())
	if r.Code != GateSpoofed || !strings.Contains(r.Message, "NXDOMAIN") || r.SystemError == "" {
		t.Fatalf("result: %+v", r)
	}
}

func TestSpoofGate_BlockAddress(t *testing.T) {
	for _, bad := range []string{"127.0.0.1", "0.0.0.0"} {
		g := gateWith(t, nil, ret(bad), nil)
		r := g.Check(context.Background())
		if r.Code != GateSpoofed || !strings.Contains(r.Message, bad) {
			t.Errorf("%s: %+v", bad, r)
		}
	}
}

func TestSpoofGate_UnknownWithoutReference(t *testing.T) {
	g := gateWith(t, nil, ret("10.10.10.10"), nil)
	r := g.Check(context.Background())
	if r.Code != GateUnknown || r.Verdict != GateVerdictUnknown || r.Spoofed() {
		t.Fatalf("result: %+v", r)
	}
	if len(r.RefAddrs) != 0 || r.RefSource != "" {
		t.Errorf("ref: %+v", r)
	}
}

func TestSpoofGate_UnknownSystemSilent(t *testing.T) {
	g := gateWith(t, []string{"104.21.32.39"}, fail(errors.New("timeout")), nil)
	r := g.Check(context.Background())
	if r.Code != GateUnknown || !strings.Contains(r.Message, "Системный DNS не отвечает") {
		t.Fatalf("result: %+v", r)
	}
	g2 := gateWith(t, []string{"104.21.32.39"}, ret(), nil)
	if r := g2.Check(context.Background()); r.Code != GateUnknown {
		t.Fatalf("empty system answer: %+v", r)
	}
}

func TestSpoofGate_UDPFallbackReference(t *testing.T) {
	g := gateWith(t, nil, ret("55.55.55.55"), ret("55.55.55.55", "2606::1"))
	r := g.Check(context.Background())
	if r.Code != GateClean || !strings.HasPrefix(r.RefSource, "UDP 8.8.8.8") {
		t.Fatalf("result: %+v", r)
	}
	if !reflect.DeepEqual(r.RefAddrs, []string{"55.55.55.55"}) {
		t.Errorf("ref addrs: %v", r.RefAddrs)
	}

	g = gateWith(t, nil, ret("66.66.66.66"), ret("55.55.55.55"))
	if r := g.Check(context.Background()); r.Code != GateSpoofed {
		t.Fatalf("UDP-эталон не совпал -> spoofed: %+v", r)
	}
}

func TestSpoofGate_CustomDomainPassedThrough(t *testing.T) {
	var gotDomain string
	g := gateWith(t, []string{"1.1.1.1"}, ret("1.1.1.1"), nil)
	g.Domain = "example.org"
	g.SystemLookup = func(ctx context.Context, d, s string) ([]string, error) {
		gotDomain = d
		if s != "" {
			t.Errorf("системный резолвер вызван с сервером %q", s)
		}
		return []string{"1.1.1.1"}, nil
	}
	if r := g.Check(context.Background()); r.Code != GateClean || r.Domain != "example.org" || gotDomain != "example.org" {
		t.Fatalf("result: %+v domain=%q", r, gotDomain)
	}
}

func TestGateResultJSON(t *testing.T) {
	g := gateWith(t, []string{"1.1.1.1"}, ret("1.1.1.1"), nil)
	raw, err := json.Marshal(g.Check(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	for _, k := range []string{"code", "verdict", "domain", "ref_source", "ref_addrs", "system_addrs", "hits", "message"} {
		if _, ok := m[k]; !ok {
			t.Errorf("нет поля %q: %s", k, raw)
		}
	}
	if _, ok := m["system_error"]; ok {
		t.Errorf("пустой system_error не должен сериализоваться: %s", raw)
	}
}

func TestDefaultLookup_UnreachableServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	// 127.0.0.1:9 — UDP «discard»: ответа нет, должна вернуться ошибка, а не зависание.
	if _, err := DefaultLookup(ctx, "example.com", "127.0.0.1:9"); err == nil {
		t.Error("ожидали ошибку резолва")
	}
}

func TestSplitAddrs(t *testing.T) {
	v4, v6 := splitAddrs([]string{" 1.2.3.4 ", "1.2.3.4", "::1", "::1", "x", "", "2001:db8::1"})
	if !reflect.DeepEqual(v4, []string{"1.2.3.4"}) || !reflect.DeepEqual(v6, []string{"::1", "2001:db8::1"}) {
		t.Errorf("v4=%v v6=%v", v4, v6)
	}
}

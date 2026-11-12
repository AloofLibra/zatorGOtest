package netcheck

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Значения по умолчанию DNS-проверок (Z2R_DNS_* из netcheck.sh).
const (
	DefaultDNSCheckDomain = "deb.torproject.org"
	DefaultDNSCheckServer = "8.8.8.8"
	DefaultDNSTries       = 3
	DefaultDNSInterval    = time.Second

	DefaultRefDomain = "rutracker.org"
)

// DefaultDNSKnownAddrs — эталонные адреса deb.torproject.org.
var DefaultDNSKnownAddrs = []string{
	"204.8.99.144", "204.8.99.146", "95.216.163.36", "116.202.120.165", "116.202.120.166",
	"2620:7:6002:0:466:39ff:fe7f:1826", "2620:7:6002:0:466:39ff:fe32:e3dd",
	"2a01:4f8:fff0:4f:266:37ff:feae:3bbc", "2a01:4f8:fff0:4f:266:37ff:fe2c:5d19",
	"2a01:4f9:c010:19eb::1",
}

// DefaultRefAddrs — известные адреса rutracker.org (Cloudflare, 2026-09-30).
// Ротация возможна — это крайний случай, а не единственный эталон.
var DefaultRefAddrs = []string{"172.67.182.196", "104.21.32.39"}

// LookupFunc резолвит domain через резолвер server ("host" или "host:port";
// пустой — системный) и возвращает адреса IPv4 и IPv6 строками.
type LookupFunc func(ctx context.Context, domain, server string) ([]string, error)

// Причины неудачи одной пробы DNS.
const (
	ReasonMatch    = "match"    // адреса из эталона
	ReasonRotate   = "rotate"   // адреса есть, но не из эталона
	ReasonNXDomain = "nxdomain" // NXDOMAIN / нет такого хоста
	ReasonNoAnswer = "noanswer" // таймаут / ошибка / нет ответа
	ReasonEmpty    = "empty"    // ответ без IPv4
)

// DefaultLookup — LookupFunc на net.Resolver: с server — прямой UDP-запрос
// к серверу, без server — системный резолвер.
func DefaultLookup(ctx context.Context, domain, server string) ([]string, error) {
	r := net.DefaultResolver
	if server != "" {
		addr := server
		if _, _, err := net.SplitHostPort(server); err != nil {
			addr = net.JoinHostPort(server, "53")
		}
		r = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "udp", addr)
			},
		}
	}
	addrs, err := r.LookupIPAddr(ctx, domain)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.IP.String())
	}
	return out, nil
}

// DNSProbe — результат одной пробы (строка "state|reason|v4|v6|hits").
type DNSProbe struct {
	State  string   `json:"state"`  // ok|warn|fail
	Reason string   `json:"reason"` // Reason*
	V4     []string `json:"v4"`
	V6     []string `json:"v6"`
	Hits   []string `json:"hits"`
}

// DNSSeriesConfig — параметры серии проб антиспуфа DNS (профиль 10).
type DNSSeriesConfig struct {
	Domain     string        // по умолчанию deb.torproject.org
	Server     string        // по умолчанию 8.8.8.8
	Tries      int           // по умолчанию 3
	Interval   time.Duration // пауза между пробами (по умолчанию 1 с; <0 — без паузы)
	KnownAddrs []string      // эталон (по умолчанию DefaultDNSKnownAddrs)
	Lookup     LookupFunc    // по умолчанию DefaultLookup
	Sleep      func(ctx context.Context, d time.Duration) error
}

func (c DNSSeriesConfig) withDefaults() DNSSeriesConfig {
	if c.Domain == "" {
		c.Domain = DefaultDNSCheckDomain
	}
	if c.Server == "" {
		c.Server = DefaultDNSCheckServer
	}
	if c.Tries <= 0 {
		c.Tries = DefaultDNSTries
	}
	if c.Interval == 0 {
		c.Interval = DefaultDNSInterval
	}
	if c.KnownAddrs == nil {
		c.KnownAddrs = DefaultDNSKnownAddrs
	}
	if c.Lookup == nil {
		c.Lookup = DefaultLookup
	}
	if c.Sleep == nil {
		c.Sleep = sleepCtx
	}
	return c
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// splitAddrs разделяет адреса на IPv4 и IPv6 (с дедупликацией, без пустых).
func splitAddrs(addrs []string) (v4, v6 []string) {
	seen := map[string]bool{}
	for _, a := range addrs {
		ip := net.ParseIP(strings.TrimSpace(a))
		if ip == nil || seen[ip.String()] {
			continue
		}
		seen[ip.String()] = true
		if ip.To4() != nil {
			v4 = append(v4, ip.String())
		} else {
			v6 = append(v6, ip.String())
		}
	}
	return v4, v6
}

func isNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

// ProbeDNS — z2r_dns_check_target: одна проба.
func ProbeDNS(ctx context.Context, cfg DNSSeriesConfig) DNSProbe {
	cfg = cfg.withDefaults()
	addrs, err := cfg.Lookup(ctx, cfg.Domain, cfg.Server)
	v4, v6 := splitAddrs(addrs)
	if len(v4) > 0 {
		var hits []string
		for _, a := range append(append([]string{}, v4...), v6...) {
			for _, k := range cfg.KnownAddrs {
				if a == k {
					hits = append(hits, a)
				}
			}
		}
		if len(hits) > 0 {
			return DNSProbe{State: "ok", Reason: ReasonMatch, V4: v4, V6: v6, Hits: hits}
		}
		return DNSProbe{State: "warn", Reason: ReasonRotate, V4: v4, V6: v6}
	}
	switch {
	case err != nil && isNotFound(err):
		return DNSProbe{State: "fail", Reason: ReasonNXDomain}
	case err != nil:
		return DNSProbe{State: "fail", Reason: ReasonNoAnswer}
	}
	return DNSProbe{State: "fail", Reason: ReasonEmpty}
}

// DNSSeriesResult — агрегат серии (z2r_dns_check_series).
type DNSSeriesResult struct {
	State  string   `json:"state"` // ok|warn|fail
	Reason string   `json:"reason"`
	V4     []string `json:"v4"`
	V6     []string `json:"v6"`
	Hits   []string `json:"hits"`
	OK     int      `json:"n_ok"`
	Warn   int      `json:"n_warn"`
	Fail   int      `json:"n_fail"`
	// States — "state/reason" каждой пробы по порядку.
	States []string `json:"states"`
	Text   string   `json:"text"`
}

// RunDNSSeries выполняет серию проб с интервалом: одиночный ответ у ТСПУ флаки,
// вердикт ставится по серии (одного настоящего ответа достаточно).
func RunDNSSeries(ctx context.Context, cfg DNSSeriesConfig) DNSSeriesResult {
	cfg = cfg.withDefaults()
	var res DNSSeriesResult
	var okV4, okV6, okHits, warnV4, warnV6 []string
	firstFail := ""
	for i := 0; i < cfg.Tries; i++ {
		if i > 0 && cfg.Interval > 0 {
			if err := cfg.Sleep(ctx, cfg.Interval); err != nil {
				break
			}
		}
		if ctx.Err() != nil {
			break
		}
		p := ProbeDNS(ctx, cfg)
		res.States = append(res.States, p.State+"/"+p.Reason)
		switch p.State {
		case "ok":
			res.OK++
			if okV4 == nil {
				okV4, okV6, okHits = p.V4, p.V6, p.Hits
			}
		case "warn":
			res.Warn++
			if warnV4 == nil {
				warnV4, warnV6 = p.V4, p.V6
			}
		default:
			res.Fail++
			if firstFail == "" {
				firstFail = p.Reason
			}
		}
	}
	switch {
	case res.OK > 0:
		res.State, res.Reason, res.V4, res.V6, res.Hits = "ok", ReasonMatch, okV4, okV6, okHits
	case res.Warn > 0:
		res.State, res.Reason, res.V4, res.V6 = "warn", ReasonRotate, warnV4, warnV6
	default:
		res.State = "fail"
		res.Reason = firstFail
		if res.Reason == "" {
			res.Reason = ReasonEmpty
		}
	}
	res.Text = res.describe(cfg)
	return res
}

// describe — z2r_dns_series_text.
func (r DNSSeriesResult) describe(cfg DNSSeriesConfig) string {
	total := r.OK + r.Warn + r.Fail
	switch r.State {
	case "ok":
		return fmt.Sprintf("резолв настоящий в %d из %d проверок (адреса: %s, эталон torproject:%s)",
			r.OK, total, strings.Join(r.V4, " "), strings.Join(r.Hits, " "))
	case "warn":
		return fmt.Sprintf("адреса приходят (%d из %d проверок), но не из эталонного набора torproject - возможно, набор сменился. Сверьте вручную: dig +tcp %s @%s",
			r.Warn, total, cfg.Domain, cfg.Server)
	}
	switch r.Reason {
	case ReasonNXDomain:
		return fmt.Sprintf("подмена DNS во всех %d проверках: NXDOMAIN/без адресов от %s @ %s", total, cfg.Domain, cfg.Server)
	case ReasonNoAnswer:
		return fmt.Sprintf("DNS не отвечает ни в одной из %d проверок (%s @ %s) - вероятно, дропается оригинал запроса", total, cfg.Domain, cfg.Server)
	}
	return fmt.Sprintf("ответ DNS без IPv4-адресов во всех %d проверках (%s @ %s)", total, cfg.Domain, cfg.Server)
}

// CheckDNSAntispoof — check.cgi?profile=10: серия DNS-проб в формате TargetResult.
func (c *Checker) CheckDNSAntispoof(ctx context.Context) TargetResult {
	cfg := c.DNS.withDefaults()
	r := RunDNSSeries(ctx, cfg)
	detail := VersionDetail{Proto: "-", Time: "-", IP: "-", State: StateNone}
	return TargetResult{
		Label:       LabelDNS,
		Target:      fmt.Sprintf("nslookup %s @ %s", cfg.Domain, cfg.Server),
		Verdict:     r.State,
		Text:        r.Text,
		TLS12Detail: detail,
		TLS13Detail: detail,
	}
}

// --- Гейт подмены DNS ---

// GateCode — код возврата z2r_dns_spoof_gate.
type GateCode int

// Коды гейта.
const (
	GateClean   GateCode = 0 // чисто — продолжаем
	GateUnknown GateCode = 1 // неопределимо: эталона нет / системный DNS молчит
	GateSpoofed GateCode = 2 // системный DNS подменён/сломан — стоп
)

// Строковые вердикты гейта.
const (
	GateVerdictClean   = "clean"
	GateVerdictUnknown = "unknown"
	GateVerdictSpoofed = "spoofed"
)

// GateResult — итог проверки подмены DNS.
type GateResult struct {
	Code        GateCode `json:"code"`
	Verdict     string   `json:"verdict"`
	Domain      string   `json:"domain"`
	RefSource   string   `json:"ref_source"`
	RefAddrs    []string `json:"ref_addrs"`
	SystemAddrs []string `json:"system_addrs"`
	SystemError string   `json:"system_error,omitempty"`
	Hits        []string `json:"hits"`
	Message     string   `json:"message"`
}

// Spoofed сообщает, что прогон нужно остановить.
func (r GateResult) Spoofed() bool { return r.Code == GateSpoofed }

// SpoofGate — пред-проверка DNS перед суперавтопрогоном. Сверяется СИСТЕМНЫЙ
// резолвер (им резолвятся все пробы прогона) с эталоном: каскад DoH, а при
// полной недоступности DoH — прямой UDP-запрос к 8.8.8.8.
type SpoofGate struct {
	Domain     string   // по умолчанию rutracker.org
	KnownAddrs []string // известные адреса домена (по умолчанию DefaultRefAddrs)
	DoH        *DoH     // по умолчанию DoH с каскадом по умолчанию
	// SystemLookup резолвит через системный DNS (по умолчанию DefaultLookup без сервера).
	SystemLookup LookupFunc
	// UDPLookup — резервный эталон по UDP (по умолчанию DefaultLookup).
	UDPLookup LookupFunc
	// UDPServer — сервер резервного эталона (по умолчанию 8.8.8.8).
	UDPServer string
}

const gateHelp = " Что можно сделать: сменить DNS на роутере/устройствах (8.8.8.8 / 1.1.1.1 вместо DNS провайдера); " +
	"подобрать стратегию антиспуфинга DNS (профиль 10); после исправления запустить суперавтопрогон заново."

// Check выполняет проверку; сетевые ошибки в Result.Message, а не в error.
func (g *SpoofGate) Check(ctx context.Context) GateResult {
	domain := g.Domain
	if domain == "" {
		domain = DefaultRefDomain
	}
	known := g.KnownAddrs
	if known == nil {
		known = DefaultRefAddrs
	}
	doh := g.DoH
	if doh == nil {
		doh = &DoH{}
	}
	sysLookup := g.SystemLookup
	if sysLookup == nil {
		sysLookup = DefaultLookup
	}
	udp := g.UDPLookup
	if udp == nil {
		udp = DefaultLookup
	}
	udpSrv := g.UDPServer
	if udpSrv == "" {
		udpSrv = DefaultDNSCheckServer
	}

	res := GateResult{Domain: domain}

	// Эталон: DoH, затем UDP 8.8.8.8.
	if ans, err := doh.LookupA(ctx, domain); err == nil {
		res.RefSource, res.RefAddrs = ans.Source, ans.IPs
	} else if addrs, uerr := udp(ctx, domain, udpSrv); uerr == nil {
		if v4, _ := splitAddrs(addrs); len(v4) > 0 {
			res.RefSource, res.RefAddrs = "UDP "+udpSrv+" (DoH недоступны)", uniqSorted(v4)
		}
	}

	// Системный резолвер.
	addrs, serr := sysLookup(ctx, domain, "")
	sys, _ := splitAddrs(addrs)
	res.SystemAddrs = uniqSorted(sys)
	if serr != nil {
		res.SystemError = serr.Error()
	}

	// Совпадение с известными адресами или живым эталоном — чисто.
	for _, a := range res.SystemAddrs {
		if containsStr(known, a) || containsStr(res.RefAddrs, a) {
			res.Hits = append(res.Hits, a)
		}
	}
	if len(res.Hits) > 0 {
		res.Code, res.Verdict = GateClean, GateVerdictClean
		res.Message = "DNS чист: системные ответы совпадают с эталоном/известными адресами. Продолжаем прогон."
		return res
	}

	if serr != nil && isNotFound(serr) {
		res.Code, res.Verdict = GateSpoofed, GateVerdictSpoofed
		res.Message = "СИСТЕМНЫЙ DNS ПОДМЕНЁН/СЛОМАН: отвечает NXDOMAIN на существующий домен. " +
			"Подбор стратегий бессмысленен — все пробы прогона не смогут отрезолвить цели." + gateHelp
		return res
	}
	for _, a := range res.SystemAddrs {
		if a == "127.0.0.1" || a == "0.0.0.0" {
			res.Code, res.Verdict = GateSpoofed, GateVerdictSpoofed
			res.Message = "СИСТЕМНЫЙ DNS ОТВЕЧАЕТ АДРЕСОМ БЛОКИРОВКИ (" + a + ")." + gateHelp
			return res
		}
	}
	if len(res.SystemAddrs) > 0 {
		if len(res.RefAddrs) > 0 {
			res.Code, res.Verdict = GateSpoofed, GateVerdictSpoofed
			res.Message = fmt.Sprintf("СИСТЕМНЫЙ DNS ОТВЕЧАЕТ МИМО ЭТАЛОНА (похоже на заглушку провайдера). Ответ: %s; эталон (%s): %s.%s",
				strings.Join(res.SystemAddrs, " "), res.RefSource, strings.Join(res.RefAddrs, " "), gateHelp)
			return res
		}
		res.Code, res.Verdict = GateUnknown, GateVerdictUnknown
		res.Message = "Не удалось сверить с эталоном (резолверы недоступны), адреса вне известного списка. " +
			"Если это заглушка — результаты прогона будут искажены."
		return res
	}
	res.Code, res.Verdict = GateUnknown, GateVerdictUnknown
	res.Message = "Системный DNS не отвечает. Это не подтверждённая подмена — прогон продолжается, " +
		"но если резолв молчит и в системе, поможет подбор антиспуфа DNS (профиль 10)."
	return res
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

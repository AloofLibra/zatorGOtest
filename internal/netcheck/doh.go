package netcheck

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DoHKind — вариант DoH-API.
type DoHKind int

// Варианты DoH.
const (
	// DoHJSON — JSON API (dns.google/resolve?name=…&type=A).
	DoHJSON DoHKind = iota
	// DoHWire — RFC 8484, application/dns-message (POST).
	DoHWire
)

// DefaultDoHTimeout — таймаут одного DoH-запроса (Z2R_DNS_DOH_TIMEOUT).
const DefaultDoHTimeout = 6 * time.Second

// DoHSource — один DoH-резолвер каскада.
type DoHSource struct {
	Name     string
	URL      string
	Kind     DoHKind
	Insecure bool // не проверять сертификат (доступ по IP: https://8.8.8.8)
}

// DefaultDoHSources — каскад эталона: dns.google -> https://8.8.8.8 (тот же
// JSON API по IP, работает когда имя dns.google не резолвится) -> quad9 wire.
func DefaultDoHSources() []DoHSource {
	return []DoHSource{
		{Name: "Google DoH (dns.google)", URL: "https://dns.google/resolve", Kind: DoHJSON},
		{Name: "Google DoH (8.8.8.8)", URL: "https://8.8.8.8/resolve", Kind: DoHJSON, Insecure: true},
		{Name: "quad9 DoH (dns.quad9.net, wire)", URL: "https://dns.quad9.net/dns-query", Kind: DoHWire},
	}
}

// DoH — клиент каскада DoH-резолверов.
type DoH struct {
	// Sources — каскад (по умолчанию DefaultDoHSources).
	Sources []DoHSource
	// Timeout — таймаут одного запроса (по умолчанию 6 с).
	Timeout time.Duration
	// UserAgent — User-Agent запросов.
	UserAgent string
	// Client — если задан, используется для всех источников (тесты).
	Client *http.Client
}

// DoHAnswer — результат эталонного резолва.
type DoHAnswer struct {
	Source string   // имя источника
	IPs    []string // IPv4, отсортированы, без дублей
}

// ErrDoHUnavailable — ни один DoH-резолвер не дал адресов.
var ErrDoHUnavailable = errors.New("netcheck: DoH-резолверы недоступны")

var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidDomain проверяет имя для DNS-запроса (как фильтр в z2r_doh_a_lookup).
func ValidDomain(domain string) bool {
	if len(domain) == 0 || len(domain) > 253 {
		return false
	}
	for _, l := range strings.Split(domain, ".") {
		if len(l) == 0 || len(l) > 63 || !labelRe.MatchString(l) {
			return false
		}
	}
	return true
}

// LookupA запрашивает A-записи по каскаду; возвращает первый непустой ответ.
func (d *DoH) LookupA(ctx context.Context, domain string) (DoHAnswer, error) {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if !ValidDomain(domain) {
		return DoHAnswer{}, fmt.Errorf("netcheck: некорректное имя %q", domain)
	}
	sources := d.Sources
	if len(sources) == 0 {
		sources = DefaultDoHSources()
	}
	for _, s := range sources {
		if ctx.Err() != nil {
			break
		}
		var ips []string
		var err error
		switch s.Kind {
		case DoHWire:
			ips, err = d.lookupWire(ctx, s, domain)
		default:
			ips, err = d.lookupJSON(ctx, s, domain)
		}
		if err == nil && len(ips) > 0 {
			return DoHAnswer{Source: s.Name, IPs: ips}, nil
		}
	}
	return DoHAnswer{}, ErrDoHUnavailable
}

func (d *DoH) httpClient(s DoHSource) *http.Client {
	if d.Client != nil {
		return d.Client
	}
	tr := &http.Transport{
		Proxy:              nil,
		TLSClientConfig:    &tls.Config{InsecureSkipVerify: s.Insecure, MinVersion: tls.VersionTLS12}, //nolint:gosec // доступ по IP.
		ForceAttemptHTTP2:  true,
		DisableKeepAlives:  true,
		DisableCompression: true,
		DialContext:        (&net.Dialer{Timeout: DefaultConnectTimeout}).DialContext,
	}
	return &http.Client{Transport: tr}
}

func (d *DoH) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return DefaultDoHTimeout
}

func (d *DoH) ua() string {
	if d.UserAgent != "" {
		return d.UserAgent
	}
	return DefaultUserAgent
}

func (d *DoH) do(ctx context.Context, s DoHSource, req *http.Request) ([]byte, error) {
	cl := d.httpClient(s)
	if t, ok := cl.Transport.(*http.Transport); ok && d.Client == nil {
		defer t.CloseIdleConnections()
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("netcheck: DoH %s: HTTP %d", s.Name, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<10))
}

type dohJSON struct {
	Status int `json:"Status"`
	Answer []struct {
		Type int    `json:"type"`
		Data string `json:"data"`
	} `json:"Answer"`
}

func (d *DoH) lookupJSON(ctx context.Context, s DoHSource, domain string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	u, err := url.Parse(s.URL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("name", domain)
	q.Set("type", "A")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", d.ua())
	req.Header.Set("Accept", "application/dns-json")
	body, err := d.do(ctx, s, req)
	if err != nil {
		return nil, err
	}
	var r dohJSON
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	var ips []string
	for _, a := range r.Answer {
		if a.Type == 1 && isIPv4(a.Data) {
			ips = append(ips, a.Data)
		}
	}
	return uniqSorted(ips), nil
}

func (d *DoH) lookupWire(ctx context.Context, s DoHSource, domain string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	q, err := BuildDNSQuery(domain, 0)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(q))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", d.ua())
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	body, err := d.do(ctx, s, req)
	if err != nil {
		return nil, err
	}
	ips, _, err := ParseDNSAnswerA(body)
	if err != nil {
		return nil, err
	}
	return uniqSorted(ips), nil
}

// BuildDNSQuery собирает DNS-запрос типа A класса IN (RFC 1035) с заданным
// ID (для RFC 8484 рекомендуется 0).
func BuildDNSQuery(domain string, id uint16) ([]byte, error) {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	if !ValidDomain(domain) {
		return nil, fmt.Errorf("netcheck: некорректное имя %q", domain)
	}
	msg := make([]byte, 12, 12+len(domain)+6)
	binary.BigEndian.PutUint16(msg[0:], id)
	binary.BigEndian.PutUint16(msg[2:], 0x0100) // RD
	binary.BigEndian.PutUint16(msg[4:], 1)      // QDCOUNT
	for _, l := range strings.Split(domain, ".") {
		msg = append(msg, byte(len(l)))
		msg = append(msg, l...)
	}
	msg = append(msg, 0, 0, 1, 0, 1) // root, QTYPE=A, QCLASS=IN
	return msg, nil
}

var errShortDNS = errors.New("netcheck: усечённое DNS-сообщение")

// skipName пропускает закодированное имя (с учётом сжатия) и возвращает смещение за ним.
func skipName(msg []byte, off int) (int, error) {
	for {
		if off >= len(msg) {
			return 0, errShortDNS
		}
		c := int(msg[off])
		switch {
		case c == 0:
			return off + 1, nil
		case c&0xC0 == 0xC0:
			if off+2 > len(msg) {
				return 0, errShortDNS
			}
			return off + 2, nil
		case c&0xC0 != 0:
			return 0, errors.New("netcheck: некорректная метка DNS")
		}
		off += 1 + c
	}
}

// ParseDNSAnswerA разбирает DNS-ответ и возвращает IPv4 из A-записей секции
// ответов, а также RCODE.
func ParseDNSAnswerA(msg []byte) (ips []string, rcode int, err error) {
	if len(msg) < 12 {
		return nil, 0, errShortDNS
	}
	rcode = int(msg[3] & 0x0F)
	qd := int(binary.BigEndian.Uint16(msg[4:]))
	an := int(binary.BigEndian.Uint16(msg[6:]))
	off := 12
	for i := 0; i < qd; i++ {
		if off, err = skipName(msg, off); err != nil {
			return nil, rcode, err
		}
		off += 4
		if off > len(msg) {
			return nil, rcode, errShortDNS
		}
	}
	for i := 0; i < an; i++ {
		if off, err = skipName(msg, off); err != nil {
			return ips, rcode, err
		}
		if off+10 > len(msg) {
			return ips, rcode, errShortDNS
		}
		typ := binary.BigEndian.Uint16(msg[off:])
		class := binary.BigEndian.Uint16(msg[off+2:])
		rdlen := int(binary.BigEndian.Uint16(msg[off+8:]))
		off += 10
		if off+rdlen > len(msg) {
			return ips, rcode, errShortDNS
		}
		if typ == 1 && class == 1 && rdlen == 4 {
			ips = append(ips, net.IP(msg[off:off+4]).String())
		}
		off += rdlen
	}
	return ips, rcode, nil
}

func isIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil && !strings.Contains(s, ":")
}

func uniqSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

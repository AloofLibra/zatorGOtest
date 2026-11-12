package netcheck

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Адреса и метки целей из api_check/profile_check_json.
const (
	LabelYouTube      = "YouTube"
	LabelGooglevideo  = "Googlevideo"
	LabelBlockedSites = "Blocked Sites"
	LabelInstagram    = "Instagram"
	LabelDiscord      = "Discord"
	LabelDNS          = "DNS антиспуф"

	URLYouTube   = "https://www.youtube.com/"
	URLMeduza    = "https://meduza.io"
	URLInstagram = "https://www.instagram.com/"
	URLDiscord   = "https://discord.com/"

	// ClusterRedirectorURL — редиректор, раскрывающий кодовое имя кластера googlevideo.
	ClusterRedirectorURL = "https://redirector.xn--ngstr-lra8j.com/report_mapping?di=no"
	// FallbackClusterDomain — запасной узел googlevideo.
	FallbackClusterDomain = "rr2---sn-4g5ednly.googlevideo.com"

	// MessageUDPProfile — пояснение для UDP-профилей 5/6.
	MessageUDPProfile = "Для UDP-профиля быстрая TLS-проверка неприменима. Проверьте работу в браузере или приложении."
)

// Target — цель проверки.
type Target struct {
	Label string
	URL   string
}

// DefaultOptions — настройки как у check.cgi: без проверки сертификата (в CGI
// нет CA-бандла), версии TLS ждутся не обязательно.
func DefaultOptions() Options {
	return Options{InsecureSkipVerify: true}
}

// Checker запускает проверки целей. Нулевое значение пригодно к работе
// (проверка сертификата включена; для DPI-тестов используйте DefaultOptions).
type Checker struct {
	Options Options

	// RedirectorURL подменяет ClusterRedirectorURL (тесты).
	RedirectorURL string
	// DNS — настройки DNS-серий для профиля 10.
	DNS DNSSeriesConfig
}

// NewChecker создаёт Checker с заданными опциями.
func NewChecker(opts Options) *Checker { return &Checker{Options: opts} }

// CheckTarget проверяет одну цель (z2r_tls_check_target + вердикт):
// параллельные пробы TLS 1.2/1.3, при 2xx/3xx — докачка до 64 КБ с одной
// повторной попыткой.
func (c *Checker) CheckTarget(ctx context.Context, label, rawURL string) TargetResult {
	opts := c.Options.withDefaults()
	v12, v13 := probeBoth(ctx, rawURL, opts)

	var dl *Download
	if v12.CodeOK() || v13.CodeOK() {
		first := ProbeDownload(ctx, rawURL, opts)
		dl = &first
		if first.State != DLOK && ctx.Err() == nil {
			second := ProbeDownload(ctx, rawURL, opts)
			if second.State == DLOK {
				second.Retry = true
				second.Text = describeDownload(second, opts)
				dl = &second
			}
		}
	}
	return buildResult(label, rawURL, v12, v13, dl)
}

func buildResult(label, target string, v12, v13 VersionDetail, dl *Download) TargetResult {
	verdict, text := Verdict(v12, v13, dl)
	return TargetResult{
		Label:       label,
		Target:      target,
		TLS12:       b2i(v12.Answered()),
		TLS13:       b2i(v13.Answered()),
		Verdict:     verdict,
		Text:        text,
		TLS12Detail: v12,
		TLS13Detail: v13,
		Download:    dl,
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// probeBoth запускает пробы TLS 1.2 и 1.3 параллельно (1.3 — после ProbeGap).
// Без WaitBoth вторая проба добивается, как только первая получила 2xx/3xx,
// и помечается aborted.
func probeBoth(ctx context.Context, rawURL string, opts Options) (v12, v13 VersionDetail) {
	type out struct {
		ver TLSVersion
		d   VersionDetail
	}
	ctx12, cancel12 := context.WithCancel(ctx)
	ctx13, cancel13 := context.WithCancel(ctx)
	defer cancel12()
	defer cancel13()

	ch := make(chan out, 2)
	go func() { ch <- out{TLS12, ProbeVersion(ctx12, rawURL, TLS12, opts)} }()
	go func() {
		if opts.ProbeGap > 0 {
			t := time.NewTimer(opts.ProbeGap)
			select {
			case <-t.C:
			case <-ctx13.Done():
				t.Stop()
			}
		}
		ch <- out{TLS13, ProbeVersion(ctx13, rawURL, TLS13, opts)}
	}()

	for i := 0; i < 2; i++ {
		r := <-ch
		if r.ver == TLS12 {
			v12 = r.d
		} else {
			v13 = r.d
		}
		if i == 0 && !opts.WaitBoth && r.d.CodeOK() {
			if r.ver == TLS12 {
				cancel13()
			} else {
				cancel12()
			}
		}
	}
	return v12, v13
}

// CheckTargets проверяет цели последовательно, по одной (против rate-эвристик).
func (c *Checker) CheckTargets(ctx context.Context, targets []Target) []TargetResult {
	out := make([]TargetResult, 0, len(targets))
	for _, t := range targets {
		out = append(out, c.CheckTarget(ctx, t.Label, t.URL))
	}
	return out
}

// DefaultTargets — четыре цели полной проверки api_check: YouTube,
// Googlevideo (реальный кластер), Blocked Sites, Instagram.
func (c *Checker) DefaultTargets(ctx context.Context) []Target {
	return []Target{
		{LabelYouTube, URLYouTube},
		{LabelGooglevideo, "https://" + c.ClusterDomain(ctx)},
		{LabelBlockedSites, URLMeduza},
		{LabelInstagram, URLInstagram},
	}
}

// Check — полная проверка api_check без параметра profile.
func (c *Checker) Check(ctx context.Context) CheckResponse {
	return CheckResponse{Results: c.CheckTargets(ctx, c.DefaultTargets(ctx))}
}

// ProfileTarget возвращает цель быстрой проверки профиля (profile_check_json).
// ok=false — для профиля TLS-проверка не определена.
func (c *Checker) ProfileTarget(ctx context.Context, profile int) (Target, bool) {
	switch profile {
	case 1:
		return Target{LabelYouTube, URLYouTube}, true
	case 2:
		return Target{LabelGooglevideo, "https://" + c.ClusterDomain(ctx)}, true
	case 3:
		return Target{LabelBlockedSites, URLMeduza}, true
	case 4:
		return Target{LabelDiscord, URLDiscord}, true
	}
	return Target{}, false
}

// CheckProfile — check.cgi?profile=N: проверка одной цели профиля.
// Профили 5/6 — пустой список и пояснение; 10 — серия DNS-проб антиспуфа;
// прочие — пустой список.
func (c *Checker) CheckProfile(ctx context.Context, profile int) CheckResponse {
	switch profile {
	case 5, 6:
		return CheckResponse{Results: []TargetResult{}, Message: MessageUDPProfile}
	case 10:
		return CheckResponse{Results: []TargetResult{c.CheckDNSAntispoof(ctx)}}
	}
	t, ok := c.ProfileTarget(ctx, profile)
	if !ok {
		return CheckResponse{Results: []TargetResult{}}
	}
	return CheckResponse{Results: []TargetResult{c.CheckTarget(ctx, t.Label, t.URL)}}
}

// --- YouTube / googlevideo ---

const (
	clusterMapA = "uzpkfa50vqlgb61wrmhc72xsnid83ytoje94-"
	clusterMapB = "0123456789abcdefghijklmnopqrstuvwxyz-"
)

var (
	clusterArrowRe    = regexp.MustCompile(`=>\s*([^ (:)]*)`)
	clusterCodenameRe = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
)

// ConvertClusterCodename переводит кодовое имя кластера (tr 'uzpk…' '0123…')
// в суффикс узла rr1---sn-<имя>.googlevideo.com.
func ConvertClusterCodename(codename string) string {
	m := make(map[rune]rune, len(clusterMapA))
	b := []rune(clusterMapB)
	for i, r := range clusterMapA {
		m[r] = b[i]
	}
	out := make([]rune, 0, len(codename))
	for _, r := range codename {
		if t, ok := m[r]; ok {
			out = append(out, t)
		} else {
			out = append(out, r)
		}
	}
	return "rr1---sn-" + string(out) + ".googlevideo.com"
}

// parseClusterCodename извлекает кодовое имя из ответа редиректора
// (первая строка вида "... => <name> ...").
func parseClusterCodename(body string) string {
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		idx := strings.LastIndex(line, "=>")
		if idx < 0 {
			continue
		}
		m := clusterArrowRe.FindStringSubmatch(line[idx:])
		if m != nil && m[1] != "" && clusterCodenameRe.MatchString(m[1]) {
			return m[1]
		}
	}
	return ""
}

// ClusterDomain — get_yt_cluster_domain: узел кластера googlevideo пользователя.
// Две попытки (4 с каждая); при неудаче возвращается FallbackClusterDomain.
func (c *Checker) ClusterDomain(ctx context.Context) string {
	url := c.RedirectorURL
	if url == "" {
		url = ClusterRedirectorURL
	}
	opts := c.Options.withDefaults()
	opts.InsecureSkipVerify = true // curl -k
	for i := 0; i < 2 && ctx.Err() == nil; i++ {
		if code := fetchCodename(ctx, url, opts); code != "" {
			return ConvertClusterCodename(code)
		}
	}
	return FallbackClusterDomain
}

func fetchCodename(ctx context.Context, url string, opts Options) string {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	st := &connState{}
	tr := opts.transport(tls.VersionTLS12, tls.VersionTLS13, st)
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", opts.UserAgent)
	resp, err := opts.client(tr).Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil && len(body) == 0 {
		return ""
	}
	return parseClusterCodename(string(body))
}

// CheckYouTube — проверка https://www.youtube.com/.
func (c *Checker) CheckYouTube(ctx context.Context) TargetResult {
	return c.CheckTarget(ctx, LabelYouTube, URLYouTube)
}

// CheckGooglevideo — TLS/CDN-проверка реального кластера googlevideo.
// Реальные медиаданные не проверяются (см. check_googlevideo_access):
// корневой путь googlevideo обычно отдаёт 404 — это норма (verdict ok).
func (c *Checker) CheckGooglevideo(ctx context.Context) TargetResult {
	return c.CheckTarget(ctx, LabelGooglevideo, "https://"+c.ClusterDomain(ctx))
}

// CheckAccessList — порт check_access_list: DNS-гейт-подобная проверка не
// выполняется (см. SpoofGate), последовательно проверяются YouTube,
// Googlevideo, Blocked Sites и Instagram.
func (c *Checker) CheckAccessList(ctx context.Context) []TargetResult {
	return c.CheckTargets(ctx, c.DefaultTargets(ctx))
}

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
	// MessageNoTLSProfile — профили без привязанной HTTPS-цели.
	MessageNoTLSProfile = "У этого профиля нет привязанной HTTPS-цели: быстрый TLS-тест недоступен. Лок применяется, проверку делайте вручную или SuperSweep."
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
	// MediaResolver obtains short-lived audio/video URLs for profile 2.
	// Nil uses the built-in Innertube resolver.
	MediaResolver MediaResolver

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

// CheckTargetReliable — CheckTarget + один повтор при timeout/conn/tls fail.
func (c *Checker) CheckTargetReliable(ctx context.Context, label, rawURL string) (TargetResult, bool) {
	first := c.CheckTarget(ctx, label, rawURL)
	if first.Verdict != VerdictFail || ctx.Err() != nil {
		return first, false
	}
	if !transientFail(first) {
		return first, false
	}
	t := time.NewTimer(450 * time.Millisecond)
	select {
	case <-ctx.Done():
		t.Stop()
		return first, false
	case <-t.C:
	}
	second := c.CheckTarget(ctx, label, rawURL)
	if second.Verdict == VerdictFail {
		return second, true
	}
	second.Text = second.Text + " (со 2-й попытки)"
	return second, true
}

func transientFail(r TargetResult) bool {
	for _, st := range []string{r.TLS12Detail.State, r.TLS13Detail.State} {
		switch st {
		case StateTimeout, StateConn, StateTLS, StateNone:
			return true
		}
	}
	return false
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
func (c *Checker) CheckProfile(ctx context.Context, profile int) CheckResponse {
	return c.CheckProfileOpts(ctx, profile, ProfileCheckOptions{})
}

// CheckProfileOpts — CheckProfile с опциями (media_url для профиля 2).
// Профили 5/6 — пустой список; 10 — DNS-антиспуф. Profile 2: media-классификация
// + при fail контроль YouTube.
func (c *Checker) CheckProfileOpts(ctx context.Context, profile int, opts ProfileCheckOptions) CheckResponse {
	switch profile {
	case 5, 6:
		return CheckResponse{Results: []TargetResult{}, Message: MessageUDPProfile}
	case 10:
		return CheckResponse{Results: []TargetResult{c.CheckDNSAntispoof(ctx)}}
	}
	if profile == 2 {
		return c.checkProfileGooglevideo(ctx, opts)
	}
	t, ok := c.ProfileTarget(ctx, profile)
	if !ok {
		return CheckResponse{Results: []TargetResult{}, Message: MessageNoTLSProfile}
	}
	primary, retried := c.CheckTargetReliable(ctx, t.Label, t.URL)
	return CheckResponse{Results: []TargetResult{primary}, Retried: retried}
}

func (c *Checker) checkProfileGooglevideo(ctx context.Context, opts ProfileCheckOptions) CheckResponse {
	primary := c.CheckGooglevideoMediaWithOptions(ctx, opts)
	resp := CheckResponse{Results: []TargetResult{primary}}
	if primary.Media != nil {
		switch primary.Media.Class {
		case MediaMidstream:
			resp.Message = "Один из крупных диапазонов медиапотока оборван — проверьте WSSIZE Lite и актуальность googlevideo-ipv4"
		case MediaTiny:
			resp.Message = "Медиадиапазоны tiny — ротируйте стратегию профиля 2 (не WSSIZE)"
		case MediaPlay:
			resp.Message = "Media Range OK; если в браузере рвётся — смотрите QUIC/h3 (UDP) или клиент"
		case MediaWarn:
			resp.Message = primary.Media.Text
		case MediaTLS:
			resp.Message = "Проверен только TLS CDN; медиапоток не проверен"
		case MediaUnknown:
			if primary.TLS12Detail.Answered() || primary.TLS13Detail.Answered() {
				resp.Message = "TLS CDN отвечает, но медиапоток не подтверждён: " + primary.Media.ResolverStatus
			} else {
				resp.Message = "TLS к Googlevideo не подтверждён; медиапоток неизвестен: " + primary.Media.ResolverStatus
			}
		}
	}
	if primary.Verdict == VerdictFail && ctx.Err() == nil {
		ctrl, r2 := c.CheckTargetReliable(ctx, LabelYouTube+" (контроль)", URLYouTube)
		resp.Retried = r2
		resp.Results = append(resp.Results, ctrl)
		switch ctrl.Verdict {
		case VerdictOK, VerdictWarn:
			if resp.Message == "" {
				resp.Message = "Googlevideo недоступен, YouTube отвечает — сбой на цели профиля 2 (не общий обрыв сети)"
			}
		default:
			resp.Message = "Недоступны Googlevideo и YouTube — общий сбой сети/DNS/DPI, не только стратегия профиля 2"
		}
	}
	return resp
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

// CheckGooglevideo resolves and checks a real YouTube media stream.
func (c *Checker) CheckGooglevideo(ctx context.Context) TargetResult {
	return c.CheckGooglevideoMedia(ctx, "")
}

// CheckGooglevideoMedia checks a supplied signed videoplayback URL. If empty,
// it resolves media URLs through Innertube.
func (c *Checker) CheckGooglevideoMedia(ctx context.Context, mediaURL string) TargetResult {
	return c.CheckGooglevideoMediaWithOptions(ctx, ProfileCheckOptions{MediaURL: mediaURL})
}

// CheckGooglevideoMediaURLs checks one Innertube-resolved audio/video pair over
// both TLS versions. The caller resolves URLs before starting an isolated
// transport; no unsigned CDN preflight is sent through the candidate strategy.
func (c *Checker) CheckGooglevideoMediaURLs(ctx context.Context, targetURL string, media YouTubeMedia) TargetResult {
	return c.CheckGooglevideoMediaURLsWithTLS(ctx, targetURL, media, PrefBoth)
}

func (c *Checker) CheckGooglevideoMediaURLsWithTLS(ctx context.Context, targetURL string, media YouTubeMedia, pref TLSPref) TargetResult {
	audioHost, audioErr := validateYouTubeMediaURL(media.AudioURL)
	videoHost, videoErr := validateYouTubeMediaURL(media.VideoURL)
	if audioErr != nil || videoErr != nil {
		return unknownMediaTarget(targetURL, media.VideoID, "invalid_media_url")
	}
	pref = ParseTLSPref(string(pref))
	run12, run13 := pref != Pref13, pref != Pref12
	a12, v12, m12 := Download{}, Download{}, Download{}
	if run12 {
		a12, v12, m12 = probeResolvedMediaVersion(ctx, media, c.Options, TLS12)
	}
	a13, v13, m13 := Download{}, Download{}, Download{}
	if run13 && ctx.Err() == nil {
		a13, v13, m13 = probeResolvedMediaVersion(ctx, media, c.Options, TLS13)
	}
	r := TargetResult{Label: LabelGooglevideo, Target: "https://" + videoHost + "/videoplayback"}
	r.TLS12Detail = VersionDetail{State: StateAborted, Time: "-", IP: "-", Proto: "-", Text: "TLS 1.2 was not requested"}
	r.TLS13Detail = VersionDetail{State: StateAborted, Time: "-", IP: "-", Proto: "-", Text: "TLS 1.3 was not requested"}
	if run12 {
		r.TLS12Detail = mediaVersionDetail(TLS12, a12, v12, m12)
	}
	if run13 {
		r.TLS13Detail = mediaVersionDetail(TLS13, a13, v13, m13)
	}
	probe12 := classifyResolvedMedia(media, a12, v12, m12)
	probe13 := classifyResolvedMedia(media, a13, v13, m13)
	var result MediaProbe
	var selected Download
	switch {
	case run12 && !run13:
		result, selected = probe12, m12
	case run13 && !run12:
		result, selected = probe13, m13
	case probe12.Class == MediaPlay && probe13.Class == MediaPlay:
		result, selected = probe13, m13
	case probe12.Class == MediaPlay:
		result, selected = probe12, m12
		result.Class = MediaWarn
		result.Text = "Медиапоток проходит TLS 1.2, но не TLS 1.3"
	case probe13.Class == MediaPlay:
		result, selected = probe13, m13
		result.Class = MediaWarn
		result.Text = "Медиапоток проходит TLS 1.3, но не TLS 1.2"
	case probe12.Class == MediaMidstream:
		result, selected = probe12, m12
	case probe13.Class == MediaMidstream:
		result, selected = probe13, m13
	default:
		if run13 {
			result, selected = probe13, m13
		} else {
			result, selected = probe12, m12
		}
		if staleResolvedMedia(a12, v12, m12, a13, v13, m13) {
			result.ResolverStatus = "signed_url_expired"
		} else {
			result.ResolverStatus = "media_range_unconfirmed"
		}
		result.Text = "Медиапоток не подтверждён: " + result.ResolverStatus
	}
	if result.Class == MediaUnknown && result.ResolverStatus == "" {
		if staleResolvedMedia(a12, v12, m12, a13, v13, m13) {
			result.ResolverStatus = "signed_url_expired"
		} else {
			result.ResolverStatus = "media_range_unconfirmed"
		}
		result.Text = "Медиапоток не подтверждён: " + result.ResolverStatus
	}
	requestedDetails := make([]VersionDetail, 0, 2)
	if run12 {
		requestedDetails = append(requestedDetails, r.TLS12Detail)
	}
	if run13 && ctx.Err() == nil {
		requestedDetails = append(requestedDetails, r.TLS13Detail)
	}
	if result.Class == MediaUnknown && result.ResolverStatus == "media_range_unconfirmed" && allMediaVersionsHaveNetworkFailure(requestedDetails...) {
		result.Class = MediaFail
		result.ResolverStatus = "media_network_error"
		result.Text = "Ошибка соединения с медиасервером до HTTP-ответа; это не подтверждает причину сбоя"
	}
	if ctx.Err() != nil {
		result.Class = MediaUnknown
		result.ResolverStatus = "cancelled"
		result.Text = "Медиапоток не подтверждён: cancelled"
	}
	result.Host, result.AudioHost, result.VideoID = videoHost, audioHost, media.VideoID
	r.Download = &selected
	applyMediaToResult(&r, result)
	return r
}

func unknownMediaTarget(targetURL, videoID, reason string) TargetResult {
	media := MediaProbe{Class: MediaUnknown, Text: "Медиапоток не подтверждён: " + reason, VideoID: videoID, ResolverStatus: reason}
	return TargetResult{Label: LabelGooglevideo, Target: targetURL, Verdict: VerdictUnknown, Text: media.Text, Media: &media}
}

// CheckGooglevideoMediaWithOptions checks real audio/video ranges. If no signed
// URL was supplied it resolves both streams via Innertube first.
func (c *Checker) CheckGooglevideoMediaWithOptions(ctx context.Context, options ProfileCheckOptions) TargetResult {
	host := c.ClusterDomain(ctx)
	rawURL := "https://" + host
	r := c.CheckTarget(ctx, LabelGooglevideo, rawURL)

	dnsFail := r.TLS12Detail.State == StateDNS || r.TLS13Detail.State == StateDNS
	tlsOK := r.TLS12Detail.Answered() || r.TLS13Detail.Answered()

	mediaURL := strings.TrimSpace(options.MediaURL)
	hasURL := looksLikeGooglevideoMediaURL(mediaURL)
	var d16, d64 *Download
	var media YouTubeMedia
	var resolvedProbe *MediaProbe
	resolverStatus := ""
	audioBytes := 0
	mediaHTTP := tlsOK
	if mediaURL != "" && !hasURL {
		resolverStatus = "invalid_media_url"
	}
	if hasURL && ctx.Err() == nil {
		a, b := ProbeMediaRanges(ctx, mediaURL, c.Options)
		d16, d64 = &a, &b
		mediaHTTP = mediaHTTP || a.Code != 0 || b.Code != 0
		// Показываем 64K-проб как download в результате (удобнее в UI).
		r.Download = d64
		media.VideoURL = mediaURL
	} else if mediaURL == "" && ctx.Err() == nil {
		videoID := strings.TrimSpace(options.VideoID)
		if videoID == "" {
			videoID = DefaultYouTubeVideoID
		}
		resolver := c.MediaResolver
		if resolver == nil {
			resolver = func(ctx context.Context, id string) (YouTubeMedia, error) {
				return ResolveYouTubeMedia(ctx, id, c.Options)
			}
		}
		resolved, err := resolver(ctx, videoID)
		if err != nil {
			resolverStatus = err.Error()
		} else {
			media = resolved
			audio, videoStart, videoMiddle := probeResolvedMedia(ctx, resolved, c.Options)
			mediaHTTP = mediaHTTP || audio.Code != 0 || videoStart.Code != 0 || videoMiddle.Code != 0
			if staleResolvedMedia(audio, videoStart, videoMiddle) && ctx.Err() == nil {
				refreshed, refreshErr := resolver(ctx, videoID)
				if refreshErr != nil {
					resolverStatus = "signed_url_refresh_failed:" + refreshErr.Error()
				} else {
					media = refreshed
					audio, videoStart, videoMiddle = probeResolvedMedia(ctx, refreshed, c.Options)
					mediaHTTP = mediaHTTP || audio.Code != 0 || videoStart.Code != 0 || videoMiddle.Code != 0
				}
			}
			audioBytes = audio.Size
			probe := classifyResolvedMedia(media, audio, videoStart, videoMiddle)
			resolvedProbe = &probe
			if resolverStatus == "" && staleResolvedMedia(audio, videoStart, videoMiddle) {
				resolverStatus = "signed_url_expired"
			} else if resolverStatus == "" && probe.Class != MediaPlay && probe.Class != MediaMidstream {
				resolverStatus = "media_range_unconfirmed"
			}
			if ctx.Err() != nil {
				resolverStatus = "cancelled"
			}
			if ctx.Err() == nil {
				d16, d64 = &videoStart, &videoMiddle
				r.Download = d64
			}
		}
	}
	result := ClassifyMediaRange(dnsFail && !mediaHTTP, mediaHTTP, hasURL || media.VideoURL != "", d16, d64)
	if resolvedProbe != nil {
		result = *resolvedProbe
	}
	result.Host = host
	forceUnknown := strings.HasPrefix(resolverStatus, "signed_url") || resolverStatus == "invalid_media_url"
	if resolverStatus != "" && (forceUnknown || result.Class != MediaMidstream && result.Class != MediaTiny) {
		result.Class = MediaUnknown
		result.Text = "Медиапоток не проверен: " + resolverStatus
	}
	if resolverStatus != "" {
		result.ResolverStatus = resolverStatus
	}
	if media.VideoID != "" {
		result.VideoID = media.VideoID
	}
	if media.AudioHost != "" {
		result.AudioHost = media.AudioHost
	}
	result.AudioBytes = audioBytes
	if result.Class == MediaUnknown && result.ResolverStatus == "" {
		result.ResolverStatus = "media_not_checked"
	}
	applyMediaToResult(&r, result)
	return r
}

// CheckAccessList — порт check_access_list: DNS-гейт-подобная проверка не
// выполняется (см. SpoofGate), последовательно проверяются YouTube,
// Googlevideo, Blocked Sites и Instagram.
func (c *Checker) CheckAccessList(ctx context.Context) []TargetResult {
	return c.CheckTargets(ctx, c.DefaultTargets(ctx))
}

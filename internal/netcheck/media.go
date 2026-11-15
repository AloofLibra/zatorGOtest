package netcheck

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Классы media-пробы googlevideo / videoplayback (Package B).
const (
	MediaNone      = "none"      // нет данных / TLS не прошёл
	MediaDNS       = "dns"       // NXDOMAIN / DNS fail
	MediaTLS       = "tls"       // TLS к CDN OK, media URL не проверялся
	MediaUnknown   = "unknown"   // Innertube или media URL недоступны; медиа не подтверждено
	MediaFail      = "fail"      // медиа transport failure до HTTP на выбранных TLS версиях
	MediaWarn      = "warn"      // только одна версия TLS отдала все диапазоны
	MediaTiny      = "tiny"      // после TLS пришло ≪ 16 КБ (tiny_after_tls)
	MediaMidstream = "midstream" // ~16 КБ ок, 64 КБ срезан
	MediaPlay      = "play"      // ≥64 КБ ушли
	MediaQUIC      = "quic_hint" // подсказка: смотри UDP/h3
)

// Пороги Range-проб (как в test_googlevideo.com.md).
const (
	MediaRange16K         = 16384
	MediaRange64K         = 65536
	MediaTinyMax          = 4096
	MediaAudioRange       = 256 << 10
	MediaVideoRange       = 1 << 20
	MediaVideoMiddleRange = 2 << 20
	maxMediaRange         = 4 << 20
)

// MediaProbe — классификация обрыва видеопотока (поле media в TargetResult).
type MediaProbe struct {
	Class            string `json:"class"`
	Text             string `json:"text"`
	Size16K          int    `json:"size_16k,omitempty"`
	Size64K          int    `json:"size_64k,omitempty"`
	Host             string `json:"host,omitempty"`
	VideoID          string `json:"video_id,omitempty"`
	AudioHost        string `json:"audio_host,omitempty"`
	AudioBytes       int    `json:"audio_bytes,omitempty"`
	VideoRangeBytes  int    `json:"video_range_bytes,omitempty"`
	VideoMiddleBytes int    `json:"video_middle_bytes,omitempty"`
	ResolverStatus   string `json:"resolver_status,omitempty"`
}

// ProbeMediaSegment downloads one bounded signed media range and validates
// status, Content-Range, MIME type, and exact body length. Redirects are limited
// to two HTTPS signed Googlevideo URLs so credentials never reach other hosts.
func ProbeMediaSegment(ctx context.Context, rawURL string, opts Options, offset, requested, total int64, expectedMIME string) Download {
	return probeMediaSegment(ctx, rawURL, opts, offset, requested, total, expectedMIME, 0)
}

func probeMediaSegmentVersion(ctx context.Context, rawURL string, opts Options, offset, requested, total int64, expectedMIME string, version TLSVersion) Download {
	return probeMediaSegment(ctx, rawURL, opts, offset, requested, total, expectedMIME, version)
}

func probeMediaSegment(ctx context.Context, rawURL string, opts Options, offset, requested, total int64, expectedMIME string, version TLSVersion) Download {
	opts = opts.withDefaults()
	out := Download{State: DLFail, Time: "-", Text: "invalid media range"}
	_, err := validateYouTubeMediaURL(rawURL)
	if err != nil || requested < MediaTinyMax || requested > maxMediaRange || total < MediaTinyMax*4 || offset < 0 {
		return out
	}
	count := requested
	if count > total/4 {
		count = total / 4
	}
	if count < MediaTinyMax || offset+count > total {
		return out
	}
	end := offset + count - 1
	current, _ := url.Parse(rawURL)
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, opts.DownloadMaxTime)
	defer cancel()
	for redirects := 0; redirects <= 2; redirects++ {
		if _, err := validateYouTubeMediaURL(current.String()); err != nil {
			return out
		}
		st := &connState{}
		minTLS, maxTLS := uint16(tls.VersionTLS12), uint16(tls.VersionTLS13)
		if version != 0 {
			var ok bool
			minTLS, maxTLS, ok = tlsBounds(version)
			if !ok {
				return out
			}
		}
		tr := opts.transport(minTLS, maxTLS, st)
		client := opts.client(tr)
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			tr.CloseIdleConnections()
			return out
		}
		req.Header.Set("User-Agent", opts.UserAgent)
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))
		resp, err := client.Do(req)
		if err != nil {
			tr.CloseIdleConnections()
			out.Time = formatSeconds(time.Since(start))
			out.Err = classify(err, st.connected.Load())
			out.IP = st.remoteIP()
			out.Text = "media range request failed"
			return out
		}
		if resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusPermanentRedirect {
			loc, parseErr := current.Parse(resp.Header.Get("Location"))
			resp.Body.Close()
			tr.CloseIdleConnections()
			if parseErr != nil || redirects == 2 {
				out.Code = resp.StatusCode
				out.Text = "unsafe or excessive media redirect"
				return out
			}
			current = loc
			continue
		}
		out.Code = resp.StatusCode
		out.Proto = resp.Proto
		out.IP = st.remoteIP()
		wantRange := fmt.Sprintf("bytes %d-%d/%d", offset, end, total)
		gotMIME := strings.ToLower(strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0]))
		valid := resp.StatusCode == http.StatusPartialContent && resp.Header.Get("Content-Range") == wantRange && (expectedMIME == "" || gotMIME == strings.ToLower(expectedMIME)) && (resp.ContentLength < 0 || resp.ContentLength == count)
		if valid {
			n, readErr := io.CopyN(io.Discard, resp.Body, count)
			out.Size = int(n)
			if readErr == nil && n == count {
				out.State = DLOK
				out.Text = "validated media range"
			} else {
				out.State = DLCut
				out.Text = "incomplete media range"
			}
		} else {
			out.Text = "media range headers did not match"
		}
		resp.Body.Close()
		tr.CloseIdleConnections()
		out.Time = formatSeconds(time.Since(start))
		return out
	}
	return out
}

func probeResolvedMedia(ctx context.Context, media YouTubeMedia, opts Options) (audio, videoStart, videoMiddle Download) {
	audio = ProbeMediaSegment(ctx, media.AudioURL, opts, 0, MediaAudioRange, media.AudioLength, media.AudioMIME)
	if ctx.Err() == nil {
		videoStart = ProbeMediaSegment(ctx, media.VideoURL, opts, 0, MediaVideoRange, media.VideoLength, media.VideoMIME)
	}
	if ctx.Err() == nil {
		videoMiddle = ProbeMediaSegment(ctx, media.VideoURL, opts, media.VideoLength/2, MediaVideoMiddleRange, media.VideoLength, media.VideoMIME)
	}
	return audio, videoStart, videoMiddle
}

func probeResolvedMediaVersion(ctx context.Context, media YouTubeMedia, opts Options, version TLSVersion) (audio, videoStart, videoMiddle Download) {
	audio = probeMediaSegmentVersion(ctx, media.AudioURL, opts, 0, MediaAudioRange, media.AudioLength, media.AudioMIME, version)
	if ctx.Err() == nil && audio.State == DLOK {
		videoStart = probeMediaSegmentVersion(ctx, media.VideoURL, opts, 0, MediaVideoRange, media.VideoLength, media.VideoMIME, version)
	}
	if ctx.Err() == nil && videoStart.State == DLOK {
		videoMiddle = probeMediaSegmentVersion(ctx, media.VideoURL, opts, media.VideoLength/2, MediaVideoMiddleRange, media.VideoLength, media.VideoMIME, version)
	}
	return audio, videoStart, videoMiddle
}

func mediaVersionDetail(version TLSVersion, ds ...Download) VersionDetail {
	d := VersionDetail{State: StateNone, Time: "-", IP: "-", Proto: "-", Text: "media probe failed"}
	for _, sample := range ds {
		if sample.Code != 0 {
			d.Code, d.Time, d.IP, d.Proto = sample.Code, sample.Time, sample.IP, sample.Proto
			d.State = StateHTTP
			if sample.Code >= 200 && sample.Code < 400 {
				d.State = StateOK
			}
		}
		if d.Code == 0 && sample.Err != "" {
			d.Err = sample.Err
			switch sample.Err {
			case ErrDNS:
				d.State = StateDNS
			case ErrTimeout:
				d.State = StateTimeout
			case ErrConn:
				d.State = StateConn
			case ErrTLS:
				d.State = StateTLS
			case ErrAborted:
				d.State = StateAborted
			default:
				d.State = StateNone
			}
		}
	}
	d.Text = fmt.Sprintf("TLS %s media range", version.Label())
	return d
}

func resolvedRangeSize(total, requested int64) int {
	if total < 0 {
		return 0
	}
	if requested > total/4 {
		requested = total / 4
	}
	return int(requested)
}

func staleResolvedMedia(ds ...Download) bool {
	for _, d := range ds {
		if d.Code == http.StatusUnauthorized || d.Code == http.StatusForbidden || d.Code == http.StatusGone {
			return true
		}
	}
	return false
}

func classifyResolvedMedia(media YouTubeMedia, audio, videoStart, videoMiddle Download) MediaProbe {
	out := MediaProbe{Class: MediaUnknown, AudioBytes: audio.Size, VideoRangeBytes: videoStart.Size, VideoMiddleBytes: videoMiddle.Size}
	expectedAudio := resolvedRangeSize(media.AudioLength, MediaAudioRange)
	expectedVideo := resolvedRangeSize(media.VideoLength, MediaVideoRange)
	expectedMiddle := resolvedRangeSize(media.VideoLength, MediaVideoMiddleRange)
	if expectedAudio > 0 && expectedVideo > 0 && expectedMiddle > 0 &&
		audio.State == DLOK && audio.Size == expectedAudio &&
		videoStart.State == DLOK && videoStart.Size == expectedVideo &&
		videoMiddle.State == DLOK && videoMiddle.Size == expectedMiddle {
		out.Class = MediaPlay
		out.Text = fmt.Sprintf("Media OK: audio %d B, video %d B, middle video %d B", audio.Size, videoStart.Size, videoMiddle.Size)
		return out
	}
	for _, d := range []Download{audio, videoStart, videoMiddle} {
		if d.State == DLCut {
			out.Class = MediaMidstream
			out.Text = fmt.Sprintf("Медиадиапазон оборван: audio %d B, video %d B, middle video %d B", audio.Size, videoStart.Size, videoMiddle.Size)
			return out
		}
	}
	out.Text = "Медиапоток не подтверждён: ответ CDN не прошёл проверку диапазона"
	return out
}

func allMediaVersionsHaveNetworkFailure(details ...VersionDetail) bool {
	if len(details) == 0 {
		return false
	}
	for _, d := range details {
		switch d.State {
		case StateTimeout, StateConn, StateTLS:
		default:
			return false
		}
	}
	return true
}

// ProfileCheckOptions — доп. параметры CheckProfile (media_url для профиля 2).
type ProfileCheckOptions struct {
	// MediaURL — необязательный подписанный URL videoplayback. Если пуст, URLs
	// автономно получаются через Innertube.
	MediaURL string
	// VideoID задаёт ролик для автономного получения (по умолчанию встроенный).
	VideoID string
}

// ClassifyMediaRange выносит класс по TLS/DNS и двум Range-пробам.
// d16/d64 могут быть nil, если media URL не задан или проба не запускалась.
func ClassifyMediaRange(dnsFail, tlsOK bool, hasURL bool, d16, d64 *Download) MediaProbe {
	switch {
	case dnsFail:
		return MediaProbe{
			Class: MediaDNS,
			Text:  "DNS: домен googlevideo не разрешается — это не DPI-стратегия профиля 2.",
		}
	case !tlsOK:
		return MediaProbe{
			Class: MediaNone,
			Text:  "TLS к CDN не проходит — сначала подберите стратегию профиля 2.",
		}
	case !hasURL:
		return MediaProbe{
			Class: MediaTLS,
			Text:  "TLS к CDN OK (404 корня нормален), медиапоток ещё не проверен.",
		}
	}

	s16, s64 := 0, 0
	st16, st64 := "", ""
	if d16 != nil {
		s16, st16 = d16.Size, d16.State
	}
	if d64 != nil {
		s64, st64 = d64.Size, d64.State
	}
	out := MediaProbe{Size16K: s16, Size64K: s64}

	// 16K почти полный и 64K сильно меньше / cut → mid-stream DPI.
	if s16 >= MediaRange16K*3/4 && (st64 == DLCut || s64 < MediaRange64K/2) {
		out.Class = MediaMidstream
		out.Text = fmt.Sprintf("Mid-stream: 16K=%d ОК, 64K=%d (%s) — режьте WSSIZE lite / ipset googlevideo-ipv4.", s16, s64, st64)
		return out
	}
	// Добрали ≥ половина 64K → транспорт для медиа жив.
	if s64 >= MediaRange64K/2 && (st64 == DLOK || s64 >= MediaRange64K*3/4) {
		out.Class = MediaPlay
		out.Text = fmt.Sprintf("Media OK: 16K=%d, 64K=%d — CDN отдаёт тело; если плеер рвётся, смотрите QUIC/h3.", s16, s64)
		return out
	}
	// Tiny / zero после TLS.
	if s16 < MediaTinyMax && s64 < MediaTinyMax {
		out.Class = MediaTiny
		out.Text = fmt.Sprintf("Tiny after TLS: 16K=%d, 64K=%d — похоже на bad ClientHello / ошибку CDN; ротируйте стратегию профиля 2.", s16, s64)
		return out
	}
	out.Class = MediaTiny
	out.Text = fmt.Sprintf("Слабый поток: 16K=%d (%s), 64K=%d (%s).", s16, st16, s64, st64)
	return out
}

// ProbeMediaRanges делает Range 16K и 64K на mediaURL.
func ProbeMediaRanges(ctx context.Context, mediaURL string, opts Options) (d16, d64 Download) {
	opts = opts.withDefaults()
	if _, err := validateYouTubeMediaURL(mediaURL); err != nil {
		invalid := Download{State: DLFail, Text: "invalid or expired YouTube media URL"}
		return invalid, invalid
	}
	o16 := opts
	o16.DownloadRange = MediaRange16K
	d16 = ProbeDownload(ctx, mediaURL, o16)
	if ctx.Err() != nil {
		return d16, d64
	}
	o64 := opts
	o64.DownloadRange = MediaRange64K
	d64 = ProbeDownload(ctx, mediaURL, o64)
	return d16, d64
}

// applyMediaToResult дополняет TargetResult полем Media и ужесточает verdict
// при tiny/midstream (TLS «зелёный» больше не маскирует обрыв видео).
func applyMediaToResult(r *TargetResult, media MediaProbe) {
	if r == nil {
		return
	}
	m := media
	r.Media = &m
	switch m.Class {
	case MediaMidstream:
		r.Verdict = VerdictFail
		r.Text = m.Text
	case MediaFail:
		r.Verdict = VerdictFail
		r.Text = m.Text
	case MediaTiny:
		r.Verdict = VerdictFail
		r.Text = m.Text
	case MediaDNS:
		r.Verdict = VerdictFail
		if r.Text == "" {
			r.Text = m.Text
		}
	case MediaPlay:
		r.Verdict = VerdictOK
		r.Text = m.Text
	case MediaWarn:
		r.Verdict = VerdictWarn
		r.Text = m.Text
	case MediaTLS:
		// TLS-only: оставляем ok на 404 корня, но текст поясняет ограничение.
		if r.Verdict == VerdictOK {
			r.Text = r.Text + " " + m.Text
		}
	case MediaUnknown:
		r.Verdict = VerdictUnknown
		r.Text = m.Text
	}
}

// looksLikeGooglevideoMediaURL проверяет подписанный URL media сегмента.
func looksLikeGooglevideoMediaURL(u string) bool {
	_, err := validateYouTubeMediaURL(strings.TrimSpace(u))
	return err == nil
}

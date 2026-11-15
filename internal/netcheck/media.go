package netcheck

import (
	"context"
	"fmt"
	"strings"
)

// Классы media-пробы googlevideo / videoplayback (Package B).
const (
	MediaNone      = "none"      // нет данных / TLS не прошёл
	MediaDNS       = "dns"       // NXDOMAIN / DNS fail
	MediaTLS       = "tls"       // TLS к CDN OK, media URL не проверялся
	MediaUnknown   = "unknown"   // Innertube или media URL недоступны; медиа не подтверждено
	MediaTiny      = "tiny"      // после TLS пришло ≪ 16 КБ (tiny_after_tls)
	MediaMidstream = "midstream" // ~16 КБ ок, 64 КБ срезан
	MediaPlay      = "play"      // ≥64 КБ ушли
	MediaQUIC      = "quic_hint" // подсказка: смотри UDP/h3
)

// Пороги Range-проб (как в test_googlevideo.com.md).
const (
	MediaRange16K = 16384
	MediaRange64K = 65536
	MediaTinyMax  = 4096
)

// MediaProbe — классификация обрыва видеопотока (поле media в TargetResult).
type MediaProbe struct {
	Class          string `json:"class"`
	Text           string `json:"text"`
	Size16K        int    `json:"size_16k,omitempty"`
	Size64K        int    `json:"size_64k,omitempty"`
	Host           string `json:"host,omitempty"`
	VideoID        string `json:"video_id,omitempty"`
	AudioHost      string `json:"audio_host,omitempty"`
	AudioBytes     int    `json:"audio_bytes,omitempty"`
	ResolverStatus string `json:"resolver_status,omitempty"`
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
	case MediaTiny:
		r.Verdict = VerdictFail
		r.Text = m.Text
	case MediaDNS:
		r.Verdict = VerdictFail
		if r.Text == "" {
			r.Text = m.Text
		}
	case MediaPlay:
		if r.Verdict == VerdictOK || r.Verdict == "" {
			r.Text = m.Text
		}
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

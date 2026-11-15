package netcheck

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultYouTubeVideoID = "BPk1uJ4txPc"
	youtubeAPIBase        = "https://www.youtube.com/youtubei/v1/"
	maxInnertubeResponse  = 256 << 10
)

var youtubeMediaHostRE = regexp.MustCompile(`^[a-z0-9.-]+$`)

// YouTubeMedia contains short-lived URLs returned by Innertube. Do not log or
// persist these URLs: they contain signed playback credentials.
type YouTubeMedia struct {
	VideoID     string
	AudioURL    string
	VideoURL    string
	AudioHost   string
	VideoHost   string
	AudioLength int64
	VideoLength int64
	AudioMIME   string
	VideoMIME   string
}

// MediaResolver may be replaced by tests or callers with a local resolver.
type MediaResolver func(context.Context, string) (YouTubeMedia, error)

// ResolveYouTubeMedia obtains direct audio and video URLs without browser state.
// It uses only the standard library and bounds each response body.
func ResolveYouTubeMedia(ctx context.Context, videoID string, opts Options) (YouTubeMedia, error) {
	return resolveYouTubeMedia(ctx, videoID, opts, youtubeAPIBase)
}

func resolveYouTubeMedia(ctx context.Context, videoID string, opts Options, apiBase string) (YouTubeMedia, error) {
	if !validYouTubeVideoID(videoID) {
		return YouTubeMedia{}, errors.New("invalid_video_id")
	}
	cpnBytes := make([]byte, 12)
	if _, err := rand.Read(cpnBytes); err != nil {
		return YouTubeMedia{}, errors.New("random_failed")
	}
	cpn := base64.RawURLEncoding.EncodeToString(cpnBytes)
	client := youtubeAPIClient(opts.withDefaults())
	defer client.CloseIdleConnections()
	visitorURL := strings.TrimRight(apiBase, "/") + "/visitor_id?prettyPrint=false"
	var visitorResp struct {
		ResponseContext struct {
			VisitorData string `json:"visitorData"`
		} `json:"responseContext"`
	}
	body := innertubeBody("", "", "")
	if err := postInnertube(ctx, client, visitorURL, body, &visitorResp); err != nil {
		return YouTubeMedia{}, err
	}
	visitor := visitorResp.ResponseContext.VisitorData
	if visitor == "" || len(visitor) > 4096 || strings.ContainsAny(visitor, "\r\n\x00") {
		return YouTubeMedia{}, errors.New("visitor_missing")
	}

	playerURL, _ := url.Parse(strings.TrimRight(apiBase, "/") + "/player")
	q := playerURL.Query()
	q.Set("prettyPrint", "false")
	q.Set("t", cpn[:12])
	q.Set("id", videoID)
	q.Set("$fields", "playabilityStatus,videoDetails(videoId,lengthSeconds),streamingData")
	playerURL.RawQuery = q.Encode()
	var player struct {
		PlayabilityStatus struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"playabilityStatus"`
		VideoDetails struct {
			VideoID string `json:"videoId"`
		} `json:"videoDetails"`
		StreamingData struct {
			AdaptiveFormats []struct {
				URL             string            `json:"url"`
				MIMEType        string            `json:"mimeType"`
				ContentLength   json.RawMessage   `json:"contentLength"`
				Bitrate         int64             `json:"bitrate"`
				DRMFamilies     []json.RawMessage `json:"drmFamilies"`
				SignatureCipher string            `json:"signatureCipher"`
				Cipher          string            `json:"cipher"`
			} `json:"adaptiveFormats"`
		} `json:"streamingData"`
	}
	if err := postInnertube(ctx, client, playerURL.String(), innertubeBody(videoID, cpn, visitor), &player); err != nil {
		return YouTubeMedia{}, err
	}
	if player.VideoDetails.VideoID != videoID {
		return YouTubeMedia{}, errors.New("video_id_mismatch")
	}
	if player.PlayabilityStatus.Status != "OK" {
		reason := strings.ToLower(player.PlayabilityStatus.Reason)
		if strings.Contains(reason, "po token") || strings.Contains(reason, "potoken") {
			return YouTubeMedia{}, errors.New("po_token_required")
		}
		if player.PlayabilityStatus.Status == "LOGIN_REQUIRED" {
			return YouTubeMedia{}, errors.New("auth_required")
		}
		return YouTubeMedia{}, errors.New("video_unavailable")
	}

	var audio, video *selectedYouTubeFormat
	ciphered := false
	for _, f := range player.StreamingData.AdaptiveFormats {
		if f.SignatureCipher != "" || f.Cipher != "" {
			ciphered = true
		}
		kind := ""
		mime := strings.ToLower(strings.TrimSpace(strings.SplitN(f.MIMEType, ";", 2)[0]))
		switch {
		case strings.HasPrefix(mime, "audio/"):
			kind = "audio"
		case strings.HasPrefix(mime, "video/"):
			kind = "video"
		default:
			continue
		}
		if f.URL == "" || len(f.DRMFamilies) != 0 || parseContentLength(f.ContentLength) < 4096 {
			continue
		}
		host, err := validateYouTubeMediaURL(f.URL)
		if err != nil {
			continue
		}
		item := selectedYouTubeFormat{url: f.URL, host: host, mime: mime, size: parseContentLength(f.ContentLength), bitrate: f.Bitrate}
		if kind == "audio" {
			chooseFormat(&audio, item, 1<<20)
		} else {
			chooseFormat(&video, item, 8<<20)
		}
	}
	if audio == nil || video == nil {
		if ciphered {
			return YouTubeMedia{}, errors.New("signature_required")
		}
		return YouTubeMedia{}, errors.New("media_urls_missing")
	}
	return YouTubeMedia{VideoID: videoID, AudioURL: audio.url, VideoURL: video.url, AudioHost: audio.host, VideoHost: video.host,
		AudioLength: audio.size, VideoLength: video.size, AudioMIME: audio.mime, VideoMIME: video.mime}, nil
}

type selectedYouTubeFormat struct {
	url, host     string
	mime          string
	size, bitrate int64
}

func staleYouTubeMedia(a16, a64, v16, v64 Download) bool {
	for _, d := range []Download{a16, a64, v16, v64} {
		switch d.Code {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusGone:
			return true
		}
	}
	return false
}

func chooseFormat(dst **selectedYouTubeFormat, f selectedYouTubeFormat, preferredSize int64) {
	if *dst == nil || (f.size >= preferredSize && (*dst).size < preferredSize) ||
		((f.size >= preferredSize) == ((*dst).size >= preferredSize) && f.bitrate < (*dst).bitrate) {
		copy := f
		*dst = &copy
	}
}

func validYouTubeVideoID(id string) bool {
	if len(id) != 11 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func parseContentLength(raw json.RawMessage) int64 {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		n, _ := strconv.ParseInt(s, 10, 64)
		return n
	}
	var n int64
	_ = json.Unmarshal(raw, &n)
	return n
}

func innertubeBody(videoID, cpn, visitor string) []byte {
	client := map[string]any{
		"clientName": "VISIONOS", "clientVersion": "1.04", "clientScreen": "WATCH",
		"platform": "MOBILE", "deviceMake": "Apple", "deviceModel": "RealityDevice17,1",
		"osName": "visionOS", "osVersion": "26.6.0.23O770", "hl": "en", "gl": "US", "utcOffsetMinutes": 0,
	}
	if visitor != "" {
		client["visitorData"] = visitor
	}
	body := map[string]any{
		"context": map[string]any{"client": client},
		"request": map[string]any{"internalExperimentFlags": []any{}, "useSsl": true},
		"user":    map[string]any{"lockedSafetyMode": false},
	}
	if videoID != "" {
		body["videoId"], body["cpn"] = videoID, cpn
		body["contentCheckOk"], body["racyCheckOk"] = true, true
	}
	b, _ := json.Marshal(body)
	return b
}

func youtubeAPIClient(opts Options) *http.Client {
	dial := opts.DialContext
	if dial == nil {
		d := &net.Dialer{Timeout: opts.ConnectTimeout}
		dial = d.DialContext
	}
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if network == "tcp" {
				network = "tcp4"
			}
			return dial(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: opts.InsecureSkipVerify, RootCAs: opts.RootCAs},
	}
	return &http.Client{Transport: tr, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func postInnertube(ctx context.Context, client *http.Client, endpoint string, body []byte, dst any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("api_error")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Format-Version", "2")
	req.Header.Set("User-Agent", "com.google.visionos.youtube/1.04(RealityDevice17,1; U; CPU visionOS 26_6_0 like Mac OS X; US)")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("network_error")
		}
		return errors.New("network_error")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusForbidden:
			return errors.New("innertube_denied")
		case http.StatusTooManyRequests:
			return errors.New("rate_limited")
		default:
			return errors.New("api_error")
		}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxInnertubeResponse+1))
	if err != nil || len(data) > maxInnertubeResponse {
		return errors.New("api_response_too_large")
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return errors.New("api_response_invalid")
	}
	return nil
}

func validateYouTubeMediaURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Path != "/videoplayback" {
		return "", errors.New("invalid_media_url")
	}
	host := strings.ToLower(u.Hostname())
	if !strings.HasSuffix(host, ".googlevideo.com") || strings.Contains(host, "..") || !youtubeMediaHostRE.MatchString(host) || u.Port() != "" {
		return "", errors.New("invalid_media_url")
	}
	q := u.Query()
	if q.Get("sig") == "" && q.Get("signature") == "" {
		return "", errors.New("unsigned_media_url")
	}
	if q.Has("n") {
		return "", errors.New("unsupported_media_url")
	}
	exp := q.Get("expire")
	unix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || unix <= time.Now().Add(2*time.Minute).Unix() {
		return "", errors.New("expired_media_url")
	}
	return host, nil
}

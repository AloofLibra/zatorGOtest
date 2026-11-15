package netcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestResolveYouTubeMedia(t *testing.T) {
	var calls int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("User-Agent"), "com.google.visionos.youtube/") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.URL.Path == "/visitor_id" {
			_ = json.NewEncoder(w).Encode(map[string]any{"responseContext": map[string]string{"visitorData": "visitor-data"}})
			return
		}
		if r.URL.Path != "/player" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			VideoID string `json:"videoId"`
			CPN     string `json:"cpn"`
			Context struct {
				Client struct {
					Visitor string `json:"visitorData"`
				} `json:"client"`
			} `json:"context"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.VideoID != "BPk1uJ4txPc" || body.CPN == "" || body.Context.Client.Visitor != "visitor-data" {
			t.Errorf("bad player request: %+v, err=%v", body, err)
		}
		exp := time.Now().Add(time.Hour).Unix()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"playabilityStatus": map[string]string{"status": "OK"},
			"videoDetails":      map[string]string{"videoId": body.VideoID},
			"streamingData": map[string]any{"adaptiveFormats": []any{
				map[string]any{"url": fmt.Sprintf("https://rr1---sn-a.googlevideo.com/videoplayback?sig=a&expire=%d", exp), "mimeType": "audio/mp4; codecs=\"mp4a.40.2\"", "contentLength": "2097152", "bitrate": 128000},
				map[string]any{"url": fmt.Sprintf("https://rr2---sn-b.googlevideo.com/videoplayback?sig=v&expire=%d", exp), "mimeType": "video/mp4; codecs=\"avc1.4d401f\"", "contentLength": "16777216", "bitrate": 1000000},
			}},
		})
	}))
	defer srv.Close()

	media, err := resolveYouTubeMedia(context.Background(), DefaultYouTubeVideoID, Options{InsecureSkipVerify: true}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || media.VideoID != DefaultYouTubeVideoID || media.AudioHost != "rr1---sn-a.googlevideo.com" || media.VideoHost != "rr2---sn-b.googlevideo.com" {
		t.Fatalf("calls=%d media=%+v", calls, media)
	}
	if strings.Contains(media.AudioURL, "visitor") || !strings.Contains(media.VideoURL, "sig=v") {
		t.Fatalf("unexpected resolved URLs: %+v", media)
	}
}

func TestCheckGooglevideoMediaURLsUsesAndRedactsSignedURLs(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, end := int64(0), int64(65535)
		if r.Header.Get("Range") != "" {
			_, _ = fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/16777216", start, end))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(make([]byte, end-start+1))
	}))
	defer srv.Close()
	exp := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	media := YouTubeMedia{
		VideoID:   DefaultYouTubeVideoID,
		AudioURL:  "https://rr1---sn-a.googlevideo.com/videoplayback?sig=audio&expire=" + exp,
		VideoURL:  "https://rr2---sn-b.googlevideo.com/videoplayback?sig=video&expire=" + exp,
		AudioHost: "rr1---sn-a.googlevideo.com", VideoHost: "rr2---sn-b.googlevideo.com",
	}
	dialer := &net.Dialer{}
	checker := NewChecker(Options{InsecureSkipVerify: true, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, srv.Listener.Addr().String())
	}})
	result := checker.CheckGooglevideoMediaURLs(context.Background(), "https://rr0---sn-c.googlevideo.com/", media)
	if result.Media == nil || result.Media.Class != MediaPlay || result.Verdict != VerdictOK {
		t.Fatalf("media result = %+v", result)
	}
	if result.Media.AudioBytes != MediaRange16K+MediaRange64K || result.Media.VideoID != media.VideoID {
		t.Fatalf("media details = %+v", result.Media)
	}
	if strings.Contains(result.Target, "sig=") || !strings.Contains(result.Target, media.VideoHost) {
		t.Fatalf("signed URL leaked in target: %q", result.Target)
	}
}

func TestCheckGooglevideoMediaURLsRefreshesStaleSignatureOnce(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("sig") == "old" {
			http.Error(w, "expired", http.StatusForbidden)
			return
		}
		start, end := int64(0), int64(65535)
		if r.Header.Get("Range") != "" {
			_, _ = fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/16777216", start, end))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(make([]byte, end-start+1))
	}))
	defer srv.Close()
	exp := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	makeURLs := func(sig string) YouTubeMedia {
		return YouTubeMedia{VideoID: DefaultYouTubeVideoID,
			AudioURL:  "https://rr1---sn-a.googlevideo.com/videoplayback?sig=" + sig + "&expire=" + exp,
			VideoURL:  "https://rr2---sn-b.googlevideo.com/videoplayback?sig=" + sig + "&expire=" + exp,
			AudioHost: "rr1---sn-a.googlevideo.com", VideoHost: "rr2---sn-b.googlevideo.com"}
	}
	dialer := &net.Dialer{}
	checker := NewChecker(Options{InsecureSkipVerify: true, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, srv.Listener.Addr().String())
	}})
	refreshes := 0
	checker.MediaResolver = func(context.Context, string) (YouTubeMedia, error) {
		refreshes++
		return makeURLs("fresh"), nil
	}
	result := checker.CheckGooglevideoMediaURLs(context.Background(), "https://rr0---sn-c.googlevideo.com/", makeURLs("old"))
	if refreshes != 1 || result.Media == nil || result.Media.Class != MediaPlay || result.Media.ResolverStatus != "" {
		t.Fatalf("refreshes=%d media=%+v verdict=%s", refreshes, result.Media, result.Verdict)
	}
}

func TestResolveYouTubeMediaRejectsInvalidIDAndUnsafeURL(t *testing.T) {
	if _, err := resolveYouTubeMedia(context.Background(), "../../etc/passwd", Options{}, "https://invalid.invalid"); err == nil || err.Error() != "invalid_video_id" {
		t.Fatalf("invalid id error = %v", err)
	}
	for _, raw := range []string{
		"http://rr1.googlevideo.com/videoplayback?sig=x",
		"https://fakegooglevideo.com/videoplayback?sig=x",
		"https://rr1.googlevideo.com/videoplayback?id=x",
		"https://rr1.googlevideo.com/videoplayback?sig=x&n=unsupported",
	} {
		if _, err := validateYouTubeMediaURL(raw); err == nil {
			t.Errorf("accepted unsafe URL %q", raw)
		}
	}
}

func TestStaleYouTubeMediaHTTPStatuses(t *testing.T) {
	if !staleYouTubeMedia(Download{Code: http.StatusForbidden}, Download{}, Download{}, Download{}) {
		t.Fatal("403 must trigger one URL refresh")
	}
	if staleYouTubeMedia(Download{Code: 206}, Download{}, Download{Code: 404}, Download{}) {
		t.Fatal("ordinary range responses are not stale signed URLs")
	}
}

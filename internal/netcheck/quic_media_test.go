package netcheck

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/paths"
)

func argValue(args []string, flag string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func TestProbeQUICRangeSecretConfigAndRedirects(t *testing.T) {
	signedURL := "https://rr1---sn.googlevideo.com/videoplayback?expire=9999999999&sig=SECRET_SIG_TOKEN"
	redirectURL := "https://rr2---sn.googlevideo.com/videoplayback?expire=9999999999&sig=REDIRECT_SIG"

	var calls int
	runner := func(_ context.Context, _ string, args []string, extraEnv []string) (string, int, error) {
		calls++
		// Critical security invariant: URL never in argv.
		for _, a := range args {
			if strings.Contains(a, "SECRET_SIG_TOKEN") || strings.Contains(a, "REDIRECT_SIG") || strings.Contains(a, "googlevideo.com") {
				t.Fatalf("URL leaked into argv: %v", args)
			}
		}
		cfgPath := argValue(args, "--config")
		hdrPath := argValue(args, "-D")
		if cfgPath == "" || hdrPath == "" {
			t.Fatalf("missing --config or -D in args: %v", args)
		}
		cfgRaw, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		if calls == 1 {
			if !strings.Contains(string(cfgRaw), signedURL) {
				t.Fatalf("initial url missing from --config: %s", cfgRaw)
			}
			_ = os.WriteFile(hdrPath, []byte("HTTP/3 302\r\nLocation: "+redirectURL+"\r\n\r\n"), 0o600)
			return "302|0|3|0.01|0.02|0.03|0.04|0", 0, nil
		}
		if !strings.Contains(string(cfgRaw), redirectURL) {
			t.Fatalf("redirect url missing from --config: %s", cfgRaw)
		}
		_ = os.WriteFile(hdrPath, []byte("HTTP/3 206\r\nContent-Range: bytes 0-4095/40000\r\nContent-Type: video/webm; codecs=vp9\r\n\r\n"), 0o600)
		return "206|4096|3|0.01|0.02|0.03|0.05|80000", 0, nil
	}

	pr := ProbeQUICRange(context.Background(), "/usr/bin/curl", "", signedURL, "video/webm", 40000, 0, 4096, "", runner)
	if calls != 2 || pr.State != "OK" || pr.Reason != "media_h3" || pr.HTTPVersion != "3" {
		t.Fatalf("unexpected probe result (calls=%d): %+v", calls, pr)
	}
}

func TestCheckProfile5WithAndWithoutH3Curl(t *testing.T) {
	p := paths.New(filepath.Join(t.TempDir(), "zator"), filepath.Join(t.TempDir(), "zapret2"))
	media := YouTubeMedia{
		VideoID:     "BPk1uJ4txPc",
		VideoURL:    "https://rr1---sn.googlevideo.com/videoplayback?sig=AAA",
		VideoHost:   "rr1---sn.googlevideo.com",
		VideoMIME:   "video/mp4",
		VideoLength: 10 << 20,
	}

	// Missing h3-curl must explain how to install instead of erroring abruptly.
	cMissing := &Checker{
		Paths: p,
		H3Env: H3Env{
			Arch:            "amd64",
			EntwareCurlPath: filepath.Join(t.TempDir(), "none"),
			LookPathFn:      func(string) (string, error) { return "", os.ErrNotExist },
			VersionCheckFn:  func(string) (string, error) { return "", os.ErrNotExist },
		},
	}
	respMissing := cMissing.CheckProfile(context.Background(), 5)
	if len(respMissing.Results) != 0 || !strings.Contains(respMissing.Message, "h3-curl") {
		t.Fatalf("unexpected missing h3-curl response: %+v", respMissing)
	}

	// Partial pass (first 2 probes OK, remaining fail) -> VerdictWarn.
	fakeCurl := filepath.Join(t.TempDir(), "fake-h3-curl")
	_ = os.WriteFile(fakeCurl, []byte("x"), 0o755)
	var probeIdx int
	cWarn := &Checker{
		Paths: p,
		H3Env: H3Env{
			Arch:           "amd64",
			EnvOverride:    fakeCurl,
			VersionCheckFn: func(string) (string, error) { return "HTTP3 nghttp3", nil },
		},
		MediaResolver: func(context.Context, string) (YouTubeMedia, error) { return media, nil },
		QUICRunner: func(_ context.Context, _ string, args []string, _ []string) (string, int, error) {
			probeIdx++
			hdrPath := argValue(args, "-D")
			rng := argValue(args, "--range")
			maxSz := argValue(args, "--max-filesize")
			if probeIdx <= 2 {
				hdr := fmt.Sprintf("HTTP/3 206\r\nContent-Range: bytes %s/%d\r\nContent-Type: video/mp4\r\n\r\n", rng, media.VideoLength)
				_ = os.WriteFile(hdrPath, []byte(hdr), 0o600)
				return fmt.Sprintf("206|%s|3|0.01|0.02|0.03|0.05|100000", maxSz), 0, nil
			}
			return "000|0|0|0|0|0|0|0", 28, nil
		},
	}
	respWarn := cWarn.CheckProfile(context.Background(), 5)
	if len(respWarn.Results) != 1 || respWarn.Results[0].Verdict != VerdictWarn || respWarn.Results[0].QUIC.OKCount != 2 {
		t.Fatalf("expected VerdictWarn with 2/5 OK, got %+v", respWarn)
	}
}

package sweep

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/netcheck"
	"github.com/AloofLibra/zator/internal/orchestra"
)

func TestProbeGooglevideoCandidatesUsesPrivateLockAndRequiresBothTLSAndFullDownload(t *testing.T) {
	e := newCoordEnv(t, true, func(_ string, strategy int) netcheck.TargetResult {
		if strategy == 2 {
			return resGood("1.0")
		}
		return resOnly12()
	}, nil)
	const url = "https://rr1---sn-example.googlevideo.com/"
	e.chk.keyFor[url] = "2"
	media := netcheck.YouTubeMedia{
		VideoURL:  "https://rr1---sn-example.googlevideo.com/videoplayback?sig=video",
		AudioURL:  "https://rr1---sn-example.googlevideo.com/videoplayback?sig=audio",
		VideoHost: "rr1---sn-example.googlevideo.com", AudioHost: "rr1---sn-example.googlevideo.com",
	}

	selected, _, err := e.c.ProbeGooglevideoCandidates(context.Background(), "rr1---sn-example.googlevideo.com", []int{1, 2}, media, nil)
	if err != nil || selected != 2 {
		t.Fatalf("selected=%d err=%v", selected, err)
	}
	if got := e.chk.callLog(); len(got) != 2 || got[0] != "2:1" || got[1] != "2:2" {
		t.Fatalf("candidate order = %v", got)
	}
	if got := mustGet(t, e.st, "2", "tls"); !got.IsAuto() {
		t.Fatalf("production lock changed to %v", got)
	}
	if got := mustGet(t, e.probeSt, "2", "tls"); got != orchestra.Strategy(2) {
		t.Fatalf("private lock = %v, want 2", got)
	}
	if starts := e.probe.startList(); len(starts) != 1 || starts[0] != 2 || e.probe.stops != 1 {
		t.Fatalf("probe lifecycle starts=%v stops=%d", starts, e.probe.stops)
	}
}

func TestProbeGooglevideoCandidatesRejectsPartialDownloadAndBusyRun(t *testing.T) {
	e := newCoordEnv(t, true, func(string, int) netcheck.TargetResult { return resCut() }, nil)
	const url = "https://rr1---sn-example.googlevideo.com/"
	e.chk.keyFor[url] = "2"
	media := netcheck.YouTubeMedia{
		VideoURL:  "https://rr1---sn-example.googlevideo.com/videoplayback?sig=video",
		AudioURL:  "https://rr1---sn-example.googlevideo.com/videoplayback?sig=audio",
		VideoHost: "rr1---sn-example.googlevideo.com", AudioHost: "rr1---sn-example.googlevideo.com",
	}
	selected, _, err := e.c.ProbeGooglevideoCandidates(context.Background(), "rr1---sn-example.googlevideo.com", []int{1}, media, nil)
	if err != nil || selected != 0 {
		t.Fatalf("partial download selected=%d err=%v", selected, err)
	}
	if got := mustGet(t, e.st, "2", "tls"); !got.IsAuto() {
		t.Fatalf("partial result changed production lock to %v", got)
	}

	lockDir := e.c.lockDir()
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "pid"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.c.ProbeGooglevideoCandidates(context.Background(), "rr1---sn-example.googlevideo.com", []int{1}, media, nil)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("busy probe error = %v", err)
	}
}

func TestProbeGooglevideoCandidatesRejectsDifferentMediaHostkey(t *testing.T) {
	e := newCoordEnv(t, true, func(string, int) netcheck.TargetResult { return resGood("1.0") }, nil)
	media := netcheck.YouTubeMedia{
		VideoURL:  "https://rr1---sn-other.googlevideo.com/videoplayback?sig=video",
		AudioURL:  "https://rr1---sn-other.googlevideo.com/videoplayback?sig=audio",
		VideoHost: "rr1---sn-other.googlevideo.com", AudioHost: "rr1---sn-other.googlevideo.com",
	}
	selected, detail, err := e.c.ProbeGooglevideoCandidates(context.Background(), "rr1---sn-example.googlevideo.com", []int{16}, media, nil)
	if err != nil || selected != 0 || !strings.Contains(detail, "media_hostkey_mismatch") {
		t.Fatalf("mismatched media selected=%d detail=%q err=%v", selected, detail, err)
	}
	if starts := e.probe.startList(); len(starts) != 0 {
		t.Fatalf("probe started for another POP: %v", starts)
	}
}

package app

import (
	"context"
	"os"
	"testing"
)

// TestLiveGooglevideoSeedProbe запускает одну настоящую изолированную probe
// только при явном включении на тестовом роутере. Он не сохраняет auto-lock.
func TestLiveGooglevideoSeedProbe(t *testing.T) {
	if os.Getenv("ZATOR_LIVE_GOOGLEVIDEO_PROBE") != "1" {
		t.Skip("requires explicit isolated-router probe")
	}
	host := os.Getenv("ZATOR_LIVE_GOOGLEVIDEO_HOST")
	if host == "" {
		t.Fatal("ZATOR_LIVE_GOOGLEVIDEO_HOST is required")
	}
	a := New()
	candidates := []int{16}
	strategy, detail, err := a.probeGooglevideoCandidates(context.Background(), host, candidates, func(n int) {
		t.Logf("trying profile 2 strategy %d", n)
	})
	if err != nil {
		t.Fatal(err)
	}
	if strategy == 0 {
		t.Fatalf("no verified strategy for %s: %s", host, detail)
	}
	t.Logf("verified profile 2 strategy %d for %s: %s", strategy, host, detail)
}

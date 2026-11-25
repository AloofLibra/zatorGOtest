package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/AloofLibra/zator/internal/googlevideo"
	"github.com/AloofLibra/zator/internal/paths"
	"github.com/AloofLibra/zator/internal/platform"
)

func TestNativeGooglevideoCollectorRunningAt(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "collector.pid")
	procRoot := filepath.Join(root, "proc")
	cmdline := filepath.Join(procRoot, "1234", "cmdline")
	if err := os.MkdirAll(filepath.Dir(cmdline), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidFile, []byte("1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cmdline, []byte("/opt/zator/tools/bin/googlevideo-dns-sniffer\x00--collector\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !nativeGooglevideoCollectorRunningAt(pidFile, procRoot) {
		t.Fatal("active native collector was not detected")
	}
	if err := os.WriteFile(cmdline, []byte("/opt/zator/bin/zator\x00webui\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if nativeGooglevideoCollectorRunningAt(pidFile, procRoot) {
		t.Fatal("unrelated process was mistaken for the native collector")
	}
}

func TestRefreshGooglevideoIPv4SyncsKernelSet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# googlevideo IPv4; generated=2026-11-25T10:00:00Z; max_age=604800s\n8.8.8.8\n1.1.1.1\n"))
	}))
	defer srv.Close()
	t.Setenv(googlevideo.EnvListURL, srv.URL)

	root := t.TempDir()
	p := paths.New(filepath.Join(root, "zator"), filepath.Join(root, "zapret2"))
	a := NewWith(p, platform.Platform{})

	var synced []string
	prevHook := gvSyncKernelIPsHook
	gvSyncKernelIPsHook = func(ctx context.Context, ips []string) error {
		synced = append([]string(nil), ips...)
		return nil
	}
	t.Cleanup(func() { gvSyncKernelIPsHook = prevHook })

	res, err := a.RefreshGooglevideoIPv4(context.Background(), true)
	if err != nil {
		t.Fatalf("RefreshGooglevideoIPv4: %v", err)
	}
	if res.Status != googlevideo.UpdateFetched || res.Count != 2 {
		t.Fatalf("result = %+v", res)
	}
	if !reflect.DeepEqual(synced, []string{"8.8.8.8", "1.1.1.1"}) {
		t.Fatalf("synced = %v, want [8.8.8.8 1.1.1.1]", synced)
	}
}

func TestResolveGooglevideoHosts(t *testing.T) {
	t.Setenv(googlevideo.EnvListURL, "off")
	root := t.TempDir()
	t.Setenv("Z2R_GV_TMPDIR", filepath.Join(root, "gv-state"))
	p := paths.New(filepath.Join(root, "zator"), filepath.Join(root, "zapret2"))
	a := NewWith(p, platform.Platform{})

	sniLog := p.GooglevideoSNILog()
	if err := os.MkdirAll(filepath.Dir(sniLog), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sniLog, []byte("rr1---sn-abc123.googlevideo.com\t1700000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prevLookup := gvLookupIPv4Hook
	prevSync := gvSyncKernelIPsHook
	prevCollect := gvCollect
	gvCollect = nil
	gvLookupIPv4Hook = func(ctx context.Context, host string) ([]string, error) {
		if host == "rr1---sn-abc123.googlevideo.com" {
			return []string{"8.8.4.4"}, nil
		}
		return nil, nil
	}
	var synced []string
	gvSyncKernelIPsHook = func(ctx context.Context, ips []string) error {
		synced = append([]string(nil), ips...)
		return nil
	}
	t.Cleanup(func() {
		gvLookupIPv4Hook = prevLookup
		gvSyncKernelIPsHook = prevSync
		gvCollect = prevCollect
	})

	if added, err := a.SyncGooglevideoHosts(); err != nil || added != 1 {
		t.Fatalf("SyncGooglevideoHosts = (%d, %v)", added, err)
	}
	res, err := a.ResolveGooglevideoHosts(context.Background(), false)
	if err != nil {
		t.Fatalf("ResolveGooglevideoHosts: %v", err)
	}
	if res.Resolved != 1 || !reflect.DeepEqual(res.LiveIPs, []string{"8.8.4.4"}) {
		t.Fatalf("res = %+v", res)
	}
	if !reflect.DeepEqual(synced, []string{"8.8.4.4"}) {
		t.Fatalf("kernel sync = %v, want [8.8.4.4]", synced)
	}
	if got := a.GooglevideoLocalIPCount(); got != 1 {
		t.Fatalf("GooglevideoLocalIPCount = %d, want 1", got)
	}
}

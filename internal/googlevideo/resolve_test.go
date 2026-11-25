package googlevideo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestResolveHosts_FiltersAndRecords(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1700000000, 0)
	col := NewCollector(CollectorConfig{
		Dir:       dir,
		HostsFile: filepath.Join(dir, HostsFileName),
		Now:       func() time.Time { return now },
	})

	hosts := []string{
		"rr1---sn-aaa111.googlevideo.com",
		"rr1---sn-aaa111.googlevideo.com", // дубликат
		"rr2---sn-bbb222.googlevideo.com",
		"rr3---sn-err333.googlevideo.com",
		"invalid.example.com",
	}

	var calls atomic.Int32
	res := ResolveHosts(context.Background(), hosts, ResolveConfig{
		Collector: col,
		Now:       func() time.Time { return now },
		LookupIPv4: func(ctx context.Context, host string) ([]string, error) {
			calls.Add(1)
			switch host {
			case "rr1---sn-aaa111.googlevideo.com":
				return []string{"8.8.8.8", "192.168.1.10", "2001:db8::1", "8.8.8.8"}, nil
			case "rr2---sn-bbb222.googlevideo.com":
				return []string{"1.1.1.1", "10.0.0.1"}, nil
			default:
				return nil, errors.New("dns timeout")
			}
		},
	})

	if calls.Load() != 3 || res.Checked != 3 {
		t.Fatalf("calls=%d checked=%d, want 3", calls.Load(), res.Checked)
	}
	if res.Resolved != 2 || !reflect.DeepEqual(res.IPs, []string{"1.1.1.1", "8.8.8.8"}) {
		t.Fatalf("resolved IPs = %+v", res)
	}
	if !reflect.DeepEqual(res.LiveIPs, []string{"1.1.1.1", "8.8.8.8"}) {
		t.Fatalf("live IPs = %v", res.LiveIPs)
	}
	local := col.Local()
	if len(local) != 2 {
		t.Fatalf("local.tsv rows = %d, want 2: %+v", len(local), local)
	}
	if pending := len(col.Pending()); pending != 2 {
		t.Fatalf("pending.tsv count = %d, want 2", pending)
	}
}

func TestResolveHosts_SkipsFreshUnlessForced(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1700000000, 0)
	col := NewCollector(CollectorConfig{
		Dir: dir,
		Now: func() time.Time { return now },
	})
	if _, err := col.Observe(Observation{
		Host: "rr1---sn-aaa111.googlevideo.com",
		Type: "A",
		IP:   "8.8.4.4",
		TTL:  3600,
	}); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	lookup := func(ctx context.Context, host string) ([]string, error) {
		calls.Add(1)
		return []string{"9.9.9.9"}, nil
	}

	// Без Force свежий хост пропускается, но его IP попадает в LiveIPs.
	res := ResolveHosts(context.Background(), []string{"rr1---sn-aaa111.googlevideo.com"}, ResolveConfig{
		Collector:  col,
		LookupIPv4: lookup,
		Now:        func() time.Time { return now },
	})
	if calls.Load() != 0 || res.Checked != 0 || res.Resolved != 0 {
		t.Fatalf("expected fresh host skip, got calls=%d res=%+v", calls.Load(), res)
	}
	if !reflect.DeepEqual(res.LiveIPs, []string{"8.8.4.4"}) {
		t.Fatalf("LiveIPs = %v, want [8.8.4.4]", res.LiveIPs)
	}

	// С Force=true хост резолвится повторно.
	res = ResolveHosts(context.Background(), []string{"rr1---sn-aaa111.googlevideo.com"}, ResolveConfig{
		Collector:  col,
		LookupIPv4: lookup,
		Force:      true,
		Now:        func() time.Time { return now },
	})
	if calls.Load() != 1 || res.Checked != 1 || res.Resolved != 1 {
		t.Fatalf("expected forced resolve, got calls=%d res=%+v", calls.Load(), res)
	}
	if !reflect.DeepEqual(res.IPs, []string{"9.9.9.9"}) || !reflect.DeepEqual(res.LiveIPs, []string{"8.8.4.4", "9.9.9.9"}) {
		t.Fatalf("forced IPs mismatch: %+v", res)
	}
}

func TestResolveHosts_MaxHostsCap(t *testing.T) {
	hosts := make([]string, 10)
	for i := range hosts {
		hosts[i] = fmt.Sprintf("rr%d---sn-test%d.googlevideo.com", i+1, i)
	}
	var calls atomic.Int32
	res := ResolveHosts(context.Background(), hosts, ResolveConfig{
		MaxHosts:    3,
		Concurrency: 2,
		LookupIPv4: func(ctx context.Context, host string) ([]string, error) {
			calls.Add(1)
			return []string{"1.1.1.1"}, nil
		},
	})
	if res.Checked != 3 || calls.Load() != 3 {
		t.Fatalf("MaxHosts cap failed: checked=%d calls=%d", res.Checked, calls.Load())
	}
}

package googlevideo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMostCommonStrategy(t *testing.T) {
	locks := []AutoLock{
		{Askey: "2", Host: "sn-a", Strategy: 41},
		{Askey: "2", Host: "sn-b", Strategy: 41},
		{Askey: "2", Host: "sn-c", Strategy: 16},
		{Askey: "2", Host: "sn-seed-a", Strategy: 44, Source: SourceSeed},
		{Askey: "2", Host: "sn-seed-b", Strategy: 44, Source: SourceSeed},
		{Askey: "2", Host: "sn-seed-c", Strategy: 44, Source: SourceSeed},
	}
	s, n := MostCommonStrategy(locks)
	if s != 41 || n != 2 {
		t.Fatalf("got strat=%d n=%d", s, n)
	}
}

func TestMissingHostKeysAndCandidates(t *testing.T) {
	hosts := []string{
		"rr1---sn-abc.googlevideo.com",
		"rr2---sn-abc.googlevideo.com",
		"rr3---sn-xyz.googlevideo.com",
	}
	locks := []AutoLock{{Askey: "2", Scope: "default", Host: "sn-abc", Strategy: 41}}
	miss := MissingHostKeys(hosts, locks)
	if len(miss) != 1 || miss[0] != "sn-xyz" {
		t.Fatalf("missing=%v", miss)
	}
	cands := CandidateStrategies(locks, 41, 44, 3)
	if len(cands) < 2 || cands[0] != 41 {
		t.Fatalf("cands=%v", cands)
	}
	for _, candidate := range cands {
		if candidate == 44 {
			t.Fatalf("unverified seed incorrectly ranked as probe evidence: %v", cands)
		}
	}
}

func TestUpsertAutoLockSeedDoesNotOverwriteLearned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auto_locked.tsv")
	learned := AutoLock{Askey: "2", Scope: "default", Host: "sn-abc", Strategy: 16, Source: SourceLearned}
	if err := UpsertAutoLock(path, learned); err != nil {
		t.Fatal(err)
	}
	seed := AutoLock{Askey: "2", Scope: "default", Host: "sn-abc", Strategy: 41, Source: SourceSeed}
	if err := UpsertAutoLock(path, seed); err != nil {
		t.Fatal(err)
	}
	got, err := ParseAutoLockedTSV(path)
	if err != nil || len(got) != 1 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if got[0].Strategy != 16 {
		t.Fatalf("seed overwrote learned: %+v", got[0])
	}
}

func TestParseAutoLockedWithSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auto_locked.tsv")
	body := "2\tsn-a\t41\tseed\n2\tdefault\tsn-b\t16\tlearned\n2\tsn-c\t7\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ParseAutoLockedTSV(path)
	if err != nil || len(got) != 3 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	byHost := map[string]AutoLock{}
	for _, l := range got {
		byHost[l.Host] = l
	}
	if byHost["sn-a"].Source != SourceSeed || byHost["sn-a"].Strategy != 41 {
		t.Fatalf("sn-a=%+v", byHost["sn-a"])
	}
	if byHost["sn-b"].Source != SourceLearned || byHost["sn-b"].Scope != "default" {
		t.Fatalf("sn-b=%+v", byHost["sn-b"])
	}
	if byHost["sn-c"].Strategy != 7 {
		t.Fatalf("sn-c=%+v", byHost["sn-c"])
	}
}

func TestSeederTransferWithoutProbe(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.tsv")
	state := filepath.Join(dir, "state.tsv")
	// majority 41
	_ = UpsertAutoLock(live, AutoLock{Askey: "2", Host: "sn-old1", Strategy: 41, Source: SourceLearned})
	_ = UpsertAutoLock(live, AutoLock{Askey: "2", Host: "sn-old2", Strategy: 41, Source: SourceLearned})

	hostsFile := filepath.Join(dir, "hosts.txt")
	_ = os.WriteFile(hostsFile, []byte("rr1---sn-new.googlevideo.com\n"), 0o644)

	s := NewSeeder(SeederConfig{
		LivePath:  live,
		StatePath: state,
		ListHosts: func() ([]string, error) {
			return []string{"rr1---sn-new.googlevideo.com"}, nil
		},
		Probe:    nil,
		Interval: time.Hour,
		TailMax:  2,
	})
	s.tick(context.Background())
	got, _ := ParseAutoLockedTSV(live)
	found := false
	for _, l := range got {
		if l.Host == "sn-new" && l.Strategy == 41 && l.Source == SourceSeed {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected seed for sn-new: %+v", got)
	}
	backup, err := ParseAutoLockedTSV(state)
	if err != nil || len(backup) != 1 || backup[0].Host != "sn-new" || backup[0].Source != SourceSeed {
		t.Fatalf("seed was not persisted: locks=%+v err=%v", backup, err)
	}
}

func TestSeederDoesNotTransferAcrossScopes(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.tsv")
	for _, host := range []string{"sn-work-a", "sn-work-b"} {
		if err := UpsertAutoLock(live, AutoLock{Askey: "2", Scope: "work", Host: host, Strategy: 41, Source: SourceLearned}); err != nil {
			t.Fatal(err)
		}
	}
	s := NewSeeder(SeederConfig{LivePath: live, ListHosts: func() ([]string, error) {
		return []string{"rr1---sn-new.googlevideo.com"}, nil
	}})
	s.tick(context.Background())
	got, err := ParseAutoLockedTSV(live)
	if err != nil {
		t.Fatal(err)
	}
	for _, lock := range got {
		if lock.Host == "sn-new" {
			t.Fatalf("non-default scope produced a default seed: %+v", got)
		}
	}
	if status := s.Status(); status.LastResult != "unknown" || status.LastError != "probe_unavailable" {
		t.Fatalf("status = %+v", status)
	}
}

func TestSeederReportsStateWriteFailureBeforeLiveChange(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.tsv")
	for _, host := range []string{"sn-a", "sn-b"} {
		if err := UpsertAutoLock(live, AutoLock{Askey: "2", Host: host, Strategy: 41, Source: SourceLearned}); err != nil {
			t.Fatal(err)
		}
	}
	notDirectory := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(notDirectory, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSeeder(SeederConfig{LivePath: live, StatePath: filepath.Join(notDirectory, "state.tsv"), ListHosts: func() ([]string, error) {
		return []string{"rr1---sn-new.googlevideo.com"}, nil
	}})
	s.tick(context.Background())
	got, err := ParseAutoLockedTSV(live)
	if err != nil {
		t.Fatal(err)
	}
	for _, lock := range got {
		if lock.Host == "sn-new" {
			t.Fatalf("live seed was written after state persistence failed: %+v", got)
		}
	}
	if status := s.Status(); status.LastResult != "error" || status.LastError == "" {
		t.Fatalf("persistence error was hidden: %+v", status)
	}
}

func TestSeederWithoutProbeDoesNotWriteUntestedDefault(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.tsv")
	_ = UpsertAutoLock(live, AutoLock{Askey: "2", Host: "sn-seed1", Strategy: 41, Source: SourceSeed})
	_ = UpsertAutoLock(live, AutoLock{Askey: "2", Host: "sn-seed2", Strategy: 41, Source: SourceSeed})
	s := NewSeeder(SeederConfig{LivePath: live, ListHosts: func() ([]string, error) {
		return []string{"rr1---sn-new.googlevideo.com"}, nil
	}})
	s.tick(context.Background())
	got, err := ParseAutoLockedTSV(live)
	if err != nil {
		t.Fatalf("unverified seed was written: locks=%+v err=%v", got, err)
	}
	for _, lock := range got {
		if lock.Host == "sn-new" {
			t.Fatalf("unverified seed was written: locks=%+v", got)
		}
	}
	status := s.Status()
	if status.LastResult != "unknown" || status.LastError != "probe_unavailable" {
		t.Fatalf("status = %+v", status)
	}
}

func TestSeederProbeWritesLearnedSource(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.tsv")
	s := NewSeeder(SeederConfig{LivePath: live, DefaultStrat: 41, MaxStrat: 44,
		ListHosts: func() ([]string, error) { return []string{"rr1---sn-new.googlevideo.com"}, nil },
		Probe:     func(context.Context, string, int) (bool, string, error) { return true, "media verified", nil },
	})
	s.tick(context.Background())
	got, err := ParseAutoLockedTSV(live)
	if err != nil || len(got) != 1 || got[0].Source != SourceLearned || got[0].Strategy != 41 {
		t.Fatalf("verified result = %+v err=%v", got, err)
	}
}

func TestSeederBatchProbeWritesOnlyReturnedCandidate(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.tsv")
	var tried []int
	s := NewSeeder(SeederConfig{
		LivePath: live, DefaultStrat: 41, MaxStrat: 44, TailMax: 8, PreferredStrats: []int{16},
		ListHosts: func() ([]string, error) { return []string{"rr1---sn-new.googlevideo.com"}, nil },
		ProbeBatch: func(_ context.Context, fqdn string, candidates []int, onTry func(int)) (int, string, error) {
			if fqdn != "rr1---sn-new.googlevideo.com" || len(candidates) == 0 {
				t.Fatalf("batch target/candidates = %q/%v", fqdn, candidates)
			}
			for _, candidate := range candidates {
				onTry(candidate)
				tried = append(tried, candidate)
				if candidate == 16 {
					return candidate, "TLS and download verified", nil
				}
			}
			return 0, "no candidate passed", nil
		},
	})
	s.tick(context.Background())
	got, err := ParseAutoLockedTSV(live)
	if err != nil || len(got) != 1 || got[0].Strategy != 16 || got[0].Source != SourceLearned {
		t.Fatalf("batch result = %+v err=%v", got, err)
	}
	if len(tried) != 2 || tried[0] != 41 || tried[1] != 16 {
		t.Fatalf("tried strategies = %v", tried)
	}
	if status := s.Status(); status.LastResult != "ok" || status.LastStrat != 16 || status.Pending != 0 {
		t.Fatalf("status = %+v", status)
	}
}

func TestSeederBatchProbeFailureDoesNotWriteLockAndIsRateLimited(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.tsv")
	calls := 0
	s := NewSeeder(SeederConfig{
		LivePath:  live,
		ListHosts: func() ([]string, error) { return []string{"rr1---sn-new.googlevideo.com"}, nil },
		ProbeBatch: func(context.Context, string, []int, func(int)) (int, string, error) {
			calls++
			return 0, "no strategy passed", nil
		},
	})
	s.tick(context.Background())
	s.tick(context.Background())
	got, err := ParseAutoLockedTSV(live)
	if err != nil || len(got) != 0 {
		t.Fatalf("failed batch wrote a lock: %+v err=%v", got, err)
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d, want one within retry interval", calls)
	}
	if status := s.Status(); status.LastResult != "fail" || status.LastError != "no strategy passed" {
		t.Fatalf("status = %+v", status)
	}
}

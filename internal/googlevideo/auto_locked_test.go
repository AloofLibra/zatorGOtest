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
	// majority 41
	_ = UpsertAutoLock(live, AutoLock{Askey: "2", Host: "sn-old1", Strategy: 41, Source: SourceLearned})
	_ = UpsertAutoLock(live, AutoLock{Askey: "2", Host: "sn-old2", Strategy: 41, Source: SourceLearned})

	hostsFile := filepath.Join(dir, "hosts.txt")
	_ = os.WriteFile(hostsFile, []byte("rr1---sn-new.googlevideo.com\n"), 0o644)

	s := NewSeeder(SeederConfig{
		LivePath: live,
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
}
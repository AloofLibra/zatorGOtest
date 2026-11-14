package googlevideo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAutoLockedTSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auto_locked.tsv")
	body := "2\tsn-2oi5h5-54\t41\n2\tdefault\tsn-q4fl6nd6\t17\n# comment\nbad\n1\thost\t3\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ParseAutoLockedTSV(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3: %+v", len(got), got)
	}
	gv := FilterAskey(got, "2")
	if len(gv) != 2 {
		t.Fatalf("filter askey=2 len=%d", len(gv))
	}
	merged := MergeAutoLocked(
		[]AutoLock{{Askey: "2", Scope: "default", Host: "sn-x", Strategy: 40}},
		[]AutoLock{{Askey: "2", Scope: "default", Host: "sn-x", Strategy: 41}, {Askey: "2", Scope: "default", Host: "sn-y", Strategy: 17}},
	)
	if len(merged) != 2 {
		t.Fatalf("merge len=%d", len(merged))
	}
	for _, l := range merged {
		if l.Host == "sn-x" && l.Strategy != 40 {
			t.Fatalf("live should win: %+v", l)
		}
	}
}

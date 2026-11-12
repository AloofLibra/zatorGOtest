package firewall

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRenderQueueHook(t *testing.T) {
	s := RenderQueueHook(DefaultQueueHook())
	for _, want := range []string{"NFQUEUE", "ZATOR_NFQ", "start_rules", "--queue-num"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestWriteQueueHook(t *testing.T) {
	root := t.TempDir()
	p, err := WriteQueueHook(root, DefaultQueueHook())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != QueueHookName {
		t.Fatal(p)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// На Windows бит execute из os.Chmod часто не сохраняется.
	if runtime.GOOS != "windows" && st.Mode()&0o111 == 0 {
		t.Fatalf("not executable: %v", st.Mode())
	}
}

func TestSanitizePorts(t *testing.T) {
	got := sanitizePorts("80,443,1-2-3,2053")
	if got != "80,443,2053" && !strings.HasPrefix(got, "80,443") {
		t.Fatal(got)
	}
}

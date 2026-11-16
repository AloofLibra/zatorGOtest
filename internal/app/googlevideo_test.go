package app

import (
	"os"
	"path/filepath"
	"testing"
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

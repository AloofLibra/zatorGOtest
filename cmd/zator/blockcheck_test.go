package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteBlockcheckSummary(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "blockcheck.log")
	summaryPath := filepath.Join(dir, "summary.txt")
	log := "preamble\n* SUMMARY\nTLS 1.2: result\nTLS 1.3: result\n* COMMON\nignored\n"
	if err := os.WriteFile(logPath, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if !writeBlockcheckSummary(logPath, summaryPath) {
		t.Fatal("summary not extracted")
	}
	got, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := "* SUMMARY\nTLS 1.2: result\nTLS 1.3: result\n"; string(got) != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

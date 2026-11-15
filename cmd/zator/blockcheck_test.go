package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestWaitForState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls := 0
	if err := waitForState(ctx, false, func() bool {
		calls++
		return calls < 3
	}); err != nil {
		t.Fatal(err)
	}
	if calls < 3 {
		t.Fatalf("state was not polled until stopped: %d polls", calls)
	}
}

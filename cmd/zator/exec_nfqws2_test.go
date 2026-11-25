package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AloofLibra/zator/internal/config"
)

func TestCmdExecNfqws2PassesFullArgsBeyond16KB(t *testing.T) {
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "nfqws2")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stock, err := config.Load(filepath.Join("..", "..", "assets", "config.default"))
	if err != nil {
		t.Fatalf("load config.default: %v", err)
	}
	argsPath := filepath.Join(tmp, "nfqws2.args")
	if err := stock.WriteNfqws2RuntimeArgs(argsPath, "/opt/zapret2/lua", "nobody"); err != nil {
		t.Fatalf("WriteNfqws2RuntimeArgs: %v", err)
	}
	raw, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= 16384 {
		t.Fatalf("expected nfqws2.args > 16384 bytes to cover MAX_CONFIG_FILE_SIZE bypass, got %d", len(raw))
	}

	var gotBin string
	var gotArgv []string
	orig := execReplaceFn
	execReplaceFn = func(bin string, argv []string) error {
		gotBin = bin
		gotArgv = append([]string(nil), argv...)
		return nil
	}
	t.Cleanup(func() { execReplaceFn = orig })

	var stderr bytes.Buffer
	code := cmdExecNfqws2([]string{fakeBin, argsPath}, &stderr)
	if code != 0 {
		t.Fatalf("cmdExecNfqws2 returned %d, stderr=%s", code, stderr.String())
	}
	if gotBin != fakeBin {
		t.Fatalf("bin = %q, want %q", gotBin, fakeBin)
	}
	if len(gotArgv) <= 400 || gotArgv[0] != fakeBin {
		t.Fatalf("unexpected argv len=%d first=%q", len(gotArgv), gotArgv[0])
	}

	joined := strings.Join(gotArgv, "\n")
	for _, want := range []string{
		"--lua-desync=wssize_ramp:mode=lite:wsize=1:scale=1:open=1",
		"--filter-udp=443",
		"--filter-l7=discord,stun",
		"--filter-l7=wireguard",
		"--lua-desync=circular_quality:key=9:",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected flag %q missing in argv (possible truncation)", want)
		}
	}
}

func TestCmdExecNfqws2ValidationAndErrors(t *testing.T) {
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "nfqws2")
	if err := os.WriteFile(fakeBin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	validArgs := filepath.Join(tmp, "ok.args")
	if err := os.WriteFile(validArgs, []byte("--qnum=300\n--filter-tcp=443\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	called := false
	orig := execReplaceFn
	execReplaceFn = func(string, []string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { execReplaceFn = orig })

	var stderr bytes.Buffer
	if code := cmdExecNfqws2([]string{fakeBin}, &stderr); code != 2 {
		t.Fatalf("1 arg code = %d, want 2", code)
	}
	stderr.Reset()
	if code := cmdExecNfqws2([]string{"relative/nfqws2", validArgs}, &stderr); code != 2 {
		t.Fatalf("relative bin code = %d, want 2", code)
	}
	stderr.Reset()
	if code := cmdExecNfqws2([]string{fakeBin, "relative.args"}, &stderr); code != 2 {
		t.Fatalf("relative args code = %d, want 2", code)
	}
	stderr.Reset()
	if code := cmdExecNfqws2([]string{filepath.Join(tmp, "missing-bin"), validArgs}, &stderr); code != 1 {
		t.Fatalf("missing bin code = %d, want 1", code)
	}
	stderr.Reset()
	if code := cmdExecNfqws2([]string{tmp, validArgs}, &stderr); code != 1 {
		t.Fatalf("directory bin code = %d, want 1", code)
	}

	dupArgs := filepath.Join(tmp, "dup.args")
	if err := os.WriteFile(dupArgs, []byte("--qnum=300\n--new\n--new\n--filter-udp=443\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := cmdExecNfqws2([]string{fakeBin, dupArgs}, &stderr); code != 1 {
		t.Fatalf("duplicate --new args code = %d, want 1", code)
	}

	metaArgs := filepath.Join(tmp, "meta.args")
	if err := os.WriteFile(metaArgs, []byte("--qnum=300\n--filter-tcp=80;evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := cmdExecNfqws2([]string{fakeBin, metaArgs}, &stderr); code != 1 {
		t.Fatalf("shell metachar args code = %d, want 1", code)
	}
	if called {
		t.Fatal("execReplaceFn must not be called on invalid inputs")
	}
}

package config

import (
	"strings"
	"testing"
)

func TestSyncWssizeRampVarsEnabled(t *testing.T) {
	c := Parse(`Z2R_WSSIZE_RAMP_ENABLE=1
Z2R_WSSIZE_RAMP_MODE=lite
Z2R_WSSIZE_RAMP_WSIZE=1
Z2R_WSSIZE_RAMP_SCALE=1
Z2R_WSSIZE_RAMP_OPEN=1
Z2R_WSSIZE_RAMP_ARGS=
Z2R_WSSIZE_RAMP_SKIP=--skip
`)
	ok, err := c.SyncWssizeRampVars()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected change")
	}
	if v, _ := c.Get("Z2R_WSSIZE_RAMP_ARGS"); v != ":mode=lite:wsize=1:scale=1:open=1" {
		t.Fatalf("ARGS=%q", v)
	}
	if v, _ := c.Get("Z2R_WSSIZE_RAMP_SKIP"); v != "" {
		t.Fatalf("SKIP=%q, want empty", v)
	}
}

func TestSyncWssizeRampVarsDisabled(t *testing.T) {
	c := Parse(`Z2R_WSSIZE_RAMP_ENABLE=0
Z2R_WSSIZE_RAMP_MODE=full
Z2R_WSSIZE_RAMP_ARGS=:mode=full:wsize=1:scale=1:open=1
Z2R_WSSIZE_RAMP_SKIP=
`)
	ok, err := c.SyncWssizeRampVars()
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if v, _ := c.Get("Z2R_WSSIZE_RAMP_ARGS"); v != "" {
		t.Fatalf("ARGS=%q", v)
	}
	if v, _ := c.Get("Z2R_WSSIZE_RAMP_SKIP"); v != "--skip" {
		t.Fatalf("SKIP=%q", v)
	}
}

func TestStripWssizeShellIf(t *testing.T) {
	c := Parse(`Z2R_WSSIZE_RAMP_ENABLE=1
Z2R_WSSIZE_RAMP_ARGS=
Z2R_WSSIZE_RAMP_SKIP=--skip
if [ "$Z2R_WSSIZE_RAMP_ENABLE" = 1 ]; then
  Z2R_WSSIZE_RAMP_ARGS=":mode=$Z2R_WSSIZE_RAMP_MODE:wsize=1:scale=1:open=1"
  Z2R_WSSIZE_RAMP_SKIP=
fi
NFQWS2_UDP_PKT_OUT=5
`)
	if !c.StripWssizeShellIf() {
		t.Fatal("expected strip")
	}
	s := c.String()
	if strings.Contains(s, "if [") || strings.Contains(s, "\nfi\n") {
		t.Fatalf("shell if remains:\n%s", s)
	}
	if !strings.Contains(s, "NFQWS2_UDP_PKT_OUT=5") {
		t.Fatalf("lost later vars:\n%s", s)
	}
	if c.StripWssizeShellIf() {
		t.Fatal("second strip should be no-op")
	}
}

func TestEnsureProfile2SoftSuccessBumpsLegacyBytes(t *testing.T) {
	c := Parse(`NFQWS2_OPT="
--lua-desync=circular_quality:key=2:soft_success_in=0:soft_success_bytes=32768:exclude_substrings=x
"
`)
	ok, err := c.EnsureProfile2SoftSuccess()
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	opt, _ := c.NFQWS2Opt()
	if !strings.Contains(opt, "soft_success_bytes=65536") || strings.Contains(opt, "soft_success_bytes=32768") {
		t.Fatalf("opt=%s", opt)
	}
}

func TestEnsureProfile2SoftSuccess(t *testing.T) {
	c := Parse(`NFQWS2_OPT="
--in-range=-s32768 --lua-desync=circular_quality:key=2:fails=2:hostkey=slm_desync_googlevideo_hostkey:default_strategy=41:exclude_substrings=/opt/zator/lists/netrogat_substrings.txt:payload=tls_client_hello
"
`)
	ok, err := c.EnsureProfile2SoftSuccess()
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	opt, _ := c.NFQWS2Opt()
	if !strings.Contains(opt, "soft_success_in=0") || !strings.Contains(opt, "soft_success_bytes=65536") {
		t.Fatalf("opt=%s", opt)
	}
	ok, err = c.EnsureProfile2SoftSuccess()
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("second call should be no-op")
	}
}

func TestEnsureProfile2SoftSuccessOverridesPacketThreshold(t *testing.T) {
	c := Parse(`NFQWS2_OPT="
--lua-desync=circular_quality:key=2:soft_success_in=3:exclude_substrings=x
"
`)
	ok, err := c.EnsureProfile2SoftSuccess()
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	opt, _ := c.NFQWS2Opt()
	if strings.Contains(opt, "soft_success_in=3") {
		t.Fatalf("still soft_success_in=3: %s", opt)
	}
	if !strings.Contains(opt, "soft_success_in=0:soft_success_bytes=65536") {
		t.Fatalf("opt=%s", opt)
	}
}

func TestEnsureProfile2SoftSuccessLockedLine(t *testing.T) {
	c := Parse(`NFQWS2_OPT="
--in-range=-s32768 --lua-desync=circular_locked:key=2:exclude_substrings=/opt/zator/lists/netrogat_substrings.txt:payload=tls_client_hello
"
`)
	ok, err := c.EnsureProfile2SoftSuccess()
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	opt, _ := c.NFQWS2Opt()
	if !strings.Contains(opt, "circular_locked:key=2:soft_success_in=0:soft_success_bytes=65536:exclude_substrings=") {
		t.Fatalf("opt=%s", opt)
	}
	ok, err = c.EnsureProfile2SoftSuccess()
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("second call should be no-op")
	}
}

func TestBuildNfqws2ArgsWssizeEnabledNoSkip(t *testing.T) {
	c := Parse(`Z2R_WSSIZE_RAMP_ENABLE=1
Z2R_WSSIZE_MARK=0x04000000
Z2R_WSSIZE_RAMP_ARGS=:mode=lite:wsize=1:scale=1:open=1
Z2R_WSSIZE_RAMP_SKIP=
NFQWS2_OPT="
${Z2R_WSSIZE_RAMP_SKIP} --filter-l3=ipv4 --filter-tcp=443 --filter-mark=${Z2R_WSSIZE_MARK}/${Z2R_WSSIZE_MARK}
--lua-desync=wssize_ramp${Z2R_WSSIZE_RAMP_ARGS}
"
`)
	if _, err := c.SyncWssizeRampVars(); err != nil {
		t.Fatal(err)
	}
	args := c.BuildNfqws2Args()
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--skip") {
		t.Fatalf("unexpected --skip: %v", args)
	}
	if !strings.Contains(joined, "--lua-desync=wssize_ramp:mode=lite:wsize=1:scale=1:open=1") {
		t.Fatalf("missing wssize args: %v", args)
	}
}

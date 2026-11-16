package config

import (
	"strings"
	"testing"
)

func TestEnsureProfile2AllowNoHost(t *testing.T) {
	c := Parse(`NFQWS2_OPT="
#Z2R_AUTO_STANDARD_2_BEGIN
--in-range=-s32768 --lua-desync=circular_locked:key=2:soft_success_in=0:exclude_substrings=x
#Z2R_AUTO_STANDARD_2_END
"
`)
	changed, err := c.EnsureProfile2AllowNoHost()
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	opt, _ := c.NFQWS2Opt()
	if !strings.Contains(opt, "circular_locked:key=2:allow_nohost=1:soft_success_in=0:exclude_substrings=x") {
		t.Fatalf("profile 2 args = %s", opt)
	}
	changed, err = c.EnsureProfile2AllowNoHost()
	if err != nil || changed {
		t.Fatalf("second call changed=%v err=%v", changed, err)
	}
}

func TestEnsureProfile2AllowNoHostPreservesOtherHandlers(t *testing.T) {
	c := Parse(`NFQWS2_OPT="
#Z2R_AUTO_STANDARD_2_BEGIN
--lua-desync=circular_quality:key=2
#Z2R_AUTO_STANDARD_2_END
#Z2R_AUTO_STANDARD_3_BEGIN
--lua-desync=circular_locked:key=3
#Z2R_AUTO_STANDARD_3_END
"
`)
	changed, err := c.EnsureProfile2AllowNoHost()
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
}

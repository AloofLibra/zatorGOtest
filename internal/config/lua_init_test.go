package config

import (
	"strings"
	"testing"
)

func TestEnsureLuaInit(t *testing.T) {
	c := Parse(`NFQWS2_OPT="
--lua-init=@/opt/zator/lua/locked.lua
--lua-init=@/opt/zator/lua/wssize-ramp.lua
--new
"
`)
	ok, err := c.EnsureLuaInit("/opt/zator/lua/googlevideo-collect.lua")
	if err != nil || !ok {
		t.Fatalf("first insert: ok=%v err=%v", ok, err)
	}
	opt, _ := c.NFQWS2Opt()
	if !strings.Contains(opt, "--lua-init=@/opt/zator/lua/googlevideo-collect.lua") {
		t.Fatalf("missing line: %s", opt)
	}
	// after wssize-ramp
	idxW := strings.Index(opt, "wssize-ramp.lua")
	idxG := strings.Index(opt, "googlevideo-collect.lua")
	if idxW < 0 || idxG < idxW {
		t.Fatalf("order wrong: %s", opt)
	}
	ok, err = c.EnsureLuaInit("/opt/zator/lua/googlevideo-collect.lua")
	if err != nil || ok {
		t.Fatalf("second insert should be no-op: ok=%v err=%v", ok, err)
	}
}

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

func TestEnsureLuaInitReplacesStaleRootAndDeduplicates(t *testing.T) {
	c := Parse(`NFQWS2_OPT="
--lua-init=@/opt/zator-go/lua/wssize-ramp.lua
--lua-init=@/opt/zator-go/lua/googlevideo-collect.lua
--lua-init=@/opt/zator-go/lua/wssize-ramp.lua
--new
"
`)
	for _, name := range []string{"wssize-ramp.lua", "googlevideo-collect.lua"} {
		if changed, err := c.EnsureLuaInit("/opt/zator/lua/" + name); err != nil || !changed {
			t.Fatalf("replace %s: changed=%t err=%v", name, changed, err)
		}
	}
	opt, _ := c.NFQWS2Opt()
	for _, name := range []string{"wssize-ramp.lua", "googlevideo-collect.lua"} {
		if strings.Count(opt, "--lua-init=@/opt/zator/lua/"+name) != 1 {
			t.Errorf("%s should have one current-root init: %s", name, opt)
		}
		if strings.Contains(opt, "--lua-init=@/opt/zator-go/lua/"+name) {
			t.Errorf("%s still points at stale root: %s", name, opt)
		}
	}
	if changed, err := c.EnsureLuaInit("/opt/zator/lua/wssize-ramp.lua"); err != nil || changed {
		t.Fatalf("repeat should be no-op: changed=%t err=%v", changed, err)
	}
}

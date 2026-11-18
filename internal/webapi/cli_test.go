package webapi

import (
	"context"
	"testing"

	"github.com/AloofLibra/zator/internal/orchestra"
)

func TestCLIControlsUseSharedOperations(t *testing.T) {
	e := newEnv(t, envOpts{})
	if _, err := e.srv.CLISetSetting(context.Background(), "fallback", map[string]string{"value": "1", "restart": "0"}); err != nil {
		t.Fatal(err)
	}
	setting, err := e.srv.CLISetting(context.Background(), "fallback")
	if err != nil || setting.(map[string]any)["enabled"] != true {
		t.Fatalf("fallback setting=%v err=%v", setting, err)
	}
	if _, err := e.srv.CLISetSetting(context.Background(), "fallback", map[string]string{"value": "0", "restart": "0"}); err != nil {
		t.Fatal(err)
	}
	n := 3
	if _, err := e.srv.CLISetProfileLock(context.Background(), orchestra.ScopeDefault, 1, &n); err != nil {
		t.Fatal(err)
	}
	if got, err := e.srv.Store().GetLock(orchestra.ScopeDefault, "1", "tls"); err != nil || got != orchestra.Strategy(3) {
		t.Fatalf("lock=%v err=%v", got, err)
	}
	if _, err := e.srv.CLISetProfileLock(context.Background(), orchestra.ScopeDefault, 1, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.srv.Store().GetLock(orchestra.ScopeDefault, "1", "tls"); !got.IsAuto() {
		t.Fatalf("lock after clear=%v", got)
	}
	if _, err := e.srv.CLISetProfileLock(context.Background(), orchestra.ScopeDefault, 1, intPtr(999)); err == nil {
		t.Fatal("out-of-range strategy accepted")
	}

	if _, err := e.srv.CLIDomains(context.Background(), "netrogat", "default", "add", map[string]string{"domain": "example.org"}); err != nil {
		t.Fatal(err)
	}
	list, err := e.srv.CLIDomains(context.Background(), "netrogat", "default", "list", nil)
	if err != nil {
		t.Fatal(err)
	}
	items := list["items"].([]map[string]any)
	found := false
	for _, item := range items {
		if item["value"] == "example.org" {
			found = true
		}
	}
	if !found {
		t.Fatalf("new domain absent from list: %v", items)
	}
	if _, err := e.srv.CLIDomains(context.Background(), "netrogat", "default", "remove", map[string]string{"domain": "example.org"}); err != nil {
		t.Fatal(err)
	}
	list, err = e.srv.CLIDomains(context.Background(), "netrogat", "default", "list", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list["items"].([]map[string]any) {
		if item["value"] == "example.org" {
			t.Fatal("removed domain remains")
		}
	}
}

func intPtr(v int) *int { return &v }

func TestCLISupersweepUsesExistingCoordinator(t *testing.T) {
	e := newEnv(t, envOpts{})
	if _, err := e.srv.CLISupersweep(context.Background(), "start", map[string]string{"quick_profile": "3"}); err != nil {
		t.Fatal(err)
	}
	if !e.sweeper.started {
		t.Fatal("CLI start did not start the configured coordinator")
	}
	state, err := e.srv.CLISupersweep(context.Background(), "status", nil)
	if err != nil {
		t.Fatal(err)
	}
	if state["running"] != true {
		t.Fatalf("running=%v", state["running"])
	}
	if _, err := e.srv.CLISupersweep(context.Background(), "cancel", nil); err != nil {
		t.Fatal(err)
	}
}

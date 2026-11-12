package firewall

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseBackend(t *testing.T) {
	for in, want := range map[string]Backend{
		"iptables": BackendIptables, "nftables": BackendNftables, "nft": BackendNftables,
		` "NFTables" `: BackendNftables, "IPTABLES": BackendIptables,
	} {
		got, ok := ParseBackend(in)
		if !ok || got != want {
			t.Errorf("ParseBackend(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "auto", "pf"} {
		if got, ok := ParseBackend(in); ok || got != BackendNone {
			t.Errorf("ParseBackend(%q) = %q,%v want none", in, got, ok)
		}
	}
}

func TestDetectBackend(t *testing.T) {
	tests := []struct {
		name       string
		bins       []string
		configured string
		want       Backend
		wantErr    error
	}{
		{"config iptables", []string{"iptables", "nft"}, "iptables", BackendIptables, nil},
		{"config nftables", []string{"iptables", "nft"}, "nftables", BackendNftables, nil},
		{"config wins over fw4", []string{"iptables", "nft", "fw4"}, "iptables", BackendIptables, nil},
		{"config binary missing", []string{"iptables"}, "nftables", BackendNone, ErrBackendUnavailable},
		{"auto only nft", []string{"nft"}, "", BackendNftables, nil},
		{"auto only iptables", []string{"iptables"}, "auto", BackendIptables, nil},
		{"auto both prefers iptables", []string{"iptables", "nft"}, "", BackendIptables, nil},
		{"auto both with fw4 prefers nft", []string{"iptables", "nft", "fw4"}, "", BackendNftables, nil},
		{"none", nil, "", BackendNone, ErrNoBackend},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetectBackend(NewRecordingRunner(tt.bins...), tt.configured)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("backend = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBackendFromGetter(t *testing.T) {
	r := NewRecordingRunner("iptables", "nft")
	b, err := BackendFromGetter(r, mapGetter(map[string]string{"FWTYPE": "nftables"}))
	if err != nil || b != BackendNftables {
		t.Fatalf("got %q, %v", b, err)
	}
	if b.Binary() != "nft" || BackendIptables.Binary() != "iptables" || BackendNone.Binary() != "" {
		t.Error("Binary() mismatch")
	}
}

func TestRecordingRunner(t *testing.T) {
	r := &RecordingRunner{}
	if _, err := r.LookPath("anything"); err != nil {
		t.Errorf("zero value must report all binaries as available: %v", err)
	}
	r.Handler = func(c Command) ([]byte, error) {
		if c.Name == "bad" {
			return []byte("boom"), ErrExit1()
		}
		return []byte("ok"), nil
	}
	ctx := context.Background()
	if out, err := r.Run(ctx, Command{Name: "good", Args: []string{"a b", "c"}}); err != nil || string(out) != "ok" {
		t.Fatalf("good: %q %v", out, err)
	}
	_, err := r.Run(ctx, Command{Name: "bad"})
	var ce *CommandError
	if !errors.As(err, &ce) || ce.Output != "boom" {
		t.Fatalf("bad: %v", err)
	}
	want := []string{"good 'a b' c", "bad"}
	if got := r.Lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("Lines = %v, want %v", got, want)
	}
	r.Reset()
	if len(r.Calls()) != 0 {
		t.Error("Reset did not clear calls")
	}
	if _, err := NewRecordingRunner("nft").LookPath("iptables"); err == nil {
		t.Error("iptables must be missing")
	}
}

func TestSplitArgs(t *testing.T) {
	got := splitArgs(`-s 1.2.3.4/32 -m comment --comment "zator client scope" -j MARK --set-xmark 0x100/0xff00`)
	want := []string{"-s", "1.2.3.4/32", "-m", "comment", "--comment", "zator client scope", "-j", "MARK", "--set-xmark", "0x100/0xff00"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("splitArgs = %q", got)
	}
	if got := splitArgs(`--comment "a \"b\""`); !reflect.DeepEqual(got, []string{"--comment", `a "b"`}) {
		t.Errorf("escaped quotes: %q", got)
	}
}

func TestCommandsScript(t *testing.T) {
	s := CommandsScript([]Command{
		{Name: "iptables", Args: []string{"-t", "mangle", "-A", "X", "-m", "mark", "!", "--mark", "0x1/0x1"}},
		{Name: "nft", Args: []string{"-f", "-"}, Stdin: "add table inet t\n"},
		{Name: "echo", Args: []string{"it's"}},
	})
	for _, want := range []string{
		"iptables -t mangle -A X -m mark ! --mark 0x1/0x1\n",
		"nft -f - <<'ZATOR_EOF'\nadd table inet t\nZATOR_EOF\n",
		`echo 'it'\''s'`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
}

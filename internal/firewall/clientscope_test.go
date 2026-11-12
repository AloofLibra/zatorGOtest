package firewall

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

const sampleTSV = "mark:1\t192.168.1.10\n" +
	"mark:2\t192.168.1.20\textra\n" +
	"# comment\n" +
	"\n" +
	"default\t192.168.1.30\n" +
	"mark:0\t192.168.1.31\n" +
	"mark:256\t192.168.1.32\n" +
	"mark:3\t300.1.1.1\n" +
	"mark:4\t192.168.1.10\n" +
	"mark:5\tfd00::5\r\n" +
	"mark:6"

func testScopeConfig(b Backend) ClientScopeConfig {
	c := DefaultClientScopeConfig("/opt/zator")
	c.Enable = true
	c.Backend = b
	c.ReadFile = func(string) ([]byte, error) { return []byte(sampleTSV), nil }
	return c
}

func TestParseClientScopeTSV(t *testing.T) {
	c := testScopeConfig(BackendNftables)
	entries, skipped, err := c.ParseTSV(strings.NewReader(sampleTSV))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Addr.String())
	}
	want := []string{"192.168.1.10", "192.168.1.20", "fd00::5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("addrs = %v, want %v", got, want)
	}
	if entries[0].ID != 1 || entries[0].Mark != 1<<8 || entries[1].Mark != 2<<8 {
		t.Errorf("marks wrong: %+v", entries)
	}
	reasons := map[int]string{}
	for _, s := range skipped {
		reasons[s.Line] = s.Reason
	}
	for _, line := range []int{6, 7, 8, 9, 11} { // mark:0, mark:256, bad ip, duplicate, no ip
		if reasons[line] == "" {
			t.Errorf("line %d should be reported as skipped (%v)", line, skipped)
		}
	}
	if reasons[9] != "duplicate ip" {
		t.Errorf("line 9 reason = %q", reasons[9])
	}
}

func TestClientScopeMarkFor(t *testing.T) {
	c := DefaultClientScopeConfig("/opt/zator")
	if m, err := c.MarkFor(255); err != nil || m != 0xff00 {
		t.Errorf("MarkFor(255) = %#x, %v", m, err)
	}
	for _, id := range []uint32{0, 256} {
		if _, err := c.MarkFor(id); err == nil {
			t.Errorf("MarkFor(%d) must fail", id)
		}
	}
	// Идентификатор, не помещающийся в маску.
	c.Mask, c.Shift, c.Max = 0x100, 2, 255
	if _, err := c.MarkFor(255); err == nil {
		t.Error("id that does not fit the mask must fail")
	}
	if m, err := c.MarkFor(64); err != nil || m != 0x100 {
		t.Errorf("MarkFor(64) = %#x, %v", m, err)
	}
}

func TestClientScopeValidate(t *testing.T) {
	good := DefaultClientScopeConfig("/opt/zator")
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		mutate  func(*ClientScopeConfig)
		wantErr error
	}{
		{"missing mask", func(c *ClientScopeConfig) { c.Mask = 0 }, ErrScopeMaskMissing},
		{"shift too big", func(c *ClientScopeConfig) { c.Shift = 32 }, ErrScopeInvalid},
		{"mask below shift", func(c *ClientScopeConfig) { c.Mask = 0xff01 }, ErrScopeInvalid},
		{"max zero", func(c *ClientScopeConfig) { c.Max = 0 }, ErrScopeInvalid},
		{"bad chain", func(c *ClientScopeConfig) { c.IptablesChain = "a b;" }, ErrScopeInvalid},
		{"bad table", func(c *ClientScopeConfig) { c.IptablesTable = "MANGLE;" }, ErrScopeInvalid},
		{"bad nft chain", func(c *ClientScopeConfig) { c.NftChain = "1bad" }, ErrScopeInvalid},
		{"bad comment", func(c *ClientScopeConfig) { c.Comment = `x"y` }, ErrScopeInvalid},
		{"collides with DESYNC_MARK", func(c *ClientScopeConfig) { c.Mask = 0x40000000; c.Shift = 0 }, ErrMarkCollision},
		{"collides with wssize", func(c *ClientScopeConfig) { c.Mask = 0x04000000; c.Shift = 0 }, ErrMarkCollision},
		{"collides with supersweep", func(c *ClientScopeConfig) { c.Mask = 0x08000000; c.Shift = 0 }, ErrMarkCollision},
		{"collides with filter", func(c *ClientScopeConfig) { c.Marks.Filter = 0x100 }, ErrMarkCollision},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := good
			tt.mutate(&c)
			if err := c.Validate(); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
	// Комментарий с пробелом допустим для nft, но не для iptables.
	c := good
	c.Comment, c.Backend = "zator client scope", BackendNftables
	if err := c.Validate(); err != nil {
		t.Errorf("nft comment with spaces: %v", err)
	}
	c.Backend = BackendIptables
	if err := c.Validate(); !errors.Is(err, ErrScopeInvalid) {
		t.Errorf("iptables comment with spaces must be rejected: %v", err)
	}
}

func TestClientScopeConfigFromGetter(t *testing.T) {
	c, err := ClientScopeConfigFromGetter(mapGetter(map[string]string{
		"CLIENT_SCOPE_ENABLE":     "1",
		"CLIENT_SCOPE_MARK_MASK":  "0xff00",
		"CLIENT_SCOPE_MARK_SHIFT": "8",
		"CLIENT_SCOPE_MARK_MAX":   "10",
		"CLIENT_SCOPE_MAP_FILE":   "/tmp/x.tsv",
		"CLIENT_SCOPE_NFT_CHAIN":  "cls",
		"FWTYPE":                  "nftables",
	}), "/opt/zator")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enable || c.Mask != 0xff00 || c.Shift != 8 || c.Max != 10 || c.MapFile != "/tmp/x.tsv" ||
		c.NftChain != "cls" || c.Backend != BackendNftables {
		t.Errorf("unexpected config: %+v", c)
	}

	// config.default: пустая маска → «не настроено».
	c, err = ClientScopeConfigFromGetter(mapGetter(map[string]string{
		"CLIENT_SCOPE_ENABLE": "1", "CLIENT_SCOPE_MARK_MASK": "", "CLIENT_SCOPE_MARK_SHIFT": "0", "CLIENT_SCOPE_MARK_MAX": "255",
	}), "/opt/zator")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); !errors.Is(err, ErrScopeMaskMissing) {
		t.Errorf("Validate = %v, want ErrScopeMaskMissing", err)
	}
	if c.MapFile != "/opt/zator/extra_strats/cache/client_scope.tsv" {
		t.Errorf("default map file = %q", c.MapFile)
	}

	if _, err := ClientScopeConfigFromGetter(mapGetter(map[string]string{"CLIENT_SCOPE_MARK_SHIFT": "x"}), "/opt/zator"); err == nil {
		t.Error("invalid shift must fail")
	}
}

func TestClientScopeNftApply(t *testing.T) {
	r := NewRecordingRunner("nft")
	res, err := NewClientScope(testScopeConfig(BackendNftables), r).Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 3 || len(res.Skipped) == 0 {
		t.Errorf("result = %+v", res)
	}
	calls := r.Calls()
	if len(calls) != 1 || calls[0].Name != "nft" || !reflect.DeepEqual(calls[0].Args, []string{"-f", "-"}) {
		t.Fatalf("calls = %v", r.Lines())
	}
	want := `add table inet zator_client_scope
delete table inet zator_client_scope
add table inet zator_client_scope
add chain inet zator_client_scope classify { type filter hook prerouting priority -190; policy accept; }
add rule inet zator_client_scope classify ip saddr 192.168.1.10 meta mark set ((meta mark & 0xffff00ff) | 256) ct mark set meta mark comment "zator-client-scope"
add rule inet zator_client_scope classify ip saddr 192.168.1.20 meta mark set ((meta mark & 0xffff00ff) | 512) ct mark set meta mark comment "zator-client-scope"
add rule inet zator_client_scope classify ip6 saddr fd00::5 meta mark set ((meta mark & 0xffff00ff) | 1280) ct mark set meta mark comment "zator-client-scope"
`
	if calls[0].Stdin != want {
		t.Errorf("ruleset mismatch:\n got: %s\nwant: %s", calls[0].Stdin, want)
	}
}

func TestClientScopeIptablesApply(t *testing.T) {
	r := NewRecordingRunner("iptables")
	jumpDeleted := false
	r.Handler = func(c Command) ([]byte, error) {
		a := strings.Join(c.Args, " ")
		switch {
		case strings.HasPrefix(a, "-t mangle -S ZATOR_CLIENT_SCOPE"):
			return []byte("-N ZATOR_CLIENT_SCOPE\n" +
				"-A ZATOR_CLIENT_SCOPE -s 10.0.0.1/32 -m comment --comment zator-client-scope -j MARK --set-xmark 0x100/0xff00\n" +
				"-A ZATOR_CLIENT_SCOPE -s 10.0.0.2/32 -m comment --comment \"zator-client-scope\" -j MARK --set-xmark 0x200/0xff00\n" +
				"-A ZATOR_CLIENT_SCOPE -s 10.0.0.3/32 -m comment --comment foreign -j RETURN\n"), nil
		case strings.HasPrefix(a, "-t mangle -C PREROUTING"):
			if jumpDeleted {
				return nil, ErrExit1()
			}
			return nil, nil
		case strings.HasPrefix(a, "-t mangle -D PREROUTING"):
			jumpDeleted = true
		}
		return nil, nil
	}
	res, err := NewClientScope(testScopeConfig(BackendIptables), r).Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 2 {
		t.Errorf("Applied = %d, want 2 (IPv6 skipped)", res.Applied)
	}
	var ipv6Skipped bool
	for _, s := range res.Skipped {
		if strings.Contains(s.Reason, "ipv6") {
			ipv6Skipped = true
		}
	}
	if !ipv6Skipped {
		t.Errorf("IPv6 entry must be reported as skipped: %+v", res.Skipped)
	}
	want := []string{
		"iptables -t mangle -N ZATOR_CLIENT_SCOPE",
		"iptables -t mangle -S ZATOR_CLIENT_SCOPE",
		"iptables -t mangle -D ZATOR_CLIENT_SCOPE -s 10.0.0.1/32 -m comment --comment zator-client-scope -j MARK --set-xmark 0x100/0xff00",
		"iptables -t mangle -D ZATOR_CLIENT_SCOPE -s 10.0.0.2/32 -m comment --comment zator-client-scope -j MARK --set-xmark 0x200/0xff00",
		"iptables -t mangle -C PREROUTING -m comment --comment zator-client-scope -j ZATOR_CLIENT_SCOPE",
		"iptables -t mangle -D PREROUTING -m comment --comment zator-client-scope -j ZATOR_CLIENT_SCOPE",
		"iptables -t mangle -C PREROUTING -m comment --comment zator-client-scope -j ZATOR_CLIENT_SCOPE",
		"iptables -t mangle -I PREROUTING 1 -m comment --comment zator-client-scope -j ZATOR_CLIENT_SCOPE",
		"iptables -t mangle -A ZATOR_CLIENT_SCOPE -s 192.168.1.10 -m comment --comment zator-client-scope -j MARK --set-mark 256/0x0000ff00",
		"iptables -t mangle -A ZATOR_CLIENT_SCOPE -s 192.168.1.20 -m comment --comment zator-client-scope -j MARK --set-mark 512/0x0000ff00",
	}
	if got := r.Lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("commands:\n got: %s\nwant: %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
}

func TestClientScopeIptablesApplyNeverFlushes(t *testing.T) {
	r := NewRecordingRunner("iptables")
	r.Handler = func(c Command) ([]byte, error) {
		if strings.Contains(strings.Join(c.Args, " "), "-C PREROUTING") {
			return nil, ErrExit1()
		}
		return nil, nil
	}
	if _, err := NewClientScope(testScopeConfig(BackendIptables), r).Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, l := range r.Lines() {
		if strings.Contains(l, " -F") || strings.Contains(l, " -X") {
			t.Errorf("must not flush/delete chains on apply: %s", l)
		}
	}
}

func TestClientScopeIptablesApplyCommands(t *testing.T) {
	c := testScopeConfig(BackendIptables)
	entries, _, _ := c.ParseTSV(strings.NewReader(sampleTSV))
	var lines []string
	for _, cmd := range c.IptablesApplyCommands(entries) {
		lines = append(lines, cmd.String())
	}
	if len(lines) != 4 { // -N, -I, два IPv4 (IPv6 пропущен)
		t.Fatalf("commands: %v", lines)
	}
	if lines[1] != "iptables -t mangle -I PREROUTING 1 -m comment --comment zator-client-scope -j ZATOR_CLIENT_SCOPE" {
		t.Errorf("jump insert: %s", lines[1])
	}
}

func TestClientScopeApplyGuards(t *testing.T) {
	ctx := context.Background()

	// Выключено — нет команд.
	r := NewRecordingRunner("nft")
	c := testScopeConfig(BackendNftables)
	c.Enable = false
	res, err := NewClientScope(c, r).Apply(ctx)
	if err != nil || !res.Disabled || len(r.Calls()) != 0 {
		t.Errorf("disabled: %+v %v %v", res, err, r.Lines())
	}

	// Нет файла карты — no-op.
	c = testScopeConfig(BackendNftables)
	c.ReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	res, err = NewClientScope(c, r).Apply(ctx)
	if err != nil || !res.MissingMapFile || len(r.Calls()) != 0 {
		t.Errorf("missing file: %+v %v %v", res, err, r.Lines())
	}

	// Ошибка чтения — ошибка.
	c.ReadFile = func(string) ([]byte, error) { return nil, os.ErrPermission }
	if _, err = NewClientScope(c, r).Apply(ctx); !errors.Is(err, os.ErrPermission) {
		t.Errorf("read error: %v", err)
	}

	// Маска не задана.
	c = testScopeConfig(BackendNftables)
	c.Mask = 0
	if _, err = NewClientScope(c, r).Apply(ctx); !errors.Is(err, ErrScopeMaskMissing) {
		t.Errorf("missing mask: %v", err)
	}

	// Backend не задан / бинарник отсутствует.
	c = testScopeConfig(BackendNone)
	if _, err = NewClientScope(c, r).Apply(ctx); !errors.Is(err, ErrNoBackend) {
		t.Errorf("no backend: %v", err)
	}
	c = testScopeConfig(BackendNftables)
	if _, err = NewClientScope(c, NewRecordingRunner("iptables")).Apply(ctx); !errors.Is(err, ErrBackendUnavailable) {
		t.Errorf("nft missing: %v", err)
	}
	c = testScopeConfig(BackendIptables)
	if _, err = NewClientScope(c, NewRecordingRunner("nft")).Apply(ctx); !errors.Is(err, ErrBackendUnavailable) {
		t.Errorf("iptables missing: %v", err)
	}

	// Коллизия меток блокирует Apply до выполнения команд.
	r.Reset()
	c = testScopeConfig(BackendNftables)
	c.Mask, c.Shift = 0x40000000, 0
	if _, err = NewClientScope(c, r).Apply(ctx); !errors.Is(err, ErrMarkCollision) || len(r.Calls()) != 0 {
		t.Errorf("collision: %v %v", err, r.Lines())
	}
}

func TestClientScopeApplyFailurePropagates(t *testing.T) {
	r := NewRecordingRunner("nft")
	r.Handler = func(Command) ([]byte, error) { return []byte("Error: syntax"), ErrExit1() }
	_, err := NewClientScope(testScopeConfig(BackendNftables), r).Apply(context.Background())
	var ce *CommandError
	if !errors.As(err, &ce) || ce.Output != "Error: syntax" {
		t.Errorf("err = %v", err)
	}
}

func TestClientScopeRemoveNft(t *testing.T) {
	ctx := context.Background()
	c := testScopeConfig(BackendNftables)

	r := NewRecordingRunner("nft")
	if err := NewClientScope(c, r).Remove(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"nft list table inet zator_client_scope",
		"nft delete table inet zator_client_scope",
	}
	if got := r.Lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %v", got)
	}

	// Таблицы нет — не ошибка и без delete.
	r = NewRecordingRunner("nft")
	r.Handler = func(Command) ([]byte, error) { return nil, ErrExit1() }
	if err := NewClientScope(c, r).Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.Calls()) != 1 {
		t.Errorf("only list expected: %v", r.Lines())
	}

	// Нет nft — no-op.
	r = NewRecordingRunner()
	if err := NewClientScope(c, r).Remove(ctx); err != nil || len(r.Calls()) != 0 {
		t.Errorf("no nft: %v %v", err, r.Lines())
	}
}

func TestClientScopeRemoveIptables(t *testing.T) {
	r := NewRecordingRunner("iptables")
	r.Handler = func(c Command) ([]byte, error) {
		a := strings.Join(c.Args, " ")
		if strings.Contains(a, "-S ZATOR_CLIENT_SCOPE") {
			return []byte("-A ZATOR_CLIENT_SCOPE -s 10.0.0.1/32 -m comment --comment zator-client-scope -j MARK --set-xmark 0x100/0xff00\n"), nil
		}
		if strings.Contains(a, "-C PREROUTING") {
			return nil, ErrExit1()
		}
		return nil, nil
	}
	if err := NewClientScope(testScopeConfig(BackendIptables), r).Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"iptables -t mangle -S ZATOR_CLIENT_SCOPE",
		"iptables -t mangle -D ZATOR_CLIENT_SCOPE -s 10.0.0.1/32 -m comment --comment zator-client-scope -j MARK --set-xmark 0x100/0xff00",
		"iptables -t mangle -C PREROUTING -m comment --comment zator-client-scope -j ZATOR_CLIENT_SCOPE",
		"iptables -t mangle -X ZATOR_CLIENT_SCOPE",
	}
	if got := r.Lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("commands:\n got: %v\nwant: %v", got, want)
	}
}

func TestClientScopeRemoveNoBackend(t *testing.T) {
	if err := NewClientScope(ClientScopeConfig{}, NewRecordingRunner()).Remove(context.Background()); !errors.Is(err, ErrNoBackend) {
		t.Errorf("err = %v", err)
	}
}

func TestClientScopeIptablesRemoveBoundedLoop(t *testing.T) {
	// -C всегда «успешен», -D тоже: цикл обязан завершиться.
	r := NewRecordingRunner("iptables")
	if err := NewClientScope(testScopeConfig(BackendIptables), r).Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.Calls()); n > 4*scopeMaxLoop {
		t.Errorf("unbounded deletion loop: %d calls", n)
	}
}

func TestDefaultClientScopeMapFile(t *testing.T) {
	if got := DefaultClientScopeMapFile(`C:\zator`); got != "C:/zator/extra_strats/cache/client_scope.tsv" {
		t.Errorf("got %q", got)
	}
}

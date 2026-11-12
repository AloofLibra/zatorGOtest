package firewall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

const sampleIPs = "# googlevideo\n203.0.113.5\n\n203.0.113.4\n203.0.113.5\n"

func testWssizeConfig(b Backend) WssizeConfig {
	c := DefaultWssizeConfig("/opt/zator")
	c.Enable = true
	c.Backend = b
	c.QNum = 200
	c.ReadFile = func(string) ([]byte, error) { return []byte(sampleIPs), nil }
	return c
}

// nftHandler имитирует nft: набор уже существует с одним элементом, в цепочках
// могут быть наши старые правила.
func nftHandler(existing map[string]string) func(Command) ([]byte, error) {
	return func(c Command) ([]byte, error) {
		a := strings.Join(c.Args, " ")
		switch {
		case a == "list set inet zapret2 z2r_googlevideo4":
			if existing["set"] == "" {
				return nil, ErrExit1()
			}
			return []byte(existing["set"]), nil
		case strings.HasPrefix(a, "-a list chain inet zapret2 "):
			chain := strings.TrimPrefix(a, "-a list chain inet zapret2 ")
			out, ok := existing["chain:"+chain]
			if !ok {
				return nil, ErrExit1()
			}
			return []byte(out), nil
		}
		return nil, nil
	}
}

func chainListing(chain string, rules ...string) string {
	return "table inet zapret2 {\n\tchain " + chain + " {\n\t\ttype filter hook postrouting priority 100; policy accept;\n" +
		strings.Join(rules, "\n") + "\n\t}\n}\n"
}

func TestWssizeNftRuleSpecs(t *testing.T) {
	c := testWssizeConfig(BackendNftables)
	got := c.NftSetupScript()
	want := `insert rule inet zapret2 postnat_hook meta nfproto ipv4 ip daddr @z2r_googlevideo4 tcp dport 443 meta mark & 0x04000000 == 0 meta mark set meta mark or 0x04000000 comment "z2r_wssize_mark_out"
insert rule inet zapret2 prenat_hook meta nfproto ipv4 ip saddr @z2r_googlevideo4 tcp sport 443 meta mark & 0x04000000 == 0 meta mark set meta mark or 0x04000000 comment "z2r_wssize_mark_in"
add rule inet zapret2 postnat_hook meta mark & 0x04000000 == 0x04000000 meta mark & 0x40000000 == 0 meta nfproto ipv4 tcp dport 443 ct original packets > 20 counter meta mark set meta mark or 0x20000000 ct mark set ct mark or 0x40000000 queue num 200 bypass comment "z2r_wssize_late_out"
add rule inet zapret2 prenat_hook meta mark & 0x04000000 == 0x04000000 meta mark & 0x40000000 == 0 meta nfproto ipv4 tcp sport 443 ct reply packets > 10 counter ct mark set ct mark or 0x40000000 queue num 200 bypass comment "z2r_wssize_late_in"
`
	if got != want {
		t.Errorf("setup script mismatch:\n got:\n%s\nwant:\n%s", got, want)
	}

	// lite — без late-правил, без NAT-хуков — postrouting/prerouting.
	c.Mode, c.Postnat = WssizeLite, false
	got = c.NftSetupScript()
	if strings.Contains(got, "late") || strings.Contains(got, "queue num") {
		t.Errorf("lite mode must not install late rules:\n%s", got)
	}
	if !strings.Contains(got, "postrouting_hook") || !strings.Contains(got, "prerouting_hook") {
		t.Errorf("non-NAT chains expected:\n%s", got)
	}

	// flow offload.
	c.FlowOffload = "software"
	if got = c.NftSetupScript(); !strings.Contains(got, `insert rule inet zapret2 flow_offload ip daddr @z2r_googlevideo4 tcp dport 443 return comment "z2r_wssize_flow"`) {
		t.Errorf("flow exemption missing:\n%s", got)
	}
	c.FlowOffload = "none"
	if got = c.NftSetupScript(); strings.Contains(got, "flow_offload") {
		t.Errorf("unexpected flow exemption:\n%s", got)
	}
}

func TestWssizeNftApplyFull(t *testing.T) {
	r := NewRecordingRunner("nft")
	r.Handler = nftHandler(map[string]string{
		"set":                "table inet zapret2 {\n set z2r_googlevideo4 {\n type ipv4_addr\n elements = { 203.0.113.4 expires 6d23h }\n }\n}",
		"chain:postnat_hook": chainListing("postnat_hook", "\t\tip daddr @x tcp dport 443 meta mark set meta mark or 0x1 comment \"z2r_wssize_mark_out\" # handle 7", "\t\tcounter comment \"other\" # handle 8"),
		"chain:prenat_hook":  chainListing("prenat_hook", "\t\tqueue num 1 bypass comment \"z2r_wssize_late_in\" # handle 9"),
	})
	res, err := NewWssize(testWssizeConfig(BackendNftables), r).Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Active || res.IPs != 2 || !res.LateRules {
		t.Errorf("result = %+v", res)
	}
	calls := r.Calls()
	// list set (ensure), list set (merge), add element, list chain post/pre/flow_offload, nft -f -
	if len(calls) != 7 {
		t.Fatalf("calls = %v", r.Lines())
	}
	// Только отсутствующий элемент добавляется в набор.
	merge := calls[2]
	if merge.Name != "nft" || !reflect.DeepEqual(merge.Args, []string{"-f", "-"}) ||
		merge.Stdin != "add element inet zapret2 z2r_googlevideo4 { 203.0.113.5 }\n" {
		t.Errorf("merge call = %v / %q", merge, merge.Stdin)
	}
	// Одна транзакция: удаление старых правил по handle + новые правила.
	txn := calls[len(calls)-1].Stdin
	for _, want := range []string{
		"delete rule inet zapret2 postnat_hook handle 7\n",
		"delete rule inet zapret2 prenat_hook handle 9\n",
		"insert rule inet zapret2 postnat_hook ",
		"add rule inet zapret2 prenat_hook ",
	} {
		if !strings.Contains(txn, want) {
			t.Errorf("transaction missing %q:\n%s", want, txn)
		}
	}
	if strings.Contains(txn, "handle 8") {
		t.Errorf("foreign rule must not be deleted:\n%s", txn)
	}
	// Набор никогда не удаляется.
	for _, l := range r.Lines() {
		if strings.Contains(l, "delete set") || strings.Contains(l, "flush set") {
			t.Errorf("shared set must stay intact: %s", l)
		}
	}
}

func TestWssizeNftApplyCreatesMissingSet(t *testing.T) {
	r := NewRecordingRunner("nft")
	r.Handler = nftHandler(map[string]string{
		"chain:postnat_hook": chainListing("postnat_hook"),
		"chain:prenat_hook":  chainListing("prenat_hook"),
	})
	if _, err := NewWssize(testWssizeConfig(BackendNftables), r).Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	lines := r.Lines()
	if lines[0] != "nft list set inet zapret2 z2r_googlevideo4" ||
		lines[1] != "nft add set inet zapret2 z2r_googlevideo4 '{ type ipv4_addr; flags timeout; timeout 7d; size 8192; }'" {
		t.Fatalf("set creation: %v", lines[:2])
	}
	// Набор пуст — оба адреса добавляются (отсортированно).
	var merge Command
	for _, c := range r.Calls() {
		if strings.HasPrefix(c.Stdin, "add element") {
			merge = c
		}
	}
	if merge.Stdin != "add element inet zapret2 z2r_googlevideo4 { 203.0.113.4, 203.0.113.5 }\n" {
		t.Errorf("merge stdin = %q", merge.Stdin)
	}
}

func TestWssizeNftMergeRetry(t *testing.T) {
	r := NewRecordingRunner("nft")
	attempts := 0
	base := nftHandler(map[string]string{
		"set":                "elements = { 203.0.113.4 }",
		"chain:postnat_hook": chainListing("postnat_hook"),
		"chain:prenat_hook":  chainListing("prenat_hook"),
	})
	r.Handler = func(c Command) ([]byte, error) {
		if strings.HasPrefix(c.Stdin, "add element") {
			attempts++
			if attempts == 1 {
				return []byte("File exists"), ErrExit1() // dnsmasq вставил тот же элемент
			}
		}
		return base(c)
	}
	if _, err := NewWssize(testWssizeConfig(BackendNftables), r).Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Errorf("merge attempts = %d, want 2", attempts)
	}

	// Повторный отказ — ошибка.
	r2 := NewRecordingRunner("nft")
	r2.Handler = func(c Command) ([]byte, error) {
		if strings.HasPrefix(c.Stdin, "add element") {
			return nil, ErrExit1()
		}
		return base(c)
	}
	if _, err := NewWssize(testWssizeConfig(BackendNftables), r2).Apply(context.Background()); err == nil {
		t.Error("second merge failure must be reported")
	}
}

func TestWssizeNftApplyLiteRemovesLateRules(t *testing.T) {
	r := NewRecordingRunner("nft")
	r.Handler = nftHandler(map[string]string{
		"set":                "elements = { 203.0.113.4, 203.0.113.5 }",
		"chain:postnat_hook": chainListing("postnat_hook", `		queue num 200 bypass comment "z2r_wssize_late_out" # handle 11`),
		"chain:prenat_hook":  chainListing("prenat_hook"),
	})
	c := testWssizeConfig(BackendNftables)
	c.Mode = WssizeLite
	res, err := NewWssize(c, r).Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.LateRules {
		t.Error("lite must not report late rules")
	}
	last := r.Calls()[len(r.Calls())-1].Stdin
	if !strings.Contains(last, "delete rule inet zapret2 postnat_hook handle 11") {
		t.Errorf("stale late rule must be removed:\n%s", last)
	}
	if strings.Contains(last, "queue num") {
		t.Errorf("lite must not add queue rules:\n%s", last)
	}
	// Все адреса уже в наборе — add element не вызывается.
	for _, call := range r.Calls() {
		if strings.HasPrefix(call.Stdin, "add element") {
			t.Errorf("unexpected merge: %q", call.Stdin)
		}
	}
}

func TestWssizeNftApplyFlowOffload(t *testing.T) {
	r := NewRecordingRunner("nft")
	r.Handler = nftHandler(map[string]string{
		"set":                "elements = { 203.0.113.4, 203.0.113.5 }",
		"chain:postnat_hook": chainListing("postnat_hook"),
		"chain:prenat_hook":  chainListing("prenat_hook"),
		"chain:flow_offload": chainListing("flow_offload", `		ip daddr @z2r_googlevideo4 tcp dport 443 return comment "z2r_wssize_flow" # handle 3`),
	})
	c := testWssizeConfig(BackendNftables)
	c.FlowOffload = "hardware"
	if _, err := NewWssize(c, r).Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	last := r.Calls()[len(r.Calls())-1].Stdin
	if !strings.Contains(last, "delete rule inet zapret2 flow_offload handle 3") ||
		!strings.Contains(last, "insert rule inet zapret2 flow_offload") {
		t.Errorf("flow rule must be replaced:\n%s", last)
	}

	// Цепочка flow_offload обязательна, если исключение включено.
	r = NewRecordingRunner("nft")
	r.Handler = nftHandler(map[string]string{
		"set":                "elements = { 203.0.113.4, 203.0.113.5 }",
		"chain:postnat_hook": chainListing("postnat_hook"),
		"chain:prenat_hook":  chainListing("prenat_hook"),
	})
	if _, err := NewWssize(c, r).Apply(context.Background()); err == nil {
		t.Error("missing flow_offload chain must fail when exemption is enabled")
	}
}

func TestWssizeNftApplyMissingChainFails(t *testing.T) {
	r := NewRecordingRunner("nft")
	r.Handler = nftHandler(map[string]string{"set": "elements = { 203.0.113.4, 203.0.113.5 }"})
	if _, err := NewWssize(testWssizeConfig(BackendNftables), r).Apply(context.Background()); err == nil {
		t.Fatal("missing zapret2 chains must fail")
	}
}

func TestWssizeNftRemove(t *testing.T) {
	r := NewRecordingRunner("nft")
	r.Handler = nftHandler(map[string]string{
		"chain:postnat_hook": chainListing("postnat_hook", `		x comment "z2r_wssize_mark_out" # handle 4`, `		y comment "z2r_supersweep" # handle 5`),
		"chain:prenat_hook":  chainListing("prenat_hook", `		x comment "z2r_wssize_late_in" # handle 6`),
	})
	if err := NewWssize(testWssizeConfig(BackendNftables), r).Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	txn := r.Calls()[len(r.Calls())-1].Stdin
	want := "delete rule inet zapret2 postnat_hook handle 4\ndelete rule inet zapret2 prenat_hook handle 6\n"
	if txn != want {
		t.Errorf("remove transaction = %q, want %q", txn, want)
	}

	// Нечего удалять — транзакция не запускается.
	r = NewRecordingRunner("nft")
	if err := NewWssize(testWssizeConfig(BackendNftables), r).Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Calls() {
		if c.Stdin != "" {
			t.Errorf("unexpected transaction: %q", c.Stdin)
		}
	}
}

func TestWssizeDisabledRemovesRules(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*WssizeConfig)
	}{
		{"ramp disabled", func(c *WssizeConfig) { c.Enable = false }},
		{"nfqws2 disabled", func(c *WssizeConfig) { c.NFQWS2Enable = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRecordingRunner("nft")
			r.Handler = nftHandler(map[string]string{
				"chain:postnat_hook": chainListing("postnat_hook", `		x comment "z2r_wssize_mark_out" # handle 4`),
			})
			c := testWssizeConfig(BackendNftables)
			tc.mutate(&c)
			res, err := NewWssize(c, r).Apply(context.Background())
			if err != nil || res.Active {
				t.Fatalf("res = %+v, err = %v", res, err)
			}
			if txn := r.Calls()[len(r.Calls())-1].Stdin; txn != "delete rule inet zapret2 postnat_hook handle 4\n" {
				t.Errorf("txn = %q", txn)
			}
		})
	}
}

func TestWssizeValidate(t *testing.T) {
	good := testWssizeConfig(BackendNftables)
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		mutate  func(*WssizeConfig)
		wantErr error
	}{
		{"mark collides with DESYNC_MARK", func(c *WssizeConfig) { c.Marks.Wssize = c.Marks.Desync }, ErrMarkCollision},
		{"mark collides with client scope", func(c *WssizeConfig) { c.Marks.ClientScopeMask = 0xff000000 }, ErrMarkCollision},
		{"mark collides with supersweep", func(c *WssizeConfig) { c.Marks.Wssize = SuperSweepMark }, ErrMarkCollision},
		{"zero mark", func(c *WssizeConfig) { c.Marks.Wssize = 0 }, ErrZeroMark},
		{"bad mode", func(c *WssizeConfig) { c.Mode = "turbo" }, ErrWssizeInvalid},
		{"bad table", func(c *WssizeConfig) { c.NftTable = "z;drop" }, ErrWssizeInvalid},
		{"bad set", func(c *WssizeConfig) { c.SetName = "a b" }, ErrWssizeInvalid},
		{"bad queue", func(c *WssizeConfig) { c.QNum = 70000 }, ErrWssizeInvalid},
		{"zero desync in full", func(c *WssizeConfig) { c.Marks.Desync = 0 }, ErrZeroMark},
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
	// Collision блокирует Apply до любых команд.
	r := NewRecordingRunner("nft")
	c := good
	c.Marks.Wssize = c.Marks.Desync
	if _, err := NewWssize(c, r).Apply(context.Background()); !errors.Is(err, ErrMarkCollision) || len(r.Calls()) != 0 {
		t.Errorf("collision must stop Apply early: %v %v", err, r.Lines())
	}
	// lite не требует QNUM и DESYNC_MARK.
	c = good
	c.Mode, c.QNum, c.Marks.Desync = WssizeLite, 0, 0
	if err := c.Validate(); err != nil {
		t.Errorf("lite: %v", err)
	}
}

func TestWssizeReadIPs(t *testing.T) {
	c := testWssizeConfig(BackendNftables)
	ips, err := c.ReadIPs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 2 || ips[0].String() != "203.0.113.4" || ips[1].String() != "203.0.113.5" {
		t.Errorf("ips = %v", ips)
	}

	for name, content := range map[string]string{
		"not an ip": "203.0.113.4\nnope\n",
		"ipv6":      "2001:db8::1\n",
		"cidr":      "203.0.113.0/24\n",
	} {
		c.ReadFile = func(string) ([]byte, error) { return []byte(content), nil }
		if _, err := c.ReadIPs(); !errors.Is(err, ErrInvalidIPv4) {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	// Лимит 8192: 8192 допустимо, 8193 — нет.
	gen := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "10.%d.%d.%d\n", i>>16&255, i>>8&255, i&255)
		}
		return b.String()
	}
	c.ReadFile = func(string) ([]byte, error) { return []byte(gen(WssizeMaxIPs)), nil }
	if ips, err = c.ReadIPs(); err != nil || len(ips) != WssizeMaxIPs {
		t.Errorf("max IPs: %d %v", len(ips), err)
	}
	c.ReadFile = func(string) ([]byte, error) { return []byte(gen(WssizeMaxIPs + 1)), nil }
	if _, err = c.ReadIPs(); !errors.Is(err, ErrTooManyIPs) {
		t.Errorf("too many: %v", err)
	}

	// Отсутствующий файл — пустой список.
	c.ReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if ips, err = c.ReadIPs(); err != nil || len(ips) != 0 {
		t.Errorf("missing file: %v %v", ips, err)
	}
}

func TestWssizeNftInvalidIPFileAbortsBeforeChanges(t *testing.T) {
	r := NewRecordingRunner("nft")
	c := testWssizeConfig(BackendNftables)
	c.ReadFile = func(string) ([]byte, error) { return []byte("bogus\n"), nil }
	if _, err := NewWssize(c, r).Apply(context.Background()); !errors.Is(err, ErrInvalidIPv4) {
		t.Fatalf("err = %v", err)
	}
	if len(r.Calls()) != 0 {
		t.Errorf("no commands expected: %v", r.Lines())
	}
}

func TestWssizeConfigFromGetter(t *testing.T) {
	c, err := WssizeConfigFromGetter(mapGetter(map[string]string{
		"Z2R_WSSIZE_RAMP_ENABLE": "1",
		"NFQWS2_ENABLE":          "1",
		"Z2R_WSSIZE_RAMP_MODE":   "LITE",
		"Z2R_WSSIZE_RAMP_IPSET":  "/tmp/gv.txt",
		"Z2R_WSSIZE_MARK":        "0x04000000",
		"NFQWS2_TCP_PKT_OUT":     "25",
		"NFQWS2_TCP_PKT_IN":      "12",
		"QNUM":                   "300",
		"POSTNAT":                "0",
		"FLOWOFFLOAD":            "software",
		"FWTYPE":                 "iptables",
		"ZAPRET_NFT_TABLE":       "zapret",
	}), "/opt/zator")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enable || !c.NFQWS2Enable || c.Mode != WssizeLite || c.IPFile != "/tmp/gv.txt" ||
		c.PktOut != 25 || c.PktIn != 12 || c.QNum != 300 || c.Postnat || c.FlowOffload != "software" ||
		c.Backend != BackendIptables || c.NftTable != "zapret" {
		t.Errorf("config = %+v", c)
	}
	if c.Marks.Wssize != 0x04000000 {
		t.Errorf("marks = %+v", c.Marks)
	}

	c, _ = WssizeConfigFromGetter(mapGetter(nil), "/opt/zator")
	if c.Enable || c.NFQWS2Enable || c.Mode != WssizeFull || !c.Postnat || c.IPFile != "/opt/zator/lists/googlevideo-ipv4.txt" {
		t.Errorf("defaults = %+v", c)
	}
	if _, err := WssizeConfigFromGetter(mapGetter(map[string]string{"QNUM": "x"}), "/opt/zator"); !errors.Is(err, ErrWssizeInvalid) {
		t.Errorf("invalid QNUM: %v", err)
	}
	if !PostnatFromGetter(mapGetter(map[string]string{"POSTNAT": "0", "POSTNAT_ALL": "1"})) {
		t.Error("POSTNAT_ALL=1 keeps NAT hooks")
	}
}

// ---------------------------------------------------------------- iptables

func iptablesWssizeRunner(jumps map[string]bool) *RecordingRunner {
	r := NewRecordingRunner("iptables", "ipset")
	r.Handler = func(c Command) ([]byte, error) {
		a := strings.Join(c.Args, " ")
		switch {
		case strings.HasPrefix(a, "-t mangle -C "):
			if jumps[strings.TrimPrefix(a, "-t mangle -C ")] {
				return nil, nil
			}
			return nil, ErrExit1()
		case strings.HasPrefix(a, "-t mangle -D "):
			delete(jumps, strings.TrimPrefix(a, "-t mangle -D "))
		case strings.HasPrefix(a, "-t mangle -nL "):
			return nil, ErrExit1() // цепочек нет
		}
		return nil, nil
	}
	return r
}

func TestWssizeIptablesApplyFull(t *testing.T) {
	r := iptablesWssizeRunner(map[string]bool{"PREROUTING -j Z2R_WS_MARK": true})
	res, err := NewWssize(testWssizeConfig(BackendIptables), r).Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Active || res.IPs != 2 || !res.LateRules {
		t.Errorf("res = %+v", res)
	}
	want := []string{
		"ipset create z2r_googlevideo4 hash:ip family inet timeout 604800 -exist",
		"ipset restore -exist",
		"iptables -t mangle -N Z2R_WS_MARK",
		"iptables -t mangle -F Z2R_WS_MARK",
		"iptables -t mangle -A Z2R_WS_MARK -m set --match-set z2r_googlevideo4 dst -p tcp --dport 443 -m mark ! --mark 0x40000000/0x40000000 -j MARK --set-xmark 0x04000000/0x04000000",
		"iptables -t mangle -A Z2R_WS_MARK -m set --match-set z2r_googlevideo4 src -p tcp --sport 443 -m mark ! --mark 0x40000000/0x40000000 -j MARK --set-xmark 0x04000000/0x04000000",
		"iptables -t mangle -C PREROUTING -j Z2R_WS_MARK",
		"iptables -t mangle -D PREROUTING -j Z2R_WS_MARK",
		"iptables -t mangle -C PREROUTING -j Z2R_WS_MARK",
		"iptables -t mangle -I PREROUTING 1 -j Z2R_WS_MARK",
		"iptables -t mangle -C POSTROUTING -j Z2R_WS_MARK",
		"iptables -t mangle -I POSTROUTING 1 -j Z2R_WS_MARK",
		"iptables -t mangle -N Z2R_WS_OUT",
		"iptables -t mangle -N Z2R_WS_IN",
		"iptables -t mangle -F Z2R_WS_OUT",
		"iptables -t mangle -F Z2R_WS_IN",
		"iptables -t mangle -A Z2R_WS_OUT -m mark --mark 0x04000000/0x04000000 -p tcp --dport 443 -m connbytes --connbytes-dir original --connbytes-mode packets --connbytes 21: -m mark ! --mark 0x40000000/0x40000000 -j NFQUEUE --queue-num 200 --queue-bypass",
		"iptables -t mangle -A Z2R_WS_IN -m mark --mark 0x04000000/0x04000000 -p tcp --sport 443 -m connbytes --connbytes-dir reply --connbytes-mode packets --connbytes 11: -m mark ! --mark 0x40000000/0x40000000 -j NFQUEUE --queue-num 200 --queue-bypass",
		"iptables -t mangle -C POSTROUTING -j Z2R_WS_OUT",
		"iptables -t mangle -A POSTROUTING -j Z2R_WS_OUT",
		"iptables -t mangle -C INPUT -j Z2R_WS_IN",
		"iptables -t mangle -A INPUT -j Z2R_WS_IN",
		"iptables -t mangle -C FORWARD -j Z2R_WS_IN",
		"iptables -t mangle -A FORWARD -j Z2R_WS_IN",
	}
	got := r.Lines()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands differ:\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if stdin := r.Calls()[1].Stdin; stdin != "add z2r_googlevideo4 203.0.113.4 -exist\nadd z2r_googlevideo4 203.0.113.5 -exist\n" {
		t.Errorf("ipset restore stdin = %q", stdin)
	}
}

func TestWssizeIptablesApplyLite(t *testing.T) {
	r := iptablesWssizeRunner(map[string]bool{})
	c := testWssizeConfig(BackendIptables)
	c.Mode = WssizeLite
	res, err := NewWssize(c, r).Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.LateRules {
		t.Error("lite must not install late rules")
	}
	lines := r.Lines()
	for _, l := range lines {
		if strings.Contains(l, "NFQUEUE") || strings.Contains(l, "-A POSTROUTING -j Z2R_WS_OUT") {
			t.Errorf("late rule in lite mode: %s", l)
		}
	}
	// Поздняя очистка выполняется (-nL Z2R_WS_OUT/IN).
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "-nL Z2R_WS_OUT") || !strings.Contains(joined, "-nL Z2R_WS_IN") {
		t.Errorf("late cleanup missing:\n%s", joined)
	}
}

func TestWssizeIptablesRequiresIpset(t *testing.T) {
	r := NewRecordingRunner("iptables")
	_, err := NewWssize(testWssizeConfig(BackendIptables), r).Apply(context.Background())
	if !errors.Is(err, ErrIpsetMissing) {
		t.Fatalf("err = %v", err)
	}
	if _, err = NewWssize(testWssizeConfig(BackendIptables), NewRecordingRunner("ipset")).Apply(context.Background()); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestWssizeIptablesRemove(t *testing.T) {
	jumps := map[string]bool{
		"POSTROUTING -j Z2R_WS_OUT": true, "INPUT -j Z2R_WS_IN": true, "FORWARD -j Z2R_WS_IN": true,
		"PREROUTING -j Z2R_WS_MARK": true, "POSTROUTING -j Z2R_WS_MARK": true,
	}
	r := NewRecordingRunner("iptables", "ipset")
	r.Handler = func(c Command) ([]byte, error) {
		a := strings.Join(c.Args, " ")
		switch {
		case strings.HasPrefix(a, "-t mangle -C "):
			if jumps[strings.TrimPrefix(a, "-t mangle -C ")] {
				return nil, nil
			}
			return nil, ErrExit1()
		case strings.HasPrefix(a, "-t mangle -D "):
			delete(jumps, strings.TrimPrefix(a, "-t mangle -D "))
		}
		return nil, nil
	}
	if err := NewWssize(testWssizeConfig(BackendIptables), r).Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(jumps) != 0 {
		t.Errorf("jumps left: %v", jumps)
	}
	joined := strings.Join(r.Lines(), "\n")
	for _, want := range []string{
		"iptables -t mangle -F Z2R_WS_OUT", "iptables -t mangle -X Z2R_WS_OUT",
		"iptables -t mangle -F Z2R_WS_IN", "iptables -t mangle -X Z2R_WS_IN",
		"iptables -t mangle -F Z2R_WS_MARK", "iptables -t mangle -X Z2R_WS_MARK",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(joined, "ipset destroy") || strings.Contains(joined, "ipset flush") {
		t.Error("ipset must stay intact")
	}
}

func TestWssizeNoBackend(t *testing.T) {
	c := testWssizeConfig(BackendNone)
	r := NewRecordingRunner()
	if _, err := NewWssize(c, r).Apply(context.Background()); !errors.Is(err, ErrNoBackend) {
		t.Errorf("Apply: %v", err)
	}
	if err := NewWssize(c, r).Remove(context.Background()); !errors.Is(err, ErrNoBackend) {
		t.Errorf("Remove: %v", err)
	}
}

func TestWssizeIptablesRuleSpecs(t *testing.T) {
	c := testWssizeConfig(BackendIptables)
	c.PktOut, c.PktIn = 30, 5
	late := c.IptablesLateRules()
	if !contains(late[0], "31:") || !contains(late[1], "6:") {
		t.Errorf("connbytes thresholds must be limit+1: %v", late)
	}
	marks := c.IptablesMarkRules()
	if len(marks) != 2 || !contains(marks[0], "dst") || !contains(marks[1], "src") {
		t.Errorf("mark rules = %v", marks)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

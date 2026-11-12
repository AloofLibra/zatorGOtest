package firewall

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Постоянные wssize_ramp (совпадают с tools/wssize-ramp-firewall.sh).
const (
	// WssizeSetName — общий kernel-набор Googlevideo IPv4 (nft set / ipset).
	WssizeSetName = "z2r_googlevideo4"
	// DefaultNftTable — таблица zapret2 (ZAPRET_NFT_TABLE).
	DefaultNftTable = "zapret2"
	// WssizeMaxIPs — максимум адресов в списке.
	WssizeMaxIPs = 8192
	// WssizeIPListRelPath — список Googlevideo IPv4 относительно корня zator.
	WssizeIPListRelPath = "lists/googlevideo-ipv4.txt"

	// iptables-цепочки wssize_ramp.
	WssizeIptMarkChain = "Z2R_WS_MARK"
	WssizeIptOutChain  = "Z2R_WS_OUT"
	WssizeIptInChain   = "Z2R_WS_IN"

	// Теги комментариев nft-правил: по ним правила находятся и снимаются.
	WssizeTagMarkOut = "z2r_wssize_mark_out"
	WssizeTagMarkIn  = "z2r_wssize_mark_in"
	WssizeTagLateOut = "z2r_wssize_late_out"
	WssizeTagLateIn  = "z2r_wssize_late_in"
	WssizeTagFlow    = "z2r_wssize_flow"

	nftSetSpec = "{ type ipv4_addr; flags timeout; timeout 7d; size 8192; }"
)

// Режимы wssize_ramp.
type WssizeMode string

const (
	// WssizeFull добавляет константное число поздних NFQUEUE-правил.
	WssizeFull WssizeMode = "full"
	// WssizeLite использует только обычные лимиты пакетов (без поздних правил).
	WssizeLite WssizeMode = "lite"
)

// Ошибки wssize_ramp.
var (
	// ErrWssizeInvalid — некорректная конфигурация.
	ErrWssizeInvalid = errors.New("firewall: invalid wssize_ramp configuration")
	// ErrTooManyIPs — в списке IPv4 больше WssizeMaxIPs адресов.
	ErrTooManyIPs = errors.New("firewall: too many IPv4 addresses in wssize_ramp list")
	// ErrInvalidIPv4 — строка списка не является IPv4.
	ErrInvalidIPv4 = errors.New("firewall: invalid IPv4 in wssize_ramp list")
	// ErrIpsetMissing — для iptables требуется утилита ipset.
	ErrIpsetMissing = errors.New("firewall: ipset is required for iptables mode")
)

var (
	nftTableRe  = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	nftIPv4Re   = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	nftHandleRe = regexp.MustCompile(`comment "([A-Za-z0-9_]+)"\s*# handle ([0-9]+)`)
)

// WssizeConfig — параметры wssize_ramp.
type WssizeConfig struct {
	// Enable — Z2R_WSSIZE_RAMP_ENABLE=1.
	Enable bool
	// NFQWS2Enable — NFQWS2_ENABLE=1 (без nfqws2 правила не нужны).
	NFQWS2Enable bool
	// Backend — iptables (ipset) или nftables (set).
	Backend Backend
	// Mode — full (по умолчанию) или lite.
	Mode WssizeMode

	// Marks — метки (Wssize, Desync, DesyncPostnat, Filter, ...).
	Marks MarkSet

	// IPFile — список Googlevideo IPv4 (Z2R_WSSIZE_RAMP_IPSET).
	IPFile string
	// NftTable — таблица zapret2 (ZAPRET_NFT_TABLE), по умолчанию zapret2.
	NftTable string
	// SetName — имя набора, по умолчанию z2r_googlevideo4.
	SetName string

	// PktOut/PktIn — NFQWS2_TCP_PKT_OUT / NFQWS2_TCP_PKT_IN (20 / 10).
	PktOut int
	PktIn  int
	// QNum — номер очереди NFQUEUE (QNUM).
	QNum int

	// Postnat — использовать NAT-хуки zapret2 (postnat_hook/prenat_hook);
	// иначе postrouting_hook/prerouting_hook. См. PostnatFromGetter.
	Postnat bool
	// PostChain/PreChain переопределяют имена nft-цепочек (необязательно).
	PostChain string
	PreChain  string
	// FlowOffload — FLOWOFFLOAD ("software"/"hardware" добавляют исключение
	// из flow offloading).
	FlowOffload string

	// ReadFile заменяет os.ReadFile (для тестов).
	ReadFile func(string) ([]byte, error)
}

// DefaultWssizeIPFile возвращает путь списка IPv4 для корня zator.
func DefaultWssizeIPFile(zatorRoot string) string {
	return path.Join(filepath.ToSlash(zatorRoot), WssizeIPListRelPath)
}

// DefaultWssizeConfig возвращает конфигурацию по умолчанию (Enable=false,
// режим full, NFQWS2 включён, NAT-хуки).
func DefaultWssizeConfig(zatorRoot string) WssizeConfig {
	return WssizeConfig{
		NFQWS2Enable: true,
		Mode:         WssizeFull,
		Marks:        DefaultMarks(),
		IPFile:       DefaultWssizeIPFile(zatorRoot),
		NftTable:     DefaultNftTable,
		SetName:      WssizeSetName,
		PktOut:       20,
		PktIn:        10,
		Postnat:      true,
	}
}

// PostnatFromGetter определяет использование NAT-хуков по POSTNAT/POSTNAT_ALL
// (как в supersweep_transport.sh).
func PostnatFromGetter(get Getter) bool {
	return !(getTrim(get, "POSTNAT") == "0" && getTrim(get, "POSTNAT_ALL") != "1")
}

// WssizeConfigFromGetter строит конфигурацию из переменных zapret2.
func WssizeConfigFromGetter(get Getter, zatorRoot string) (WssizeConfig, error) {
	c := DefaultWssizeConfig(zatorRoot)
	c.Enable = getTrim(get, "Z2R_WSSIZE_RAMP_ENABLE") == "1"
	c.NFQWS2Enable = getTrim(get, "NFQWS2_ENABLE") == "1"
	if b, ok := ParseBackend(getTrim(get, "FWTYPE")); ok {
		c.Backend = b
	}
	marks, err := MarksFromGetter(get)
	if err != nil {
		return c, err
	}
	c.Marks = marks
	if v := getTrim(get, "Z2R_WSSIZE_RAMP_MODE"); v != "" {
		c.Mode = WssizeMode(strings.ToLower(v))
	}
	if v := getTrim(get, "Z2R_WSSIZE_RAMP_IPSET"); v != "" {
		c.IPFile = v
	}
	if v := getTrim(get, "ZAPRET_NFT_TABLE"); v != "" {
		c.NftTable = v
	}
	for _, p := range []struct {
		name string
		dst  *int
	}{{"NFQWS2_TCP_PKT_OUT", &c.PktOut}, {"NFQWS2_TCP_PKT_IN", &c.PktIn}, {"QNUM", &c.QNum}} {
		if v := getTrim(get, p.name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return c, fmt.Errorf("%s: %w: %q", p.name, ErrWssizeInvalid, v)
			}
			*p.dst = n
		}
	}
	c.Postnat = PostnatFromGetter(get)
	c.FlowOffload = getTrim(get, "FLOWOFFLOAD")
	return c, nil
}

func (c WssizeConfig) withDefaults() WssizeConfig {
	if c.Mode == "" {
		c.Mode = WssizeFull
	}
	if c.NftTable == "" {
		c.NftTable = DefaultNftTable
	}
	if c.SetName == "" {
		c.SetName = WssizeSetName
	}
	if c.PktOut == 0 && c.PktIn == 0 {
		c.PktOut, c.PktIn = 20, 10
	}
	return c
}

func (c WssizeConfig) readFile(name string) ([]byte, error) {
	if c.ReadFile != nil {
		return c.ReadFile(name)
	}
	return os.ReadFile(name)
}

// Active сообщает, что правила должны быть установлены (функция и nfqws2 включены).
func (c WssizeConfig) Active() bool { return c.Enable && c.NFQWS2Enable }

// Full сообщает о режиме full (поздние правила нужны).
func (c WssizeConfig) Full() bool { return c.withDefaults().Mode == WssizeFull }

// NftChains возвращает имена nft-цепочек (post, pre).
func (c WssizeConfig) NftChains() (post, pre string) {
	post, pre = "postrouting_hook", "prerouting_hook"
	if c.Postnat {
		post, pre = "postnat_hook", "prenat_hook"
	}
	if c.PostChain != "" {
		post = c.PostChain
	}
	if c.PreChain != "" {
		pre = c.PreChain
	}
	return post, pre
}

func (c WssizeConfig) flowOffloadExempt() bool {
	return c.FlowOffload == "software" || c.FlowOffload == "hardware"
}

// Validate проверяет конфигурацию: режим, метки (коллизии), таблицу, а в режиме
// full — пределы пакетов и номер очереди.
func (c WssizeConfig) Validate() error {
	c = c.withDefaults()
	if c.Mode != WssizeFull && c.Mode != WssizeLite {
		return fmt.Errorf("%w: mode %q", ErrWssizeInvalid, c.Mode)
	}
	if !nftTableRe.MatchString(c.NftTable) {
		return fmt.Errorf("%w: invalid nft table name %q", ErrWssizeInvalid, c.NftTable)
	}
	if !nftNameRe.MatchString(c.SetName) {
		return fmt.Errorf("%w: invalid set name %q", ErrWssizeInvalid, c.SetName)
	}
	if err := c.Marks.CheckWssize(); err != nil {
		return err
	}
	if c.Mode == WssizeFull {
		if c.PktOut < 0 || c.PktIn < 0 {
			return fmt.Errorf("%w: invalid TCP packet limits", ErrWssizeInvalid)
		}
		if c.QNum < 0 || c.QNum > 65535 {
			return fmt.Errorf("%w: invalid queue number %d", ErrWssizeInvalid, c.QNum)
		}
		if c.Marks.Desync == 0 {
			return fmt.Errorf("%w: %s", ErrZeroMark, NameDesyncMark)
		}
	}
	return nil
}

// ReadIPs читает список Googlevideo IPv4: пустые строки и комментарии (#)
// пропускаются, адреса проверяются и дедуплицируются. Отсутствующий или
// пустой файл — пустой результат.
func (c WssizeConfig) ReadIPs() ([]netip.Addr, error) {
	data, err := c.readFile(c.IPFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var (
		out   []netip.Addr
		seen  = make(map[netip.Addr]bool)
		count int
	)
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		a, perr := netip.ParseAddr(line)
		if perr != nil || !a.Is4() {
			return nil, fmt.Errorf("%w: %q", ErrInvalidIPv4, line)
		}
		count++
		if count > WssizeMaxIPs {
			return nil, ErrTooManyIPs
		}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out, nil
}

// ---------------------------------------------------------------- nft rule specs

// NftMarkOutRule — правило раннего маркирования исходящих пакетов к Googlevideo
// (в post-цепочке).
func (c WssizeConfig) NftMarkOutRule() string {
	c = c.withDefaults()
	m := FormatMark(c.Marks.Wssize)
	return fmt.Sprintf(`meta nfproto ipv4 ip daddr @%s tcp dport 443 meta mark & %s == 0 meta mark set meta mark or %s comment "%s"`,
		c.SetName, m, m, WssizeTagMarkOut)
}

// NftMarkInRule — правило раннего маркирования входящих пакетов (pre-цепочка).
func (c WssizeConfig) NftMarkInRule() string {
	c = c.withDefaults()
	m := FormatMark(c.Marks.Wssize)
	return fmt.Sprintf(`meta nfproto ipv4 ip saddr @%s tcp sport 443 meta mark & %s == 0 meta mark set meta mark or %s comment "%s"`,
		c.SetName, m, m, WssizeTagMarkIn)
}

// NftLateOutRule — позднее правило очереди для исходящих пакетов после
// NFQWS2_TCP_PKT_OUT пакетов соединения.
func (c WssizeConfig) NftLateOutRule() string {
	c = c.withDefaults()
	m, d := FormatMark(c.Marks.Wssize), FormatMark(c.Marks.Desync)
	return fmt.Sprintf(`meta mark & %s == %s meta mark & %s == 0 meta nfproto ipv4 tcp dport 443 ct original packets > %d counter meta mark set meta mark or %s ct mark set ct mark or %s queue num %d bypass comment "%s"`,
		m, m, d, c.PktOut, FormatMark(c.Marks.DesyncPostnat), d, c.QNum, WssizeTagLateOut)
}

// NftLateInRule — позднее правило очереди для входящих пакетов.
func (c WssizeConfig) NftLateInRule() string {
	c = c.withDefaults()
	m, d := FormatMark(c.Marks.Wssize), FormatMark(c.Marks.Desync)
	return fmt.Sprintf(`meta mark & %s == %s meta mark & %s == 0 meta nfproto ipv4 tcp sport 443 ct reply packets > %d counter ct mark set ct mark or %s queue num %d bypass comment "%s"`,
		m, m, d, c.PktIn, d, c.QNum, WssizeTagLateIn)
}

// NftFlowExemptRule — исключение Googlevideo из flow offloading.
func (c WssizeConfig) NftFlowExemptRule() string {
	c = c.withDefaults()
	return fmt.Sprintf(`ip daddr @%s tcp dport 443 return comment "%s"`, c.SetName, WssizeTagFlow)
}

// NftSetupScript возвращает nft-скрипт установки правил (без удаления прежних):
// mark-правила вставляются в начало цепочек, late-правила (режим full)
// добавляются в конец, flow-исключение — при FLOWOFFLOAD=software|hardware.
func (c WssizeConfig) NftSetupScript() string {
	c = c.withDefaults()
	post, pre := c.NftChains()
	var b strings.Builder
	line := func(verb, chain, rule string) {
		fmt.Fprintf(&b, "%s rule inet %s %s %s\n", verb, c.NftTable, chain, rule)
	}
	line("insert", post, c.NftMarkOutRule())
	line("insert", pre, c.NftMarkInRule())
	if c.flowOffloadExempt() {
		line("insert", "flow_offload", c.NftFlowExemptRule())
	}
	if c.Mode == WssizeFull {
		line("add", post, c.NftLateOutRule())
		line("add", pre, c.NftLateInRule())
	}
	return b.String()
}

// ---------------------------------------------------------------- iptables rule specs

// IptablesMarkRules возвращает аргументы правил Z2R_WS_MARK (после "-t mangle -A").
func (c WssizeConfig) IptablesMarkRules() [][]string {
	c = c.withDefaults()
	d := markPair(c.Marks.Desync, c.Marks.Desync)
	set := markPair(c.Marks.Wssize, c.Marks.Wssize)
	return [][]string{
		{WssizeIptMarkChain, "-m", "set", "--match-set", c.SetName, "dst", "-p", "tcp", "--dport", "443",
			"-m", "mark", "!", "--mark", d, "-j", "MARK", "--set-xmark", set},
		{WssizeIptMarkChain, "-m", "set", "--match-set", c.SetName, "src", "-p", "tcp", "--sport", "443",
			"-m", "mark", "!", "--mark", d, "-j", "MARK", "--set-xmark", set},
	}
}

// IptablesLateRules возвращает аргументы поздних правил Z2R_WS_OUT / Z2R_WS_IN.
func (c WssizeConfig) IptablesLateRules() [][]string {
	c = c.withDefaults()
	d := markPair(c.Marks.Desync, c.Marks.Desync)
	set := markPair(c.Marks.Wssize, c.Marks.Wssize)
	q := strconv.Itoa(c.QNum)
	return [][]string{
		{WssizeIptOutChain, "-m", "mark", "--mark", set, "-p", "tcp", "--dport", "443",
			"-m", "connbytes", "--connbytes-dir", "original", "--connbytes-mode", "packets",
			"--connbytes", strconv.Itoa(c.PktOut+1) + ":", "-m", "mark", "!", "--mark", d,
			"-j", "NFQUEUE", "--queue-num", q, "--queue-bypass"},
		{WssizeIptInChain, "-m", "mark", "--mark", set, "-p", "tcp", "--sport", "443",
			"-m", "connbytes", "--connbytes-dir", "reply", "--connbytes-mode", "packets",
			"--connbytes", strconv.Itoa(c.PktIn+1) + ":", "-m", "mark", "!", "--mark", d,
			"-j", "NFQUEUE", "--queue-num", q, "--queue-bypass"},
	}
}

// ---------------------------------------------------------------- Wssize

// WssizeResult — итог Apply.
type WssizeResult struct {
	// Active — правила установлены (false: функция выключена, правила сняты).
	Active bool
	// IPs — число адресов в списке.
	IPs int
	// LateRules — установлены ли поздние правила (режим full).
	LateRules bool
}

// Wssize применяет и снимает правила wssize_ramp через Runner.
type Wssize struct {
	cfg WssizeConfig
	r   Runner
}

// NewWssize создаёт Wssize.
func NewWssize(cfg WssizeConfig, r Runner) *Wssize {
	return &Wssize{cfg: cfg.withDefaults(), r: r}
}

// Config возвращает действующую конфигурацию.
func (w *Wssize) Config() WssizeConfig { return w.cfg }

// Apply устанавливает правила wssize_ramp. Если функция или nfqws2 выключены —
// снимает собственные правила (Active=false). Общий набор z2r_googlevideo4
// никогда не удаляется: его заполняет dnsmasq независимо от zapret2.
func (w *Wssize) Apply(ctx context.Context) (WssizeResult, error) {
	if !w.cfg.Active() {
		return WssizeResult{}, w.Remove(ctx)
	}
	if err := w.cfg.Validate(); err != nil {
		return WssizeResult{}, err
	}
	switch w.cfg.Backend {
	case BackendNftables:
		return w.applyNft(ctx)
	case BackendIptables:
		return w.applyIptables(ctx)
	}
	return WssizeResult{}, ErrNoBackend
}

// Remove снимает собственные правила (набор остаётся). Идемпотентна.
func (w *Wssize) Remove(ctx context.Context) error {
	switch w.cfg.Backend {
	case BackendNftables:
		if _, err := w.r.LookPath("nft"); err != nil {
			return nil
		}
		return w.nftRemove(ctx)
	case BackendIptables:
		if _, err := w.r.LookPath("iptables"); err != nil {
			return nil
		}
		return w.iptablesCleanup(ctx)
	}
	return ErrNoBackend
}

// --- nftables

func (w *Wssize) applyNft(ctx context.Context) (WssizeResult, error) {
	var res WssizeResult
	c := w.cfg
	if _, err := w.r.LookPath("nft"); err != nil {
		return res, fmt.Errorf("%w: nft", ErrBackendUnavailable)
	}
	ips, err := c.ReadIPs()
	if err != nil {
		return res, err
	}
	if err := w.nftEnsureSet(ctx); err != nil {
		return res, err
	}
	if err := w.nftMergeSet(ctx, ips, false); err != nil {
		return res, err
	}
	post, pre := c.NftChains()
	var txn strings.Builder
	chains := []struct {
		name     string
		required bool
	}{{post, true}, {pre, true}, {"flow_offload", c.flowOffloadExempt()}}
	for _, ch := range chains {
		handles, err := w.nftOwnedHandles(ctx, ch.name, ch.required)
		if err != nil {
			return res, err
		}
		for _, h := range handles {
			fmt.Fprintf(&txn, "delete rule inet %s %s handle %d\n", c.NftTable, ch.name, h)
		}
	}
	txn.WriteString(c.NftSetupScript())
	if _, err := runStdin(ctx, w.r, txn.String(), "nft", "-f", "-"); err != nil {
		return res, err
	}
	res.Active, res.IPs, res.LateRules = true, len(ips), c.Mode == WssizeFull
	return res, nil
}

func (w *Wssize) nftEnsureSet(ctx context.Context) error {
	c := w.cfg
	if _, err := run(ctx, w.r, "nft", "list", "set", "inet", c.NftTable, c.SetName); err == nil {
		return nil
	}
	_, err := run(ctx, w.r, "nft", "add", "set", "inet", c.NftTable, c.SetName, nftSetSpec)
	return err
}

// nftMergeSet добавляет в набор только отсутствующие адреса. Конкурентная
// вставка dnsmasq может вызвать ошибку — тогда набор перечитывается и дельта
// добавляется ещё раз.
func (w *Wssize) nftMergeSet(ctx context.Context, ips []netip.Addr, retried bool) error {
	if len(ips) == 0 {
		return nil
	}
	c := w.cfg
	out, _ := run(ctx, w.r, "nft", "list", "set", "inet", c.NftTable, c.SetName)
	present := make(map[netip.Addr]bool)
	for _, m := range nftIPv4Re.FindAllString(string(out), -1) {
		if a, err := netip.ParseAddr(m); err == nil {
			present[a] = true
		}
	}
	var missing []string
	for _, a := range ips {
		if !present[a] {
			missing = append(missing, a.String())
		}
	}
	if len(missing) == 0 {
		return nil
	}
	script := fmt.Sprintf("add element inet %s %s { %s }\n", c.NftTable, c.SetName, strings.Join(missing, ", "))
	if _, err := runStdin(ctx, w.r, script, "nft", "-f", "-"); err != nil {
		if retried {
			return err
		}
		return w.nftMergeSet(ctx, ips, true)
	}
	return nil
}

// nftOwnedHandles возвращает handle правил цепочки с нашими тегами.
// Если цепочки нет и required=false — пустой результат без ошибки.
func (w *Wssize) nftOwnedHandles(ctx context.Context, chain string, required bool) ([]int, error) {
	out, err := run(ctx, w.r, "nft", "-a", "list", "chain", "inet", w.cfg.NftTable, chain)
	if err != nil {
		if required {
			return nil, err
		}
		return nil, nil
	}
	var handles []int
	for _, line := range strings.Split(string(out), "\n") {
		m := nftHandleRe.FindStringSubmatch(line)
		if m == nil || !strings.HasPrefix(m[1], "z2r_wssize_") {
			continue
		}
		if h, err := strconv.Atoi(m[2]); err == nil {
			handles = append(handles, h)
		}
	}
	return handles, nil
}

func (w *Wssize) nftRemove(ctx context.Context) error {
	c := w.cfg
	post, pre := c.NftChains()
	var txn strings.Builder
	for _, ch := range []string{post, pre, "flow_offload"} {
		handles, err := w.nftOwnedHandles(ctx, ch, false)
		if err != nil {
			return err
		}
		for _, h := range handles {
			fmt.Fprintf(&txn, "delete rule inet %s %s handle %d\n", c.NftTable, ch, h)
		}
	}
	if txn.Len() == 0 {
		return nil
	}
	_, err := runStdin(ctx, w.r, txn.String(), "nft", "-f", "-")
	return err
}

// --- iptables + ipset

func (w *Wssize) ipt(args ...string) Command {
	return Command{Name: "iptables", Args: append([]string{"-t", "mangle"}, args...)}
}

func (w *Wssize) applyIptables(ctx context.Context) (WssizeResult, error) {
	var res WssizeResult
	c := w.cfg
	if _, err := w.r.LookPath("iptables"); err != nil {
		return res, fmt.Errorf("%w: iptables", ErrBackendUnavailable)
	}
	if _, err := w.r.LookPath("ipset"); err != nil {
		return res, ErrIpsetMissing
	}
	ips, err := c.ReadIPs()
	if err != nil {
		return res, err
	}
	if _, err := run(ctx, w.r, "ipset", "create", c.SetName, "hash:ip", "family", "inet", "timeout", "604800", "-exist"); err != nil {
		return res, err
	}
	if len(ips) > 0 {
		var b strings.Builder
		for _, a := range ips {
			fmt.Fprintf(&b, "add %s %s -exist\n", c.SetName, a)
		}
		if _, err := runStdin(ctx, w.r, b.String(), "ipset", "restore", "-exist"); err != nil {
			return res, err
		}
	}
	if err := w.iptablesMarkInstall(ctx); err != nil {
		return res, err
	}
	if c.Mode != WssizeFull {
		if err := w.iptablesLateCleanup(ctx); err != nil {
			return res, err
		}
	} else if err := w.iptablesLateInstall(ctx); err != nil {
		return res, err
	}
	res.Active, res.IPs, res.LateRules = true, len(ips), c.Mode == WssizeFull
	return res, nil
}

func (w *Wssize) iptablesMarkInstall(ctx context.Context) error {
	_, _ = w.r.Run(ctx, w.ipt("-N", WssizeIptMarkChain))
	if _, err := w.r.Run(ctx, w.ipt("-F", WssizeIptMarkChain)); err != nil {
		return err
	}
	for _, rule := range w.cfg.IptablesMarkRules() {
		if _, err := w.r.Run(ctx, w.ipt(append([]string{"-A"}, rule...)...)); err != nil {
			return err
		}
	}
	// Метка должна появиться до обычных NFQUEUE-правил zapret2: убираем и
	// вставляем заново только свои переходы, чтобы не плодить дубликаты.
	for _, hook := range []string{"PREROUTING", "POSTROUTING"} {
		if err := w.iptablesRemoveJump(ctx, hook, WssizeIptMarkChain); err != nil {
			return err
		}
		if _, err := w.r.Run(ctx, w.ipt("-I", hook, "1", "-j", WssizeIptMarkChain)); err != nil {
			return err
		}
	}
	return nil
}

func (w *Wssize) iptablesLateInstall(ctx context.Context) error {
	_, _ = w.r.Run(ctx, w.ipt("-N", WssizeIptOutChain))
	_, _ = w.r.Run(ctx, w.ipt("-N", WssizeIptInChain))
	for _, ch := range []string{WssizeIptOutChain, WssizeIptInChain} {
		if _, err := w.r.Run(ctx, w.ipt("-F", ch)); err != nil {
			return err
		}
	}
	for _, rule := range w.cfg.IptablesLateRules() {
		if _, err := w.r.Run(ctx, w.ipt(append([]string{"-A"}, rule...)...)); err != nil {
			return err
		}
	}
	jumps := []struct{ hook, target string }{
		{"POSTROUTING", WssizeIptOutChain}, {"INPUT", WssizeIptInChain}, {"FORWARD", WssizeIptInChain},
	}
	for _, j := range jumps {
		if _, err := w.r.Run(ctx, w.ipt("-C", j.hook, "-j", j.target)); err == nil {
			continue
		}
		if _, err := w.r.Run(ctx, w.ipt("-A", j.hook, "-j", j.target)); err != nil {
			return err
		}
	}
	return nil
}

func (w *Wssize) iptablesRemoveJump(ctx context.Context, chain, target string) error {
	for i := 0; i < 4*scopeMaxLoop; i++ {
		if _, err := w.r.Run(ctx, w.ipt("-C", chain, "-j", target)); err != nil {
			return nil
		}
		if _, err := w.r.Run(ctx, w.ipt("-D", chain, "-j", target)); err != nil {
			return err
		}
	}
	return nil
}

func (w *Wssize) iptablesRemoveChain(ctx context.Context, chain string) error {
	if _, err := w.r.Run(ctx, w.ipt("-nL", chain)); err != nil {
		return nil // цепочки нет
	}
	if _, err := w.r.Run(ctx, w.ipt("-F", chain)); err != nil {
		return err
	}
	_, err := w.r.Run(ctx, w.ipt("-X", chain))
	return err
}

func (w *Wssize) iptablesLateCleanup(ctx context.Context) error {
	if err := w.iptablesRemoveJump(ctx, "POSTROUTING", WssizeIptOutChain); err != nil {
		return err
	}
	for _, hook := range []string{"INPUT", "FORWARD"} {
		if err := w.iptablesRemoveJump(ctx, hook, WssizeIptInChain); err != nil {
			return err
		}
	}
	if err := w.iptablesRemoveChain(ctx, WssizeIptOutChain); err != nil {
		return err
	}
	return w.iptablesRemoveChain(ctx, WssizeIptInChain)
}

func (w *Wssize) iptablesMarkCleanup(ctx context.Context) error {
	for _, hook := range []string{"PREROUTING", "POSTROUTING"} {
		if err := w.iptablesRemoveJump(ctx, hook, WssizeIptMarkChain); err != nil {
			return err
		}
	}
	return w.iptablesRemoveChain(ctx, WssizeIptMarkChain)
}

func (w *Wssize) iptablesCleanup(ctx context.Context) error {
	if err := w.iptablesLateCleanup(ctx); err != nil {
		return err
	}
	return w.iptablesMarkCleanup(ctx)
}

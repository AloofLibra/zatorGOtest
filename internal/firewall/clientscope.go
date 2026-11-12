package firewall

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Постоянные client scope (совпадают с firewall/client-scope-*.sh).
const (
	// ClientScopeNftTable — приватная nft-таблица (имя каноническое, не настраивается).
	ClientScopeNftTable = "zator_client_scope"
	// ClientScopeNftPriority — приоритет цепочки: после conntrack (-200), до
	// стандартной mangle-зоны (-150).
	ClientScopeNftPriority = -190
	// DefaultClientScopeIptChain — iptables-цепочка по умолчанию.
	DefaultClientScopeIptChain = "ZATOR_CLIENT_SCOPE"
	// DefaultClientScopeIptTable — iptables-таблица по умолчанию.
	DefaultClientScopeIptTable = "mangle"
	// DefaultClientScopeNftChain — nft-цепочка по умолчанию.
	DefaultClientScopeNftChain = "classify"
	// DefaultClientScopeComment — комментарий, которым помечены собственные правила.
	DefaultClientScopeComment = "zator-client-scope"
	// ClientScopeMapRelPath — относительный путь файла client_scope.tsv от корня zator.
	ClientScopeMapRelPath = "extra_strats/cache/client_scope.tsv"

	// scopeMaxLoop ограничивает циклы удаления дубликатов правил/переходов.
	scopeMaxLoop = 64
)

// Ошибки client scope.
var (
	// ErrScopeMaskMissing — маска client scope не задана (как «missing-mask» в locked.lua).
	ErrScopeMaskMissing = errors.New("firewall: client scope mark mask is not configured")
	// ErrScopeInvalid — некорректная конфигурация client scope.
	ErrScopeInvalid = errors.New("firewall: invalid client scope configuration")
)

var (
	nftNameRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,31}$`)
	nftCommentRe = regexp.MustCompile(`^[A-Za-z0-9_.:/ -]{1,64}$`)
	iptNameRe    = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,28}$`)
	iptTableRe   = regexp.MustCompile(`^[a-z]{3,8}$`)
	iptCommentRe = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,64}$`)
)

// ClientScopeConfig — параметры client scope.
type ClientScopeConfig struct {
	// Enable — CLIENT_SCOPE_ENABLE=1. При false Apply ничего не делает.
	Enable bool
	// Backend — iptables или nftables (см. DetectBackend).
	Backend Backend
	// MapFile — путь к client_scope.tsv (строки "mark:N<TAB>ip").
	MapFile string

	// Mask, Shift, Max — CLIENT_SCOPE_MARK_MASK / _SHIFT / _MAX.
	Mask  uint32
	Shift uint
	Max   uint32

	IptablesTable string // по умолчанию mangle
	IptablesChain string // по умолчанию ZATOR_CLIENT_SCOPE
	NftChain      string // по умолчанию classify
	Comment       string // по умолчанию zator-client-scope

	// Marks — остальные метки для проверки коллизий (ClientScopeMask
	// подставляется из Mask).
	Marks MarkSet

	// ReadFile заменяет os.ReadFile (для тестов).
	ReadFile func(string) ([]byte, error)
}

// DefaultClientScopeMapFile возвращает путь client_scope.tsv для корня zator
// (всегда с прямыми слешами — путь используется на устройстве).
func DefaultClientScopeMapFile(zatorRoot string) string {
	return path.Join(filepath.ToSlash(zatorRoot), ClientScopeMapRelPath)
}

// DefaultClientScopeConfig возвращает конфигурацию по умолчанию (маска 0xff00,
// сдвиг 8, максимум 255); Enable=false.
func DefaultClientScopeConfig(zatorRoot string) ClientScopeConfig {
	return ClientScopeConfig{
		MapFile:       DefaultClientScopeMapFile(zatorRoot),
		Mask:          DefaultClientScopeMask,
		Shift:         DefaultClientScopeShift,
		Max:           DefaultClientScopeMax,
		IptablesTable: DefaultClientScopeIptTable,
		IptablesChain: DefaultClientScopeIptChain,
		NftChain:      DefaultClientScopeNftChain,
		Comment:       DefaultClientScopeComment,
		Marks:         DefaultMarks(),
	}
}

// ClientScopeConfigFromGetter строит конфигурацию из переменных zapret2
// (CLIENT_SCOPE_ENABLE, CLIENT_SCOPE_MARK_MASK/SHIFT/MAX, CLIENT_SCOPE_CHAIN,
// CLIENT_SCOPE_TABLE, CLIENT_SCOPE_NFT_CHAIN, CLIENT_SCOPE_COMMENT,
// CLIENT_SCOPE_MAP_FILE, FWTYPE и метки). Пустая маска остаётся нулём —
// это «не настроено» (Validate вернёт ErrScopeMaskMissing), как в locked.lua.
func ClientScopeConfigFromGetter(get Getter, zatorRoot string) (ClientScopeConfig, error) {
	c := DefaultClientScopeConfig(zatorRoot)
	c.Enable = getTrim(get, "CLIENT_SCOPE_ENABLE") == "1"
	if b, ok := ParseBackend(getTrim(get, "FWTYPE")); ok {
		c.Backend = b
	}
	marks, err := MarksFromGetter(get)
	if err != nil {
		return c, err
	}
	c.Marks = marks
	c.Mask = marks.ClientScopeMask
	if v := getTrim(get, "CLIENT_SCOPE_MARK_SHIFT"); v != "" {
		n, err := ParseMark(v)
		if err != nil {
			return c, fmt.Errorf("CLIENT_SCOPE_MARK_SHIFT: %w", err)
		}
		c.Shift = uint(n)
	}
	if v := getTrim(get, "CLIENT_SCOPE_MARK_MAX"); v != "" {
		n, err := ParseMark(v)
		if err != nil {
			return c, fmt.Errorf("CLIENT_SCOPE_MARK_MAX: %w", err)
		}
		c.Max = n
	}
	if v := getTrim(get, "CLIENT_SCOPE_CHAIN"); v != "" {
		c.IptablesChain = v
	}
	if v := getTrim(get, "CLIENT_SCOPE_TABLE"); v != "" {
		c.IptablesTable = v
	}
	if v := getTrim(get, "CLIENT_SCOPE_NFT_CHAIN"); v != "" {
		c.NftChain = v
	}
	if v := getTrim(get, "CLIENT_SCOPE_COMMENT"); v != "" {
		c.Comment = v
	}
	if v := getTrim(get, "CLIENT_SCOPE_MAP_FILE"); v != "" {
		c.MapFile = v
	}
	return c, nil
}

func (c ClientScopeConfig) withDefaults() ClientScopeConfig {
	if c.IptablesTable == "" {
		c.IptablesTable = DefaultClientScopeIptTable
	}
	if c.IptablesChain == "" {
		c.IptablesChain = DefaultClientScopeIptChain
	}
	if c.NftChain == "" {
		c.NftChain = DefaultClientScopeNftChain
	}
	if c.Comment == "" {
		c.Comment = DefaultClientScopeComment
	}
	return c
}

func (c ClientScopeConfig) readFile(name string) ([]byte, error) {
	if c.ReadFile != nil {
		return c.ReadFile(name)
	}
	return os.ReadFile(name)
}

// Validate проверяет маску, сдвиг, имена и коллизии с остальными метками.
func (c ClientScopeConfig) Validate() error {
	c = c.withDefaults()
	if c.Mask == 0 {
		return ErrScopeMaskMissing
	}
	if c.Shift > 31 {
		return fmt.Errorf("%w: shift %d > 31", ErrScopeInvalid, c.Shift)
	}
	if c.Mask&((uint32(1)<<c.Shift)-1) != 0 {
		return fmt.Errorf("%w: mask %s has bits below shift %d", ErrScopeInvalid, FormatMark(c.Mask), c.Shift)
	}
	if c.Max == 0 {
		return fmt.Errorf("%w: max scope id must be >= 1", ErrScopeInvalid)
	}
	if !iptNameRe.MatchString(c.IptablesChain) {
		return fmt.Errorf("%w: iptables chain %q", ErrScopeInvalid, c.IptablesChain)
	}
	if !iptTableRe.MatchString(c.IptablesTable) {
		return fmt.Errorf("%w: iptables table %q", ErrScopeInvalid, c.IptablesTable)
	}
	if !nftNameRe.MatchString(c.NftChain) {
		return fmt.Errorf("%w: nft chain %q", ErrScopeInvalid, c.NftChain)
	}
	switch c.Backend {
	case BackendIptables:
		if !iptCommentRe.MatchString(c.Comment) {
			return fmt.Errorf("%w: comment %q is not valid for iptables", ErrScopeInvalid, c.Comment)
		}
	case BackendNftables:
		if !nftCommentRe.MatchString(c.Comment) {
			return fmt.Errorf("%w: comment %q is not valid for nft", ErrScopeInvalid, c.Comment)
		}
	default:
		if !iptCommentRe.MatchString(c.Comment) && !nftCommentRe.MatchString(c.Comment) {
			return fmt.Errorf("%w: comment %q", ErrScopeInvalid, c.Comment)
		}
	}
	marks := c.Marks
	marks.ClientScopeMask = c.Mask
	return marks.CheckClientScope()
}

// ClientScopeEntry — одна запись client_scope.tsv.
type ClientScopeEntry struct {
	ID   uint32
	Addr netip.Addr
	Mark uint32 // ID << Shift
}

// SkippedLine — строка TSV, не попавшая в правила.
type SkippedLine struct {
	Line   int
	Text   string
	Reason string
}

// MarkFor возвращает метку для идентификатора скоупа или ошибку, если id
// некорректен, превышает Max или не помещается в маску.
func (c ClientScopeConfig) MarkFor(id uint32) (uint32, error) {
	if id == 0 {
		return 0, errors.New("scope id must be >= 1")
	}
	if id > c.Max {
		return 0, fmt.Errorf("scope id %d exceeds max %d", id, c.Max)
	}
	if c.Shift > 31 {
		return 0, errors.New("shift out of range")
	}
	mark := uint64(id) << c.Shift
	if mark > 0xffffffff || uint32(mark)&^c.Mask != 0 {
		return 0, fmt.Errorf("scope id %d does not fit mask %s", id, FormatMark(c.Mask))
	}
	return uint32(mark), nil
}

// ParseTSV разбирает client_scope.tsv. Учитываются только строки вида
// "mark:N<TAB>ip" (прочие, пустые и комментарии пропускаются молча, как в
// shell). Некорректные id/IP попадают в skipped. Дубликаты IP: побеждает
// первая запись (как первое сработавшее правило iptables).
func (c ClientScopeConfig) ParseTSV(r io.Reader) (entries []ClientScopeEntry, skipped []SkippedLine, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	seen := make(map[netip.Addr]bool)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimRight(sc.Text(), "\r")
		fields := strings.Split(text, "\t")
		scope := strings.TrimSpace(fields[0])
		if !strings.HasPrefix(scope, "mark:") {
			continue
		}
		skip := func(reason string) {
			skipped = append(skipped, SkippedLine{Line: line, Text: text, Reason: reason})
		}
		idText := strings.TrimPrefix(scope, "mark:")
		id, perr := parseScopeID(idText)
		if perr != nil {
			skip(perr.Error())
			continue
		}
		mark, merr := c.MarkFor(id)
		if merr != nil {
			skip(merr.Error())
			continue
		}
		if len(fields) < 2 {
			skip("missing ip")
			continue
		}
		addr, aerr := netip.ParseAddr(strings.TrimSpace(fields[1]))
		if aerr != nil || addr.Zone() != "" {
			skip("invalid ip")
			continue
		}
		addr = addr.Unmap()
		if seen[addr] {
			skip("duplicate ip")
			continue
		}
		seen[addr] = true
		entries = append(entries, ClientScopeEntry{ID: id, Addr: addr, Mark: mark})
	}
	return entries, skipped, sc.Err()
}

func parseScopeID(s string) (uint32, error) {
	if s == "" || s[0] == '0' {
		return 0, errors.New("invalid scope id")
	}
	var n uint64
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, errors.New("invalid scope id")
		}
		n = n*10 + uint64(ch-'0')
		if n > 0xffffffff {
			return 0, errors.New("scope id too large")
		}
	}
	return uint32(n), nil
}

// ---------------------------------------------------------------- iptables

func (c ClientScopeConfig) ipt(args ...string) Command {
	return Command{Name: "iptables", Args: append([]string{"-t", c.IptablesTable}, args...)}
}

// IptablesJumpArgs — аргументы перехода PREROUTING → собственная цепочка
// (без "-t table" и без действия): -m comment --comment C -j CHAIN.
func (c ClientScopeConfig) IptablesJumpArgs() []string {
	c = c.withDefaults()
	return []string{"PREROUTING", "-m", "comment", "--comment", c.Comment, "-j", c.IptablesChain}
}

// IptablesJumpInsertArgs — аргументы вставки перехода первым правилом PREROUTING:
// -I PREROUTING 1 -m comment --comment C -j CHAIN.
func (c ClientScopeConfig) IptablesJumpInsertArgs() []string {
	jump := c.IptablesJumpArgs()
	return append([]string{"-I", jump[0], "1"}, jump[1:]...)
}

// IptablesRuleArgs возвращает аргументы правила для собственной цепочки
// (без "-t table" и без "-A"): "CHAIN -s IP -m comment ... -j MARK --set-mark M/MASK".
func (c ClientScopeConfig) IptablesRuleArgs(e ClientScopeEntry) []string {
	c = c.withDefaults()
	return []string{
		c.IptablesChain, "-s", e.Addr.String(),
		"-m", "comment", "--comment", c.Comment,
		"-j", "MARK", "--set-mark", fmt.Sprintf("%d/%s", e.Mark, FormatMark(c.Mask)),
	}
}

// IptablesApplyCommands возвращает последовательность команд применения для
// заданных записей без удаления прежних правил (оно требует листинга цепочки,
// см. ClientScope.Apply): создание цепочки, переход, правила. IPv6-записи
// пропускаются (iptables — только IPv4).
func (c ClientScopeConfig) IptablesApplyCommands(entries []ClientScopeEntry) []Command {
	c = c.withDefaults()
	cmds := []Command{
		c.ipt("-N", c.IptablesChain),
		c.ipt(c.IptablesJumpInsertArgs()...),
	}
	for _, e := range entries {
		if !e.Addr.Is4() {
			continue
		}
		cmds = append(cmds, c.ipt(append([]string{"-A"}, c.IptablesRuleArgs(e)...)...))
	}
	return cmds
}

// ---------------------------------------------------------------- nftables

// NftRuleLine возвращает строку правила nft для записи.
func (c ClientScopeConfig) NftRuleLine(e ClientScopeEntry) string {
	c = c.withDefaults()
	family := "ip"
	if e.Addr.Is6() {
		family = "ip6"
	}
	maskClear := 0xffffffff ^ c.Mask
	return fmt.Sprintf(`add rule inet %s %s %s saddr %s meta mark set ((meta mark & %s) | %d) ct mark set meta mark comment "%s"`,
		ClientScopeNftTable, c.NftChain, family, e.Addr, FormatMark(maskClear), e.Mark, c.Comment)
}

// NftRuleset возвращает атомарный nft-скрипт: пересоздание приватной таблицы и
// правила для всех записей. Подаётся на `nft -f -`.
func (c ClientScopeConfig) NftRuleset(entries []ClientScopeEntry) string {
	c = c.withDefaults()
	var b strings.Builder
	// add/delete/add в одной транзакции: таблица пересоздаётся атомарно,
	// даже если она ещё не существовала.
	fmt.Fprintf(&b, "add table inet %s\n", ClientScopeNftTable)
	fmt.Fprintf(&b, "delete table inet %s\n", ClientScopeNftTable)
	fmt.Fprintf(&b, "add table inet %s\n", ClientScopeNftTable)
	fmt.Fprintf(&b, "add chain inet %s %s { type filter hook prerouting priority %d; policy accept; }\n",
		ClientScopeNftTable, c.NftChain, ClientScopeNftPriority)
	for _, e := range entries {
		b.WriteString(c.NftRuleLine(e))
		b.WriteByte('\n')
	}
	return b.String()
}

// ---------------------------------------------------------------- Apply/Remove

// ClientScopeResult — итог Apply.
type ClientScopeResult struct {
	// Disabled — CLIENT_SCOPE_ENABLE != 1: ничего не выполнялось.
	Disabled bool
	// MissingMapFile — файл client_scope.tsv не существует: ничего не выполнялось.
	MissingMapFile bool
	// Applied — число установленных правил.
	Applied int
	// Skipped — строки TSV, пропущенные из-за ошибок (и IPv6 для iptables).
	Skipped []SkippedLine
}

// ClientScope применяет и снимает client scope-правила через Runner.
type ClientScope struct {
	cfg ClientScopeConfig
	r   Runner
}

// NewClientScope создаёт ClientScope. Конфигурация не валидируется до Apply.
func NewClientScope(cfg ClientScopeConfig, r Runner) *ClientScope {
	return &ClientScope{cfg: cfg.withDefaults(), r: r}
}

// Config возвращает действующую конфигурацию.
func (s *ClientScope) Config() ClientScopeConfig { return s.cfg }

// Apply читает client_scope.tsv и устанавливает правила. Поведение совпадает с
// shell-реализацией: при выключенной функции или отсутствующем файле карты
// ничего не делается (без ошибки).
func (s *ClientScope) Apply(ctx context.Context) (ClientScopeResult, error) {
	if !s.cfg.Enable {
		return ClientScopeResult{Disabled: true}, nil
	}
	if err := s.cfg.Validate(); err != nil {
		return ClientScopeResult{}, err
	}
	data, err := s.cfg.readFile(s.cfg.MapFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ClientScopeResult{MissingMapFile: true}, nil
		}
		return ClientScopeResult{}, err
	}
	entries, skipped, err := s.cfg.ParseTSV(strings.NewReader(string(data)))
	if err != nil {
		return ClientScopeResult{}, err
	}
	res, err := s.ApplyEntries(ctx, entries)
	res.Skipped = append(skipped, res.Skipped...)
	return res, err
}

// ApplyEntries устанавливает правила для готового списка записей
// (атомарно заменяя прежние собственные правила).
func (s *ClientScope) ApplyEntries(ctx context.Context, entries []ClientScopeEntry) (ClientScopeResult, error) {
	if err := s.cfg.Validate(); err != nil {
		return ClientScopeResult{}, err
	}
	switch s.cfg.Backend {
	case BackendIptables:
		return s.applyIptables(ctx, entries)
	case BackendNftables:
		return s.applyNft(ctx, entries)
	}
	return ClientScopeResult{}, ErrNoBackend
}

// Remove снимает собственные правила. Идемпотентна: отсутствие правил не ошибка.
func (s *ClientScope) Remove(ctx context.Context) error {
	switch s.cfg.Backend {
	case BackendIptables:
		if _, err := s.r.LookPath("iptables"); err != nil {
			return nil
		}
		if err := s.iptablesDeleteOwned(ctx); err != nil {
			return err
		}
		// Пустую собственную цепочку убираем; если в ней остались чужие правила,
		// iptables откажет — это не ошибка.
		_, _ = s.r.Run(ctx, s.cfg.ipt("-X", s.cfg.IptablesChain))
		return nil
	case BackendNftables:
		if _, err := s.r.LookPath("nft"); err != nil {
			return nil
		}
		if _, err := run(ctx, s.r, "nft", "list", "table", "inet", ClientScopeNftTable); err != nil {
			return nil // таблицы нет
		}
		_, err := run(ctx, s.r, "nft", "delete", "table", "inet", ClientScopeNftTable)
		return err
	}
	return ErrNoBackend
}

func (s *ClientScope) applyIptables(ctx context.Context, entries []ClientScopeEntry) (ClientScopeResult, error) {
	var res ClientScopeResult
	if _, err := s.r.LookPath("iptables"); err != nil {
		return res, fmt.Errorf("%w: iptables", ErrBackendUnavailable)
	}
	c := s.cfg
	_, _ = s.r.Run(ctx, c.ipt("-N", c.IptablesChain)) // уже существует — не ошибка
	if err := s.iptablesDeleteOwned(ctx); err != nil {
		return res, err
	}
	// Первым в PREROUTING: политики маршрутизации затирают марки полной маской.
	if _, err := s.r.Run(ctx, c.ipt(c.IptablesJumpInsertArgs()...)); err != nil {
		return res, err
	}
	for _, e := range entries {
		if !e.Addr.Is4() {
			res.Skipped = append(res.Skipped, SkippedLine{Text: e.Addr.String(), Reason: "ipv6 is not supported by the iptables backend"})
			continue
		}
		if _, err := s.r.Run(ctx, c.ipt(append([]string{"-A"}, c.IptablesRuleArgs(e)...)...)); err != nil {
			return res, err
		}
		res.Applied++
	}
	return res, nil
}

// iptablesDeleteOwned удаляет только правила своей цепочки, помеченные нашим
// комментарием, и переход из PREROUTING.
func (s *ClientScope) iptablesDeleteOwned(ctx context.Context) error {
	c := s.cfg
	out, err := s.r.Run(ctx, c.ipt("-S", c.IptablesChain))
	if err == nil {
		prefix := "-A " + c.IptablesChain + " "
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			tokens := splitArgs(strings.TrimPrefix(line, prefix))
			if !hasComment(tokens, c.Comment) {
				continue
			}
			if _, err := s.r.Run(ctx, c.ipt(append([]string{"-D", c.IptablesChain}, tokens...)...)); err != nil {
				return err
			}
		}
	}
	jump := c.IptablesJumpArgs()
	for i := 0; i < scopeMaxLoop; i++ {
		if _, err := s.r.Run(ctx, c.ipt(append([]string{"-C"}, jump...)...)); err != nil {
			break
		}
		if _, err := s.r.Run(ctx, c.ipt(append([]string{"-D"}, jump...)...)); err != nil {
			return err
		}
	}
	return nil
}

func hasComment(tokens []string, comment string) bool {
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i] == "--comment" && tokens[i+1] == comment {
			return true
		}
	}
	return false
}

func (s *ClientScope) applyNft(ctx context.Context, entries []ClientScopeEntry) (ClientScopeResult, error) {
	var res ClientScopeResult
	if _, err := s.r.LookPath("nft"); err != nil {
		return res, fmt.Errorf("%w: nft", ErrBackendUnavailable)
	}
	if !nftNameRe.MatchString(s.cfg.NftChain) || !nftCommentRe.MatchString(s.cfg.Comment) {
		return res, fmt.Errorf("%w: invalid nft identifiers", ErrScopeInvalid)
	}
	if _, err := runStdin(ctx, s.r, s.cfg.NftRuleset(entries), "nft", "-f", "-"); err != nil {
		return res, err
	}
	res.Applied = len(entries)
	return res, nil
}

// NftClientScopeCleanupCommand возвращает команду удаления приватной таблицы.
func NftClientScopeCleanupCommand() Command {
	return Command{Name: "nft", Args: []string{"delete", "table", "inet", ClientScopeNftTable}}
}

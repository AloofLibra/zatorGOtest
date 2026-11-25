// Package customstrats управляет пользовательскими стратегиями (custom_strategies.tsv),
// валидирует примитивы/ключи/блобы nfqws2, импортирует вывод blockcheck2 и инжектит стратегии в конфиг.
package customstrats

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/customblobs"
	"github.com/AloofLibra/zator/internal/orchestra"
)

const (
	MaxSpecLen = 500
	MaxGroups  = 8
	MaxEntries = 256
)

const (
	BlockTPL = "tpl"
	BlockP4  = "p4"
	BlockP5  = "p5"
	BlockP6  = "p6"
	BlockP7  = "p7"
)

var (
	ErrInvalid      = errors.New("некорректная стратегия")
	ErrNotFound     = errors.New("стратегия не найдена")
	ErrLimitReached = errors.New("достигнут лимит пользовательских стратегий (256)")
)

// BlockInfo описывает блок конфига, куда могут инъектироваться пользовательские стратегии.
type BlockInfo struct {
	Block     string   `json:"block"`
	Label     string   `json:"label"`
	Profiles  []int    `json:"profiles"`
	Protos    []string `json:"protos"`
	Transport string   `json:"transport"`
}

// ProtosFor возвращает протоколы для конкретного профиля блока (для tpl профиль 1 = tls + http, 2/3/8 = tls).
func (b BlockInfo) ProtosFor(profile int) []orchestra.Proto {
	if b.Block == BlockTPL {
		if profile == 1 {
			return []orchestra.Proto{orchestra.ProtoTLS, orchestra.ProtoHTTP}
		}
		return []orchestra.Proto{orchestra.ProtoTLS}
	}
	out := make([]orchestra.Proto, 0, len(b.Protos))
	for _, pr := range b.Protos {
		out = append(out, orchestra.Proto(pr))
	}
	return out
}

var allBlocks = []BlockInfo{
	{
		Block:     BlockTPL,
		Label:     "TCP TLS (профили 1/2/3/8 — общий набор)",
		Profiles:  []int{1, 2, 3, 8},
		Protos:    []string{"tls", "http"},
		Transport: "tcp",
	},
	{
		Block:     BlockP4,
		Label:     "Discord TCP (профиль 4)",
		Profiles:  []int{4},
		Protos:    []string{"tls"},
		Transport: "tcp",
	},
	{
		Block:     BlockP5,
		Label:     "QUIC (профиль 5, UDP 443)",
		Profiles:  []int{5},
		Protos:    []string{"udp"},
		Transport: "udp",
	},
	{
		Block:     BlockP6,
		Label:     "Голосовой UDP (профиль 6)",
		Profiles:  []int{6},
		Protos:    []string{"udp"},
		Transport: "udp",
	},
	{
		Block:     BlockP7,
		Label:     "Игровой UDP (профиль 7)",
		Profiles:  []int{7},
		Protos:    []string{"udp"},
		Transport: "udp",
	},
}

// Blocks возвращает метаданные всех поддерживаемых блоков.
func Blocks() []BlockInfo {
	out := make([]BlockInfo, len(allBlocks))
	for i, b := range allBlocks {
		out[i] = BlockInfo{
			Block:     b.Block,
			Label:     b.Label,
			Profiles:  append([]int(nil), b.Profiles...),
			Protos:    append([]string(nil), b.Protos...),
			Transport: b.Transport,
		}
	}
	return out
}

// LookupBlock возвращает описание блока по коду (`tpl|p4|p5|p6|p7`).
func LookupBlock(block string) (BlockInfo, bool) {
	b := strings.ToLower(strings.TrimSpace(block))
	for _, info := range allBlocks {
		if info.Block == b {
			return BlockInfo{
				Block:     info.Block,
				Label:     info.Label,
				Profiles:  append([]int(nil), info.Profiles...),
				Protos:    append([]string(nil), info.Protos...),
				Transport: info.Transport,
			}, true
		}
	}
	return BlockInfo{}, false
}

func makeSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

var (
	primitivesTCP = makeSet(
		"fake", "multisplit", "multidisorder", "multidisorder_legacy",
		"fakeddisorder", "fakedsplit", "fakemultisplit", "fakemultidisorder",
		"hostfakesplit", "syndata", "wssize", "tcpseg", "send",
		"tls_client_hello_clone", "oob", "pktmod", "drop",
		"http_hostcase", "http_domcase", "http_methodeol", "http_unixeol",
	)
	primitivesUDP = makeSet("fake", "send", "udplen", "dnsclone", "drop")

	keysTCP = makeSet(
		"fake_blob", "fallback", "host", "midhost", "pos", "seqovl", "seqovl_pattern",
		"sni_first", "sni_snt_new", "tcp_seq", "tcp_ack", "tcp_ts", "fake_count",
		"wsize", "scale", "disorder_after", "urp", "char", "byte", "spell",
		"tcp_flags_unset", "tcp_flags_set",
	)
	keysUDP  = makeSet("increment", "min", "ipfrag_pos_udp")
	keysBoth = makeSet("blob", "repeats", "out_range", "ip_autottl", "ip6_autottl", "ip_ttl", "ip6_ttl", "ip_id", "payload", "tls_mod", "pattern")

	flagsTCP = makeSet(
		"tcp_md5", "tcp_nop_del", "ip6_destopt", "ip6_hopbyhop", "ip6_hopbyhop2",
		"ip6_routing", "ip6_ah", "disorder_after", "nofake1", "nofake2", "nofake3",
		"nofake4", "fake_all", "nodrop", "tcp_ts_up", "sni_del",
	)
	flagsUDP  = makeSet("ip6_destopt", "ip6_hopbyhop", "ip6_hopbyhop2", "ip6_routing", "ip6_ah")
	flagsBoth = makeSet("badsum", "ipfrag", "ipfrag_disorder")

	blobRefKeys = makeSet("blob", "fake_blob", "fallback", "pattern", "seqovl_pattern")

	specCharsRe    = regexp.MustCompile(`^[A-Za-z0-9_=:,.\-+\ #%]+$`)
	strategyTailRe = regexp.MustCompile(`:strategy=[0-9]+$`)
	hexLiteralRe   = regexp.MustCompile(`^0x[0-9A-Fa-f]+$`)
	digitsRe       = regexp.MustCompile(`^[0-9]+$`)
	intValRe       = regexp.MustCompile(`^-?[0-9]+$`)
	seqovlRe       = regexp.MustCompile(`^(-?[0-9]+|#?[A-Za-z_][A-Za-z0-9_]*([+-][0-9]+)?)$`)
	urpRe          = regexp.MustCompile(`^([be]|[+-]?[0-9]+|(abs|host|endhost|sld|midsld|endsld|method|extlen|sniext)([+-][0-9]+)?)$`)
	charRe         = regexp.MustCompile(`^[A-Za-z0-9]$`)
	spellRe        = regexp.MustCompile(`^[A-Za-z]{4}$`)
	tcpFlagsRe     = regexp.MustCompile(`^[A-Za-z,]+$`)
	midhostRe      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	tlsModRe       = regexp.MustCompile(`^(rnd|rndsni|dupsid|padencap|none|sni=[A-Za-z0-9._%-]+)(,(rnd|rndsni|dupsid|padencap|none|sni=[A-Za-z0-9._%-]+))*$`)
	autottlRe      = regexp.MustCompile(`^-?[0-9]+([,-]-?[0-9]+)*$`)
	outRangeRe     = regexp.MustCompile(`^-[dnsp][0-9]+$`)
	patternRe      = regexp.MustCompile(`^(0x[0-9A-Fa-f]+|[0-9]+|[A-Za-z_][A-Za-z0-9_]*)$`)
	seqovlPatRe    = regexp.MustCompile(`^[0-9A-Za-z_]+$`)
	blobValRe      = regexp.MustCompile(`^[0-9A-Za-z_]+$`)
	ipIDRe         = regexp.MustCompile(`^(zero|rnd|0x[0-9A-Fa-f]+|[0-9]+)$`)
	payloadRe      = regexp.MustCompile(`^[a-z_,0-9]+$`)
	posRe          = regexp.MustCompile(`^[0-9A-Za-z_,+-]+$`)
	hostValRe      = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
)

// NormalizeSpec приводит пользовательский ввод к каноническому spec (без ведущего `--lua-desync=`
// и без `:strategy=N`, комбо-группы разделены ` --lua-desync=`).
func NormalizeSpec(raw string) (string, error) {
	s := strings.TrimSpace(strings.ReplaceAll(raw, "\r", ""))
	if s == "" {
		return "", fmt.Errorf("%w: Пустая стратегия.", ErrInvalid)
	}
	s = strings.Join(strings.Fields(s), " ")
	parts := strings.Split(s, " --lua-desync=")
	out := make([]string, 0, len(parts))
	for i, g := range parts {
		if i == 0 {
			g = strings.TrimPrefix(g, "--lua-desync=")
		}
		for strategyTailRe.MatchString(g) {
			g = strategyTailRe.ReplaceAllString(g, "")
		}
		if g == "" || strings.Contains(g, "strategy=") {
			return "", fmt.Errorf("%w: неверная позиция :strategy= в группе %q", ErrInvalid, g)
		}
		out = append(out, g)
	}
	return strings.Join(out, " --lua-desync="), nil
}

// Validate проверяет допустимость spec для выбранного блока и набора известных блобов.
func Validate(block, spec string, knownBlobs map[string]bool) error {
	info, ok := LookupBlock(block)
	if !ok {
		return fmt.Errorf("%w: Неизвестный блок: %s", ErrInvalid, block)
	}
	if spec == "" {
		return fmt.Errorf("%w: Пустая стратегия.", ErrInvalid)
	}
	if len(spec) > MaxSpecLen {
		return fmt.Errorf("%w: Стратегия длиннее %d символов.", ErrInvalid, MaxSpecLen)
	}
	if !specCharsRe.MatchString(spec) {
		return fmt.Errorf("%w: Запрещённые символы в стратегии (допустимы буквы, цифры и .,:=+-_#%%).", ErrInvalid)
	}

	groups := strings.Split(spec, " --lua-desync=")
	if len(groups) < 1 || len(groups) > MaxGroups {
		return fmt.Errorf("%w: От 1 до %d групп --lua-desync= в одной стратегии.", ErrInvalid, MaxGroups)
	}

	cloneTargets := collectCloneTargets(groups)
	for _, g := range groups {
		if err := validateGroup(g, info.Transport, knownBlobs, cloneTargets); err != nil {
			return err
		}
	}
	return nil
}

func collectCloneTargets(groups []string) map[string]bool {
	targets := make(map[string]bool)
	for _, g := range groups {
		if !strings.HasPrefix(g, "tls_client_hello_clone:") {
			continue
		}
		for _, tok := range strings.Split(strings.TrimPrefix(g, "tls_client_hello_clone:"), ":") {
			if strings.HasPrefix(tok, "blob=") {
				if name := strings.TrimPrefix(tok, "blob="); name != "" {
					targets[name] = true
				}
				break
			}
		}
	}
	return targets
}

func validateGroup(group, transport string, knownBlobs, cloneTargets map[string]bool) error {
	if group == "" {
		return fmt.Errorf("%w: пустая группа", ErrInvalid)
	}
	if strings.Contains(group, " ") {
		return fmt.Errorf("%w: Пробел внутри группы (группы разделяются « --lua-desync=»).", ErrInvalid)
	}
	prim, body, hasColon := strings.Cut(group, ":")
	if !primitivesTCP[prim] && !primitivesUDP[prim] {
		return fmt.Errorf("%w: Неизвестный примитив: %s", ErrInvalid, prim)
	}
	if transport == "udp" && !primitivesUDP[prim] {
		return fmt.Errorf("%w: Примитив %s не работает в UDP-блоке.", ErrInvalid, prim)
	}
	if transport != "udp" && !primitivesTCP[prim] {
		return fmt.Errorf("%w: Примитив %s не работает в TCP-блоке.", ErrInvalid, prim)
	}
	if !hasColon || body == "" {
		return nil
	}

	for _, tok := range strings.Split(body, ":") {
		if tok == "" {
			continue
		}
		key, val, hasEq := strings.Cut(tok, "=")
		if !hasEq {
			if !flagsBoth[tok] && ((transport == "udp" && !flagsUDP[tok]) || (transport != "udp" && !flagsTCP[tok])) {
				return fmt.Errorf("%w: Неизвестный флаг: %s", ErrInvalid, tok)
			}
			continue
		}
		if err := validateKV(key, val, transport); err != nil {
			return err
		}
		if blobRefKeys[key] {
			isLit := ((key == "pattern" || key == "seqovl_pattern") && (strings.HasPrefix(val, "0x") || digitsRe.MatchString(val))) ||
				(prim == "tls_client_hello_clone" && key == "blob") ||
				cloneTargets[val]
			if !isLit && !hexLiteralRe.MatchString(val) && !knownBlobs[val] {
				return fmt.Errorf("%w: Блоб «%s» не объявлен в конфиге.", ErrInvalid, val)
			}
		}
	}
	return nil
}

func validateKV(key, val, transport string) error {
	known := keysBoth[key] || (transport == "udp" && keysUDP[key]) || (transport != "udp" && keysTCP[key])
	if !known {
		return fmt.Errorf("%w: Неизвестный ключ: %s=", ErrInvalid, key)
	}
	switch key {
	case "repeats", "fake_count", "tcp_seq", "tcp_ack", "tcp_ts", "increment", "min",
		"ipfrag_pos_udp", "sni_snt_new", "wsize", "scale", "disorder_after", "byte", "ip_ttl", "ip6_ttl":
		if !intValRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ %s= ждёт целое число.", ErrInvalid, key)
		}
	case "seqovl":
		if !seqovlRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ seqovl= ждёт число, маркер (midsld-1, host+1, sniext) или #блоб.", ErrInvalid)
		}
	case "urp":
		if !urpRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ urp= ждёт b, e, число или маркер (midsld, host+1, sniext).", ErrInvalid)
		}
	case "char":
		if !charRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ char= ждёт ровно один символ (букву/цифру).", ErrInvalid)
		}
	case "spell":
		if !spellRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ spell= ждёт ровно 4 буквы (hoSt).", ErrInvalid)
		}
	case "tcp_flags_unset", "tcp_flags_set":
		if !tcpFlagsRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ %s= ждёт флаги через запятую (SYN,ACK,...).", ErrInvalid, key)
		}
	case "midhost":
		if !midhostRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ midhost= ждёт маркер позиции (midsld и т.п.).", ErrInvalid)
		}
	case "tls_mod":
		if !tlsModRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ tls_mod= ждёт моды через запятую (rnd,rndsni,dupsid,padencap,sni=домен).", ErrInvalid)
		}
	case "ip_autottl", "ip6_autottl":
		if !autottlRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ %s= ждёт диапазон вида -1,4-20.", ErrInvalid, key)
		}
	case "out_range":
		if !outRangeRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ out_range= ждёт вид -dN/-nN/-sN.", ErrInvalid)
		}
	case "pattern":
		if !patternRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ pattern= ждёт hex (0x…), число или имя блоба.", ErrInvalid)
		}
	case "seqovl_pattern":
		if !seqovlPatRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ seqovl_pattern= ждёт имя/шаблон.", ErrInvalid)
		}
	case "blob", "fake_blob", "fallback":
		if !blobValRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ %s= ждёт имя блоба.", ErrInvalid, key)
		}
	case "ip_id":
		if !ipIDRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ ip_id= ждёт zero/rnd/hex/число.", ErrInvalid)
		}
	case "payload":
		if !payloadRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ payload= ждёт список l7-протоколов.", ErrInvalid)
		}
	case "pos":
		if !posRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ pos= ждёт позиции (sniext+1,midsld,...).", ErrInvalid)
		}
	case "host", "sni_first":
		if !hostValRe.MatchString(val) {
			return fmt.Errorf("%w: Ключ %s= ждёт домен.", ErrInvalid, key)
		}
	}
	return nil
}

// KnownBlobNames собирает все известные имена блобов из конфига, встроенных блобов и custom_blobs.tsv.
func KnownBlobNames(cfg *config.Config, customBlobsTSV string) map[string]bool {
	known := make(map[string]bool)
	if cfg != nil {
		for _, n := range cfg.DeclaredBlobNames(true) {
			known[n] = true
		}
	}
	for _, n := range customblobs.BuiltinBlobNames {
		known[n] = true
	}
	if customBlobsTSV != "" {
		st := customblobs.NewStore(customBlobsTSV, "")
		if rows, err := st.List(); err == nil {
			for _, row := range rows {
				if row.Name != "" {
					known[row.Name] = true
				}
			}
		}
	}
	return known
}

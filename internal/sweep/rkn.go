package sweep

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/AloofLibra/zator/internal/netcheck"
)

// DomainTarget — нормализованный домен РКН и URL проверки.
type DomainTarget struct {
	Domain string
	URL    string
}

// NormalizeTestURL разбирает ввод CLI/WebUI: «example.com», «https://example.com/x»
// и т.п. Возвращает URL проверки и домен в нижнем регистре. ok=false — мусор.
// Допустимы только http/https; домен обязан содержать точку и хотя бы одну букву
// (одиночные «1» и «bad» отбрасываются).
func NormalizeTestURL(in string) (rawURL, domain string, ok bool) {
	s := strings.TrimSpace(in)
	if s == "" || strings.ContainsAny(s, " \t\r\n\"'`\\<>") {
		return "", "", false
	}
	full := s
	if !strings.Contains(s, "://") {
		full = "https://" + s
	}
	u, err := url.Parse(full)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return "", "", false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if !validRKNDomain(host) {
		return "", "", false
	}
	// Голый домен без пути — канонический https://domain/.
	if !strings.Contains(s, "://") && u.Path == "" && u.RawQuery == "" && u.Fragment == "" {
		return "https://" + host + "/", host, true
	}
	port := u.Port()
	u.Host = host
	if port != "" {
		u.Host = host + ":" + port
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), host, true
}

func validRKNDomain(host string) bool {
	if !strings.Contains(host, ".") || !netcheck.ValidDomain(host) {
		return false
	}
	return strings.IndexFunc(host, unicode.IsLetter) >= 0
}

// SanitizeDomains нормализует и дедуплицирует домены (последний ввод домена
// заменяет прежний URL). Возвращает цели и отброшенные строки.
func SanitizeDomains(in []string) (targets []DomainTarget, dropped []string) {
	var fields []string
	for _, s := range in {
		fields = append(fields, strings.Fields(s)...)
	}
	for _, f := range fields {
		u, d, ok := NormalizeTestURL(f)
		if !ok {
			dropped = append(dropped, f)
			continue
		}
		kept := targets[:0:0]
		for _, t := range targets {
			if t.Domain != d {
				kept = append(kept, t)
			}
		}
		targets = append(kept, DomainTarget{Domain: d, URL: u})
	}
	return targets, dropped
}

// CoverageRow — строка coverage.tsv: epoch, домен, стратегия, токен
// (ok|warn|fail), размер докачки ("-" если не было), скорость байт/с.
type CoverageRow struct {
	Epoch    int64
	Domain   string
	Strategy int
	Token    string
	DLSize   string
	Speed    int64
}

// TSV форматирует строку без перевода строки.
func (c CoverageRow) TSV() string {
	return strings.Join([]string{
		strconv.FormatInt(c.Epoch, 10), c.Domain, strconv.Itoa(c.Strategy), c.Token, cleanField(c.DLSize), strconv.FormatInt(c.Speed, 10),
	}, "\t")
}

func parseCoverage(rows [][]string) []CoverageRow {
	var out []CoverageRow
	for _, r := range rows {
		if len(r) < 6 || !isPositiveInt(r[2]) {
			continue
		}
		sp, _ := strconv.ParseInt(r[5], 10, 64)
		ep, _ := strconv.ParseInt(r[0], 10, 64)
		n, _ := strconv.Atoi(r[2])
		out = append(out, CoverageRow{Epoch: ep, Domain: r[1], Strategy: n, Token: r[3], DLSize: r[4], Speed: sp})
	}
	return out
}

// RKNWinner — победитель профиля РКН по покрытию доменов. 0 — нет победителя.
type RKNWinner struct {
	OK        int // зелёный победитель
	OKCover   int // сколько доменов покрыто зелёным
	Total     int // всего доменов
	Warn      int // жёлтый победитель (только отчёт)
	WarnCover int
}

// RKNWinners выбирает стратегию профиля РКН: покрытие доменов → сумма скоростей →
// меньший номер. Жёлтые учитываются отдельно и применяться не должны.
func RKNWinners(total int, rows []CoverageRow) RKNWinner {
	ok, okSp := map[int]int{}, map[int]int64{}
	warn, warnSp := map[int]int{}, map[int]int64{}
	for _, r := range rows {
		switch r.Token {
		case TokenOK:
			ok[r.Strategy]++
			okSp[r.Strategy] += r.Speed
		case TokenWarn:
			warn[r.Strategy]++
			warnSp[r.Strategy] += r.Speed
		}
	}
	pick := func(cnt map[int]int, sp map[int]int64) (best, cover int) {
		var bestSp int64
		for s, c := range cnt {
			better := best == 0 || c > cover ||
				(c == cover && sp[s] > bestSp) ||
				(c == cover && sp[s] == bestSp && s < best)
			if better {
				best, cover, bestSp = s, c, sp[s]
			}
		}
		return best, cover
	}
	w := RKNWinner{Total: total}
	w.OK, w.OKCover = pick(ok, okSp)
	w.Warn, w.WarnCover = pick(warn, warnSp)
	return w
}

// DomainWinner — лучшая стратегия одного домена. Rank: 0 ok, 1 warn, 2 fail.
type DomainWinner struct {
	Domain   string
	Strategy int
	Rank     int
}

// Verdict возвращает ok|warn|fail по рангу.
func (d DomainWinner) Verdict() string {
	switch d.Rank {
	case 0:
		return TokenOK
	case 1:
		return TokenWarn
	}
	return TokenFail
}

// DomainWinners выбирает победителя каждого домена: ok > warn > fail, затем
// скорость и меньший номер стратегии. Порядок — по первому появлению домена.
func DomainWinners(rows []CoverageRow) []DomainWinner {
	idx := map[string]int{}
	var out []DomainWinner
	speed := map[string]int64{}
	for _, r := range rows {
		rank := 2
		switch r.Token {
		case TokenOK:
			rank = 0
		case TokenWarn:
			rank = 1
		}
		i, seen := idx[r.Domain]
		if !seen {
			idx[r.Domain] = len(out)
			out = append(out, DomainWinner{Domain: r.Domain, Strategy: r.Strategy, Rank: rank})
			speed[r.Domain] = r.Speed
			continue
		}
		cur := out[i]
		better := rank < cur.Rank ||
			(rank == cur.Rank && r.Speed > speed[r.Domain]) ||
			(rank == cur.Rank && r.Speed == speed[r.Domain] && r.Strategy < cur.Strategy)
		if better {
			out[i] = DomainWinner{Domain: r.Domain, Strategy: r.Strategy, Rank: rank}
			speed[r.Domain] = r.Speed
		}
	}
	return out
}

// OrderStrategies возвращает порядок перебора 1..max для РКН: сначала
// стратегии, зелёные на YouTube, затем жёлтые и не проверявшиеся, затем
// красные. Меняется только очередность, не набор стратегий.
// yt — токены профиля 1 по номеру стратегии.
func OrderStrategies(max int, yt map[int]string) []int {
	type item struct{ rank, n int }
	items := make([]item, 0, max)
	for n := 1; n <= max; n++ {
		rank := 1
		switch t := yt[n]; t {
		case TokenOK:
			rank = 0
		case TokenWarn, "":
			rank = 1
		default:
			rank = 2
		}
		items = append(items, item{rank, n})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].rank != items[j].rank {
			return items[i].rank < items[j].rank
		}
		return items[i].n < items[j].n
	})
	out := make([]int, len(items))
	for i, it := range items {
		out[i] = it.n
	}
	return out
}

// RKNSweepConfig — параметры прогона профиля РКН.
type RKNSweepConfig struct {
	Targets []DomainTarget
	// Max — число стратегий профиля 3 (для порядка и проверки полноты).
	Max int
	// Order — порядок стратегий (по умолчанию 1..Max).
	Order   []int
	TLSPref netcheck.TLSPref
	// Pause — интервал между проверками и доменами (РКН: 60 с по умолчанию).
	Pause      time.Duration
	GreenPause time.Duration

	OnTry    func(domain string, strategy int)
	OnRow    func(row ProgressRow, cov CoverageRow)
	OnPacing func(strategy int, pause time.Duration)
}

// RKNSweepResult — итог (best.rkn).
type RKNSweepResult struct {
	Winner      RKNWinner
	Reference   string // эталонный (первый) домен
	Interrupted bool
	Coverage    []CoverageRow
	Rows        []ProgressRow
}

// SweepRKN выполняет полный проход стратегий по каждому домену по одному
// (против rate-эвристик ТСПУ): лок ставится на домен (tls), проверяется URL
// домена. Победители не применяются. Неполный проход — Interrupted.
func (e *Env) SweepRKN(ctx context.Context, cfg RKNSweepConfig) (RKNSweepResult, error) {
	var res RKNSweepResult
	if err := e.validate(); err != nil {
		return res, err
	}
	if len(cfg.Targets) == 0 {
		return res, ErrNoDomains
	}
	if cfg.Max < 1 {
		return res, fmt.Errorf("%w: %d", ErrNoStrategies, cfg.Max)
	}
	order := cfg.Order
	if len(order) == 0 {
		order = OrderStrategies(cfg.Max, nil)
	}
	res.Reference = cfg.Targets[0].Domain

	var runErr error
loop:
	for di, t := range cfg.Targets {
		if ctx.Err() != nil {
			break
		}
		// Пауза между доменами уже выдержана после последней проверки
		// предыдущего домена.
		for _, s := range order {
			if ctx.Err() != nil {
				break loop
			}
			if cfg.OnTry != nil {
				cfg.OnTry(t.Domain, s)
			}
			tr, err := e.Try(ctx, TryRequest{
				Domain: t.Domain, Strategy: s, Label: t.Domain, URL: t.URL, TLSPref: cfg.TLSPref,
			})
			if err != nil {
				if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					runErr = err
				}
				break loop
			}
			// РКН: unknown не бывает; всё, кроме ok/warn, — fail.
			if tr.Verdict != TokenOK && tr.Verdict != TokenWarn {
				tr.Verdict = TokenFail
			}
			cov := CoverageRow{
				Epoch: e.now().Unix(), Domain: t.Domain, Strategy: s, Token: tr.Verdict,
				DLSize: tr.DLBytes, Speed: int64(tr.Speed + 0.5),
			}
			row := e.progressRow(t.Domain, tr)
			res.Coverage = append(res.Coverage, cov)
			res.Rows = append(res.Rows, row)
			if cfg.OnRow != nil {
				cfg.OnRow(row, cov)
			}

			if di == len(cfg.Targets)-1 && s == order[len(order)-1] {
				continue // после последней проверки пауза не нужна
			}
			pause := cfg.Pause
			if tr.Verdict == TokenOK && tr.Full && cfg.GreenPause > 0 {
				pause = cfg.GreenPause
			}
			if cfg.OnPacing != nil {
				cfg.OnPacing(s, pause)
			}
			if err := e.sleep(ctx, pause); err != nil {
				break loop
			}
		}
	}

	probed := map[int]bool{}
	for _, c := range res.Coverage {
		probed[c.Strategy] = true
	}
	res.Interrupted = len(probed) < len(order)
	res.Winner = RKNWinners(len(cfg.Targets), res.Coverage)
	return res, runErr
}

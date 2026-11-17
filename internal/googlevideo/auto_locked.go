package googlevideo

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator/internal/fsutil"
)

// Источники auto-lock для UI / seed-воркера.
const (
	SourceLearned = "learned"
	SourceSeed    = "seed"
)

// AutoLock — выученная стратегия для hostkey (обычно sn-<id>) в профиле askey.
type AutoLock struct {
	Askey    string `json:"askey"`
	Scope    string `json:"scope"`
	Host     string `json:"host"`
	Strategy int    `json:"strategy"`
	// Source — seed|learned (пусто = learned).
	Source string `json:"source,omitempty"`
}

// ParseAutoLockedTSV читает auto_locked.tsv:
//
//	askey\thost\tstrat
//	askey\tscope\thost\tstrat
//	askey\tscope\thost\tstrat\tsource
func ParseAutoLockedTSV(path string) ([]AutoLock, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []AutoLock
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		var askey, scope, host, stratRaw, source string
		switch len(fields) {
		case 3:
			askey, scope, host, stratRaw = fields[0], "default", fields[1], fields[2]
		case 4:
			// askey scope host strat  ИЛИ  askey host strat source
			if _, err := strconv.Atoi(strings.TrimSpace(fields[3])); err == nil {
				askey, scope, host, stratRaw = fields[0], fields[1], fields[2], fields[3]
			} else {
				askey, scope, host, stratRaw, source = fields[0], "default", fields[1], fields[2], fields[3]
			}
		case 5:
			askey, scope, host, stratRaw, source = fields[0], fields[1], fields[2], fields[3], fields[4]
		default:
			continue
		}
		askey = strings.TrimSpace(askey)
		scope = strings.TrimSpace(scope)
		host = strings.TrimSpace(strings.ToLower(host))
		source = strings.TrimSpace(strings.ToLower(source))
		if askey == "" || host == "" {
			continue
		}
		if scope == "" {
			scope = "default"
		}
		n, err := strconv.Atoi(strings.TrimSpace(stratRaw))
		if err != nil || n < 0 {
			continue
		}
		if source != SourceSeed && source != SourceLearned {
			source = ""
		}
		out = append(out, AutoLock{Askey: askey, Scope: scope, Host: host, Strategy: n, Source: source})
	}
	return out, sc.Err()
}

// FilterAskey возвращает записи только для указанного askey (например "2").
func FilterAskey(locks []AutoLock, askey string) []AutoLock {
	if askey == "" {
		return locks
	}
	var out []AutoLock
	for _, l := range locks {
		if l.Askey == askey {
			out = append(out, l)
		}
	}
	return out
}

// MergeAutoLocked объединяет live (/tmp) и backup (state), приоритет у live по ключу askey|scope|host.
func MergeAutoLocked(live, backup []AutoLock) []AutoLock {
	type key struct{ a, s, h string }
	seen := map[key]AutoLock{}
	for _, l := range backup {
		seen[key{l.Askey, l.Scope, l.Host}] = l
	}
	for _, l := range live {
		seen[key{l.Askey, l.Scope, l.Host}] = l
	}
	out := make([]AutoLock, 0, len(seen))
	for _, l := range seen {
		out = append(out, l)
	}
	return out
}

// MostCommonStrategy — most frequent learned/legacy strategy; seed rows are
// proposals, not evidence (ties choose the smaller number).
func MostCommonStrategy(locks []AutoLock) (strat, count int) {
	if len(locks) == 0 {
		return 0, 0
	}
	counts := map[int]int{}
	for _, l := range locks {
		if l.Strategy >= 1 && l.Source != SourceSeed {
			counts[l.Strategy]++
		}
	}
	best, bestN := 0, 0
	for s, n := range counts {
		if n > bestN || (n == bestN && (best == 0 || s < best)) {
			best, bestN = s, n
		}
	}
	return best, bestN
}

// LockedHostSet — множество hostkey из локов.
func LockedHostSet(locks []AutoLock) map[string]AutoLock {
	out := map[string]AutoLock{}
	for _, l := range locks {
		out[l.Host] = l
	}
	return out
}

// MissingHostKeys возвращает sn-* из hosts, для которых ещё нет auto-lock.
func MissingHostKeys(hosts []string, locks []AutoLock) []string {
	have := LockedHostSet(locks)
	seen := map[string]struct{}{}
	var out []string
	for _, h := range hosts {
		key, ok := SNIHostKey(h)
		if !ok || key == "" {
			continue
		}
		if _, exists := have[key]; exists {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// CandidateStrategies — узкий набор для seed-probe: transfer → default → топ выученных → хвост 1..tailMax.
func CandidateStrategies(locks []AutoLock, defaultStrat, maxStrat, tailMax int) []int {
	if defaultStrat < 1 {
		defaultStrat = 41
	}
	if maxStrat < 1 {
		maxStrat = 44
	}
	if tailMax < 0 {
		tailMax = 0
	}
	seen := map[int]struct{}{}
	var out []int
	add := func(n int) {
		if n < 1 || n > maxStrat {
			return
		}
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	type pair struct{ s, n int }
	var ranked []pair
	counts := map[int]int{}
	for _, l := range locks {
		if l.Strategy >= 1 && l.Source != SourceSeed {
			counts[l.Strategy]++
		}
	}
	best, bestN := 0, 0
	for s, n := range counts {
		if n > bestN || n == bestN && (best == 0 || s < best) {
			best, bestN = s, n
		}
		ranked = append(ranked, pair{s, n})
	}
	if bestN > 0 {
		add(best)
	}
	add(defaultStrat)
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].n != ranked[j].n {
			return ranked[i].n > ranked[j].n
		}
		return ranked[i].s < ranked[j].s
	})
	for i, p := range ranked {
		if i >= 5 {
			break
		}
		add(p.s)
	}
	for i := 1; i <= tailMax && i <= maxStrat; i++ {
		add(i)
	}
	return out
}

// FormatAutoLockedLine сериализует одну запись.
func FormatAutoLockedLine(l AutoLock) string {
	scope := l.Scope
	if scope == "" {
		scope = "default"
	}
	src := l.Source
	if src == "" {
		src = SourceLearned
	}
	if scope == "default" {
		return fmt.Sprintf("%s\t%s\t%d\t%s", l.Askey, l.Host, l.Strategy, src)
	}
	return fmt.Sprintf("%s\t%s\t%s\t%d\t%s", l.Askey, scope, l.Host, l.Strategy, src)
}

// UpsertAutoLock записывает/обновляет лок в TSV (live path). Не затирает
// существующий learned более слабым seed.
func UpsertAutoLock(path string, lock AutoLock) error {
	if lock.Askey == "" || lock.Host == "" || lock.Strategy < 1 {
		return fmt.Errorf("googlevideo: некорректный auto-lock")
	}
	if lock.Scope == "" {
		lock.Scope = "default"
	}
	if lock.Source == "" {
		lock.Source = SourceLearned
	}
	existing, err := ParseAutoLockedTSV(path)
	if err != nil {
		return err
	}
	type key struct{ a, s, h string }
	k := key{lock.Askey, lock.Scope, lock.Host}
	merged := make([]AutoLock, 0, len(existing)+1)
	found := false
	for _, e := range existing {
		ek := key{e.Askey, e.Scope, e.Host}
		if ek != k {
			merged = append(merged, e)
			continue
		}
		found = true
		// Не даём seed перезаписать learned.
		if e.Source != SourceSeed && e.Source != "" && lock.Source == SourceSeed {
			merged = append(merged, e)
			continue
		}
		if e.Source == "" && lock.Source == SourceSeed {
			merged = append(merged, e)
			continue
		}
		merged = append(merged, lock)
	}
	if !found {
		merged = append(merged, lock)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Askey != merged[j].Askey {
			return merged[i].Askey < merged[j].Askey
		}
		if merged[i].Scope != merged[j].Scope {
			return merged[i].Scope < merged[j].Scope
		}
		return merged[i].Host < merged[j].Host
	})
	var b strings.Builder
	for _, l := range merged {
		b.WriteString(FormatAutoLockedLine(l))
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsutil.AtomicWriteFile(path, []byte(b.String()), 0o644)
}

// PickFQDNForHostKey выбирает любой rrN---sn-… FQDN из списка для данного sn-ключа.
func PickFQDNForHostKey(hosts []string, hostKey string) string {
	hostKey = strings.ToLower(strings.TrimSpace(hostKey))
	for _, h := range hosts {
		if k, ok := SNIHostKey(h); ok && k == hostKey {
			return strings.ToLower(strings.Trim(strings.TrimSpace(h), "."))
		}
	}
	return ""
}

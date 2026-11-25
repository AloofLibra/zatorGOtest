package orchestra

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator/internal/fsutil"
)

// LockRemapRule задаёт однопроходный перенос номера стратегии при переинжекте custom_strategies.
type LockRemapRule struct {
	Profile     int
	Proto       Proto
	OldStrategy int
	NewStrategy int // <= 0 означает удаление лока (стратегия выключена/удалена)
}

// LockMaxRule задаёт верхнюю границу допустимой стратегии (превышающие сбрасываются в auto).
type LockMaxRule struct {
	Profile     int
	Proto       Proto
	MaxStrategy int
}

// AutoLockRemapRule задаёт перенос номера стратегии в auto_locked.tsv по ключу askey ("tls"|"quic").
type AutoLockRemapRule struct {
	Key         string
	OldStrategy int
	NewStrategy int
}

// AutoLockMaxRule задаёт верхнюю границу стратегии для ключа askey в auto_locked.tsv.
type AutoLockMaxRule struct {
	Key         string
	MaxStrategy int
}

// RemapLocks атомарно переназначает номера стратегий в locked.tsv, locked.manual.tsv,
// scopes/mark_*.tsv и переданных auto_locked.tsv файлах (один проход — без цепных коллизий).
func (s *Store) RemapLocks(
	rules []LockRemapRule,
	maxRules []LockMaxRule,
	autoRules []AutoLockRemapRule,
	autoMaxRules []AutoLockMaxRule,
	autoLockedPaths ...string,
) (moved int, dropped int, err error) {
	if len(rules) == 0 && len(maxRules) == 0 && len(autoRules) == 0 && len(autoMaxRules) == 0 {
		return 0, 0, nil
	}

	type remapKey struct {
		profile string
		proto   Proto
		strat   int
	}
	type profKey struct {
		profile string
		proto   Proto
	}
	remapMap := make(map[remapKey]int, len(rules))
	for _, r := range rules {
		remapMap[remapKey{strconv.Itoa(r.Profile), r.Proto, r.OldStrategy}] = r.NewStrategy
	}
	maxMap := make(map[profKey]int, len(maxRules))
	for _, m := range maxRules {
		k := profKey{strconv.Itoa(m.Profile), m.Proto}
		if cur, ok := maxMap[k]; !ok || m.MaxStrategy > cur {
			maxMap[k] = m.MaxStrategy
		}
	}

	type autoKey struct {
		key   string
		strat int
	}
	autoRemap := make(map[autoKey]int, len(autoRules))
	for _, a := range autoRules {
		autoRemap[autoKey{strings.ToLower(a.Key), a.OldStrategy}] = a.NewStrategy
	}
	autoMax := make(map[string]int, len(autoMaxRules))
	for _, am := range autoMaxRules {
		k := strings.ToLower(am.Key)
		if cur, ok := autoMax[k]; !ok || am.MaxStrategy > cur {
			autoMax[k] = am.MaxStrategy
		}
	}

	err = s.withLock(func() error {
		lockTargets := []string{s.LockedPath(), s.ManualPath()}
		entries, rErr := os.ReadDir(s.ScopesDir())
		if rErr == nil {
			for _, e := range entries {
				if !e.IsDir() && scopeFileRe.MatchString(e.Name()) {
					lockTargets = append(lockTargets, filepath.Join(s.ScopesDir(), e.Name()))
				}
			}
		}

		for _, path := range lockTargets {
			isScoped := filepath.Dir(path) == s.ScopesDir()
			scope := ""
			if isScoped {
				m := scopeFileRe.FindStringSubmatch(filepath.Base(path))
				if m != nil {
					scope = "mark:" + m[1]
				}
			}
			f, fErr := s.read(path)
			if fErr != nil {
				return fErr
			}
			if len(f.rows) == 0 {
				continue
			}

			changed := false
			out := make([]string, 0, len(f.rows))
			for _, rw := range f.rows {
				if !rw.ok {
					out = append(out, rw.raw)
					continue
				}
				pk := profKey{rw.rec.Profile, rw.rec.Proto}
				cur := int(rw.rec.Strategy)
				rk := remapKey{rw.rec.Profile, rw.rec.Proto, cur}
				if nv, hasRemap := remapMap[rk]; hasRemap {
					changed = true
					if nv <= 0 {
						dropped++
						continue
					}
					moved++
					if isScoped || rw.cols != 4 {
						out = append(out, formatLockRow(rw.rec.Profile, rw.rec.Proto, Strategy(nv)))
					} else {
						out = append(out, fmt.Sprintf("%s\t%s\t%s\t%d", scopeOrDefault(rw.rec.Scope, scope), rw.rec.Profile, rw.rec.Proto, nv))
					}
					continue
				}
				if maxV, hasMax := maxMap[pk]; hasMax && cur > maxV {
					changed = true
					dropped++
					continue
				}
				out = append(out, rw.raw)
			}
			if changed {
				if len(out) == 0 && isScoped {
					if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
						return rmErr
					}
					continue
				}
				if wErr := writeLines(path, out); wErr != nil {
					return wErr
				}
			}
		}

		// Обработка auto_locked.tsv файлов (поддерживает 3/4/5 колонок).
		for _, ap := range autoLockedPaths {
			if ap == "" {
				continue
			}
			lines, lErr := readLines(ap)
			if lErr != nil || len(lines) == 0 {
				continue
			}
			changed := false
			out := make([]string, 0, len(lines))
			for _, ln := range lines {
				trimLn := strings.TrimSpace(ln)
				if trimLn == "" || strings.HasPrefix(trimLn, "#") {
					out = append(out, ln)
					continue
				}
				fields := strings.Split(ln, "\t")
				stratIdx := -1
				switch len(fields) {
				case 3:
					stratIdx = 2
				case 4:
					if _, aErr := strconv.Atoi(strings.TrimSpace(fields[3])); aErr == nil {
						stratIdx = 3
					} else {
						stratIdx = 2
					}
				case 5:
					stratIdx = 3
				}
				if stratIdx < 0 {
					out = append(out, ln)
					continue
				}
				askey := strings.ToLower(strings.TrimSpace(fields[0]))
				cur, pErr := strconv.Atoi(strings.TrimSpace(fields[stratIdx]))
				if pErr != nil {
					out = append(out, ln)
					continue
				}
				if nv, hasRemap := autoRemap[autoKey{askey, cur}]; hasRemap {
					changed = true
					if nv <= 0 {
						dropped++
						continue
					}
					moved++
					fields[stratIdx] = strconv.Itoa(nv)
					out = append(out, strings.Join(fields, "\t"))
					continue
				}
				if maxV, hasMax := autoMax[askey]; hasMax && cur > maxV {
					changed = true
					dropped++
					continue
				}
				out = append(out, ln)
			}
			if changed {
				var b strings.Builder
				for _, ln := range out {
					b.WriteString(ln)
					b.WriteByte('\n')
				}
				if wErr := fsutil.AtomicWriteFile(ap, []byte(b.String()), 0o644); wErr != nil {
					return wErr
				}
			}
		}
		return nil
	})
	return moved, dropped, err
}

func scopeOrDefault(recScope, fallback string) string {
	if recScope != "" {
		return recScope
	}
	if fallback != "" {
		return fallback
	}
	return ScopeDefault
}

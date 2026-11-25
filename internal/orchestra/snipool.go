package orchestra

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/fsutil"
)

// Имена файлов SNI-пула и политики чередования.
const (
	SNIPoolFileName       = "sni_pool.tsv"
	SNIPoolPolicyFileName = "sni_pool_policy.tsv"
	SNIPoolPinFileName    = "sni_pool.pin"

	SNIPoolMaxDomains = 32
	SNIPolicyRR       = "rr"
	SNIPolicyRand     = "rand"
)

// Ошибки управления SNI-наборами.
var (
	ErrSNISetProtected = errors.New("orchestra: project SNI set cannot be deleted")
	ErrSNISetInUse     = errors.New("orchestra: SNI set is referenced by sni_pool.tsv")
	ErrSNISetNotFound  = errors.New("orchestra: SNI set not found")
)

var (
	sniSetNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,31}$`)
	sniSetHeadRe = regexp.MustCompile(`^#\s*set:\s*([A-Za-z0-9_]+)\s*$`)
)

// SNISet — именованный набор доменов для клон-SNI.
type SNISet struct {
	Name    string   `json:"name"`
	Origin  string   `json:"origin"` // "zator" | "user"
	Domains []string `json:"domains"`
}

// SNIProfileInfo — сводное состояние SNI одного TLS-профиля для API/CLI/UI.
type SNIProfileInfo struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Pin       string   `json:"pin"`
	Pool      string   `json:"pool"`
	Set       string   `json:"set"`
	Policy    string   `json:"policy"`
	Effective []string `json:"effective"`
}

// SNIPayload — агрегат профилей и наборов для вкладки «Блобы и SNI».
type SNIPayload struct {
	Profiles []SNIProfileInfo `json:"profiles"`
	Sets     []SNISet         `json:"sets"`
}

// ValidSNISetName проверяет имя SNI-набора (`^[a-z0-9][a-z0-9_]{0,31}$`).
func ValidSNISetName(name string) bool {
	return sniSetNameRe.MatchString(name)
}

// ValidSNIDomain проверяет домен для SNI-пула/пина.
func ValidSNIDomain(d string) bool {
	return len(d) > 0 && len(d) <= MaxSNILength && sniRe.MatchString(d)
}

// ValidSNIPolicy проверяет политику чередования (`rr` | `rand`).
func ValidSNIPolicy(policy string) bool {
	return policy == SNIPolicyRR || policy == SNIPolicyRand
}

// SNIPoolPath возвращает путь к sni_pool.tsv.
func (s *Store) SNIPoolPath() string { return filepath.Join(s.dir, SNIPoolFileName) }

// SNIPoolPolicyPath возвращает путь к sni_pool_policy.tsv.
func (s *Store) SNIPoolPolicyPath() string { return filepath.Join(s.dir, SNIPoolPolicyFileName) }

// SNIPoolPinPath возвращает путь к sni_pool.pin.
func (s *Store) SNIPoolPinPath() string { return filepath.Join(s.dir, SNIPoolPinFileName) }

// ParseSNIDomains разбирает произвольный список доменов (пробелы, переводы строк, запятые),
// нормализует в нижний регистр, дедуплицирует и проверяет до SNIPoolMaxDomains.
func ParseSNIDomains(raw string) ([]string, error) {
	fields := strings.FieldsFunc(strings.ReplaceAll(raw, ",", " "), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\r' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, f := range fields {
		d := strings.ToLower(strings.TrimSpace(f))
		if d == "" {
			continue
		}
		if !ValidSNIDomain(d) {
			return nil, fmt.Errorf("%w: некорректный домен SNI %q", ErrInvalid, f)
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		if len(out) >= SNIPoolMaxDomains {
			return nil, fmt.Errorf("%w: слишком много доменов (максимум %d)", ErrInvalid, SNIPoolMaxDomains)
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: укажите хотя бы один домен", ErrInvalid)
	}
	return out, nil
}

func parseSetsFile(path, origin string) (order []string, sets map[string]SNISet, err error) {
	sets = make(map[string]SNISet)
	lines, err := readLines(path)
	if err != nil {
		return nil, nil, err
	}
	cur := ""
	seenDom := map[string]map[string]bool{}
	for _, ln := range lines {
		ln = strings.TrimRight(ln, "\r")
		if m := sniSetHeadRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			cur = strings.ToLower(m[1])
			if _, exists := sets[cur]; !exists {
				order = append(order, cur)
				sets[cur] = SNISet{Name: cur, Origin: origin, Domains: []string{}}
				seenDom[cur] = make(map[string]bool)
			} else {
				sets[cur] = SNISet{Name: cur, Origin: origin, Domains: []string{}}
				seenDom[cur] = make(map[string]bool)
			}
			continue
		}
		if cur == "" {
			continue
		}
		d := strings.ToLower(strings.TrimSpace(ln))
		if d == "" || strings.HasPrefix(d, "#") {
			continue
		}
		if !ValidSNIDomain(d) || seenDom[cur][d] || len(sets[cur].Domains) >= SNIPoolMaxDomains {
			continue
		}
		seenDom[cur][d] = true
		st := sets[cur]
		st.Domains = append(st.Domains, d)
		sets[cur] = st
	}
	return order, sets, nil
}

// LoadSNISets читает проектные и пользовательские SNI-наборы (пользовательские перекрывают проектные).
func LoadSNISets(zatorPath, userPath string) ([]SNISet, map[string]SNISet, error) {
	zOrder, zMap, err := parseSetsFile(zatorPath, "zator")
	if err != nil {
		return nil, nil, err
	}
	uOrder, uMap, err := parseSetsFile(userPath, "user")
	if err != nil {
		return nil, nil, err
	}
	merged := make(map[string]SNISet, len(zMap)+len(uMap))
	var names []string
	seen := make(map[string]bool)
	for _, n := range zOrder {
		merged[n] = zMap[n]
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, n := range uOrder {
		merged[n] = uMap[n]
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	list := make([]SNISet, 0, len(names))
	for _, n := range names {
		list = append(list, merged[n])
	}
	return list, merged, nil
}

// EnsureDefaultSNIPool создаёт дефолтный `sni_pool.tsv` (`1\t@google`, `2\t@google`),
// если файл ещё не существует.
func (s *Store) EnsureDefaultSNIPool() error {
	p := s.SNIPoolPath()
	return s.withLock(func() error {
		if _, err := os.Stat(p); err == nil {
			return nil
		}
		return writeLines(p, []string{"1\t@google", "2\t@google"})
	})
}

// SNIPoolRows возвращает сырые строки второго поля `sni_pool.tsv` по профилям.
func (s *Store) SNIPoolRows() (map[string][]string, error) {
	lines, err := readLines(s.SNIPoolPath())
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string)
	for _, ln := range lines {
		e, ok := parseOverrideLine(ln)
		if !ok || !digitsRe.MatchString(e.Profile) {
			continue
		}
		out[e.Profile] = append(out[e.Profile], e.Value)
	}
	return out, nil
}

// AssignSNIPoolSet назначает профилю готовый набор (`N\t@name`).
func (s *Store) AssignSNIPoolSet(profile, setName, zatorPath, userPath string) error {
	profile = strings.TrimSpace(profile)
	setName = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(setName), "@"))
	if !IsCloneProfile(profile) {
		return fmt.Errorf("%w: Некорректный профиль", ErrInvalid)
	}
	if !ValidSNISetName(setName) {
		return fmt.Errorf("%w: Некорректное имя набора", ErrInvalid)
	}
	_, sets, err := LoadSNISets(zatorPath, userPath)
	if err != nil {
		return err
	}
	if _, ok := sets[setName]; !ok {
		return fmt.Errorf("%w: Набор @%s не найден", ErrSNISetNotFound, setName)
	}
	return s.replacePoolRows(profile, []string{"@" + setName})
}

// SetSNIPoolCustom задаёт собственный список доменов профиля (до 32) в `sni_pool.tsv`.
func (s *Store) SetSNIPoolCustom(profile string, domains []string) error {
	profile = strings.TrimSpace(profile)
	if !IsCloneProfile(profile) {
		return fmt.Errorf("%w: Некорректный профиль", ErrInvalid)
	}
	if len(domains) == 0 || len(domains) > SNIPoolMaxDomains {
		return fmt.Errorf("%w: список должен содержать 1..%d доменов", ErrInvalid, SNIPoolMaxDomains)
	}
	norm := make([]string, 0, len(domains))
	seen := make(map[string]bool, len(domains))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if !ValidSNIDomain(d) {
			return fmt.Errorf("%w: некорректный домен SNI %q", ErrInvalid, d)
		}
		if !seen[d] {
			seen[d] = true
			norm = append(norm, d)
		}
	}
	return s.replacePoolRows(profile, norm)
}

// ClearSNIPool удаляет все строки профиля из `sni_pool.tsv`.
func (s *Store) ClearSNIPool(profile string) error {
	profile = strings.TrimSpace(profile)
	if !IsCloneProfile(profile) {
		return fmt.Errorf("%w: Некорректный профиль", ErrInvalid)
	}
	return s.replacePoolRows(profile, nil)
}

func (s *Store) replacePoolRows(profile string, values []string) error {
	path := s.SNIPoolPath()
	return s.withLock(func() error {
		lines, err := readLines(path)
		if err != nil {
			return err
		}
		out := make([]string, 0, len(lines)+len(values))
		for _, ln := range lines {
			if e, ok := parseOverrideLine(ln); ok && e.Profile == profile {
				continue
			}
			out = append(out, ln)
		}
		for _, v := range values {
			out = append(out, profile+"\t"+v)
		}
		return writeLines(path, out)
	})
}

// SNIPoolPolicies возвращает карту `profile -> rr|rand` (default — не записано = "rr").
func (s *Store) SNIPoolPolicies() (map[string]string, error) {
	lines, err := readLines(s.SNIPoolPolicyPath())
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for _, ln := range lines {
		e, ok := parseOverrideLine(ln)
		if ok && digitsRe.MatchString(e.Profile) && ValidSNIPolicy(e.Value) {
			out[e.Profile] = e.Value
		}
	}
	return out, nil
}

// SetSNIPoolPolicy устанавливает политику чередования (`rr` | `rand`) для профиля.
func (s *Store) SetSNIPoolPolicy(profile, policy string) error {
	profile = strings.TrimSpace(profile)
	policy = strings.ToLower(strings.TrimSpace(policy))
	if !IsCloneProfile(profile) {
		return fmt.Errorf("%w: Некорректный профиль", ErrInvalid)
	}
	if !ValidSNIPolicy(policy) {
		return fmt.Errorf("%w: Некорректная политика: %s", ErrInvalid, policy)
	}
	path := s.SNIPoolPolicyPath()
	return s.withLock(func() error {
		lines, err := readLines(path)
		if err != nil {
			return err
		}
		out := make([]string, 0, len(lines)+1)
		replaced := false
		for _, ln := range lines {
			if e, ok := parseOverrideLine(ln); ok && e.Profile == profile {
				if policy == SNIPolicyRR {
					continue // rr — значение по умолчанию
				}
				if !replaced {
					out = append(out, profile+"\t"+policy)
					replaced = true
				}
				continue
			}
			out = append(out, ln)
		}
		if policy == SNIPolicyRand && !replaced {
			out = append(out, profile+"\trand")
		}
		return writeLines(path, out)
	})
}

// WriteUserSNISet создаёт или заменяет пользовательский набор `# set: name` в sni_user.txt.
func (s *Store) WriteUserSNISet(userPath, name string, domains []string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if !ValidSNISetName(name) {
		return fmt.Errorf("%w: Имя набора: строчные латиница/цифры/_ (1..32)", ErrInvalid)
	}
	if len(domains) == 0 || len(domains) > SNIPoolMaxDomains {
		return fmt.Errorf("%w: укажите 1..%d доменов", ErrInvalid, SNIPoolMaxDomains)
	}
	norm := make([]string, 0, len(domains))
	seen := make(map[string]bool, len(domains))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if !ValidSNIDomain(d) {
			return fmt.Errorf("%w: некорректный домен SNI %q", ErrInvalid, d)
		}
		if !seen[d] {
			seen[d] = true
			norm = append(norm, d)
		}
	}
	return s.withLock(func() error {
		lines, err := readLines(userPath)
		if err != nil {
			return err
		}
		if len(lines) == 0 {
			lines = []string{
				"# Ваши SNI-наборы для SNI-пула клон-режима zator.",
			}
		}
		out := make([]string, 0, len(lines)+len(norm)+2)
		skip := false
		for _, ln := range lines {
			trimLn := strings.TrimSpace(strings.TrimRight(ln, "\r"))
			if m := sniSetHeadRe.FindStringSubmatch(trimLn); m != nil {
				if strings.EqualFold(m[1], name) {
					skip = true
					continue
				}
				skip = false
			}
			if !skip {
				out = append(out, ln)
			}
		}
		// Убираем завершающие пустые строки, чтобы секции выглядели опрятно.
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		out = append(out, "", "# set: "+name)
		out = append(out, norm...)
		return writeLines(userPath, out)
	})
}

// DeleteUserSNISet удаляет пользовательский набор из `sni_user.txt`.
// Запрещает удаление проектного набора (если отсутствует user-секция) и наборов,
// на которые точно ссылается второе поле `@<name>` в `sni_pool.tsv`.
func (s *Store) DeleteUserSNISet(zatorPath, userPath, name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if !ValidSNISetName(name) {
		return fmt.Errorf("%w: Некорректное имя набора", ErrInvalid)
	}
	return s.withLock(func() error {
		_, uMap, err := parseSetsFile(userPath, "user")
		if err != nil {
			return err
		}
		if _, exists := uMap[name]; !exists {
			_, zMap, zErr := parseSetsFile(zatorPath, "zator")
			if zErr == nil {
				if _, isProj := zMap[name]; isProj {
					return fmt.Errorf("%w: Проектный набор %s нельзя удалить", ErrSNISetProtected, name)
				}
			}
			return fmt.Errorf("%w: Набор %s не найден в sni_user.txt", ErrSNISetNotFound, name)
		}

		poolLines, err := readLines(s.SNIPoolPath())
		if err != nil {
			return err
		}
		wantRef := "@" + name
		for _, ln := range poolLines {
			if e, ok := parseOverrideLine(ln); ok && strings.EqualFold(e.Value, wantRef) {
				return fmt.Errorf("%w: Набор @%s используется профилем %s", ErrSNISetInUse, name, e.Profile)
			}
		}

		lines, err := readLines(userPath)
		if err != nil {
			return err
		}
		out := make([]string, 0, len(lines))
		skip := false
		for _, ln := range lines {
			trimLn := strings.TrimSpace(strings.TrimRight(ln, "\r"))
			if m := sniSetHeadRe.FindStringSubmatch(trimLn); m != nil {
				if strings.EqualFold(m[1], name) {
					skip = true
					continue
				}
				skip = false
			}
			if !skip {
				out = append(out, ln)
			}
		}
		return writeLines(userPath, out)
	})
}

// BuildSNIPayload собирает состояние SNI-пулов и наборов для WebUI/CLI.
func (s *Store) BuildSNIPayload(zatorPath, userPath string, profileTitles map[string]string) (SNIPayload, error) {
	sniMap, err := s.OverrideMap(OverrideSNI)
	if err != nil {
		return SNIPayload{}, err
	}
	poolRows, err := s.SNIPoolRows()
	if err != nil {
		return SNIPayload{}, err
	}
	policies, err := s.SNIPoolPolicies()
	if err != nil {
		return SNIPayload{}, err
	}
	sets, setsMap, err := LoadSNISets(zatorPath, userPath)
	if err != nil {
		return SNIPayload{}, err
	}

	profiles := make([]SNIProfileInfo, 0, len(OverrideProfiles))
	for _, id := range OverrideProfiles {
		k := strconv.Itoa(id)
		pol := policies[k]
		if pol != SNIPolicyRand {
			pol = SNIPolicyRR
		}
		polLabel := "по кругу"
		if pol == SNIPolicyRand {
			polLabel = "случайно"
		}
		rows := poolRows[k]
		setName := ""
		eff := make([]string, 0)
		seen := make(map[string]bool)
		for _, r := range rows {
			if strings.HasPrefix(r, "@") {
				nm := strings.ToLower(strings.TrimPrefix(r, "@"))
				if setName == "" {
					setName = nm
				}
				if st, ok := setsMap[nm]; ok {
					for _, d := range st.Domains {
						if !seen[d] && len(eff) < SNIPoolMaxDomains {
							seen[d] = true
							eff = append(eff, d)
						}
					}
				}
				continue
			}
			d := strings.ToLower(r)
			if !seen[d] && len(eff) < SNIPoolMaxDomains {
				seen[d] = true
				eff = append(eff, d)
			}
		}

		poolDesc := ""
		if len(rows) > 0 {
			if setName != "" {
				poolDesc = fmt.Sprintf("набор %s (%d имён, %s, автосмена)", setName, len(eff), polLabel)
			} else {
				poolDesc = fmt.Sprintf("свой список (%d имён, %s, автосмена)", len(eff), polLabel)
			}
		}

		title := profileTitles[k]
		if title == "" {
			title = "Профиль " + k
		}
		profiles = append(profiles, SNIProfileInfo{
			ID:        k,
			Title:     title,
			Pin:       sniMap[k],
			Pool:      poolDesc,
			Set:       setName,
			Policy:    pol,
			Effective: eff,
		})
	}
	return SNIPayload{Profiles: profiles, Sets: sets}, nil
}

// PinSNIPool записывает метку времени в sni_pool.pin (замораживает ротацию на время прогонов).
func (s *Store) PinSNIPool(now time.Time) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return fsutil.AtomicWriteFile(s.SNIPoolPinPath(), []byte(strconv.FormatInt(now.Unix(), 10)+"\n"), 0o644)
}

// UnpinSNIPool удаляет sni_pool.pin.
func (s *Store) UnpinSNIPool() error {
	err := os.Remove(s.SNIPoolPinPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// SortedSetNames возвращает отсортированные имена наборов.
func SortedSetNames(m map[string]SNISet) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

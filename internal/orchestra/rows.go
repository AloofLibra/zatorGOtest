package orchestra

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/AloofLibra/zator/internal/fsutil"
)

// row — строка TSV-файла. Нераспознанные строки (комментарии, пустые,
// повреждённые) сохраняются как есть и при перезаписи не теряются.
type row struct {
	raw  string
	ok   bool
	cols int // 2, 3 или 4 колонки у распознанной строки
	rec  Record
}

// tsvFile — содержимое файла в виде строк.
type tsvFile struct {
	rows []row
}

// readLines читает файл и разбивает его на строки (без \r\n). Отсутствующий
// файл — не ошибка (nil).
func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

func writeLines(path string, lines []string) error {
	var b strings.Builder
	for _, ln := range lines {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	return fsutil.AtomicWriteFile(path, []byte(b.String()), 0)
}

// parseLockLine разбирает строку locked.tsv / locked.manual.tsv (как
// locked_parse_line в locked.lua): 4 колонки scope,profile,proto,strategy;
// 3 колонки profile,proto,strategy (scope=default); 2 колонки profile,strategy
// (scope=default, proto=tls).
func parseLockLine(line string) (Record, int, bool) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
		return Record{}, 0, false
	}
	f := strings.Split(line, "\t")
	for i := range f {
		f[i] = strings.TrimSpace(f[i])
	}
	var scope, profile, proto, strat string
	switch len(f) {
	case 4:
		scope, profile, proto, strat = f[0], f[1], f[2], f[3]
		if scope == "" {
			return Record{}, 0, false
		}
	case 3:
		scope, profile, proto, strat = ScopeDefault, f[0], f[1], f[2]
	case 2:
		scope, profile, proto, strat = ScopeDefault, f[0], string(ProtoTLS), f[1]
	default:
		return Record{}, 0, false
	}
	return buildRecord(scope, profile, proto, strat, len(f))
}

// parseScopedLine разбирает строку scopes/mark_N.tsv: ровно profile,proto,strategy.
func parseScopedLine(scope, line string) (Record, int, bool) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
		return Record{}, 0, false
	}
	f := strings.Split(line, "\t")
	if len(f) != 3 {
		return Record{}, 0, false
	}
	return buildRecord(scope, strings.TrimSpace(f[0]), strings.TrimSpace(f[1]), strings.TrimSpace(f[2]), 3)
}

func buildRecord(scope, profile, proto, strat string, cols int) (Record, int, bool) {
	if profile == "" || proto == "" || strat == "" || !digitsRe.MatchString(strat) {
		return Record{}, 0, false
	}
	p := Proto(strings.ToLower(proto))
	if !p.Valid() {
		return Record{}, 0, false
	}
	n, err := ParseStrategy(strat)
	if err != nil {
		return Record{}, 0, false
	}
	return Record{
		Scope:    strings.ToLower(scope),
		Profile:  strings.ToLower(profile),
		Proto:    p,
		Strategy: n,
	}, cols, true
}

// readLockFile читает locked.tsv-подобный файл. Если scope != "", файл —
// per-mark (3 колонки, scope задан именем файла).
func readLockFile(path, scope string) (*tsvFile, error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	f := &tsvFile{rows: make([]row, 0, len(lines))}
	for _, ln := range lines {
		var (
			rec  Record
			cols int
			ok   bool
		)
		if scope == "" {
			rec, cols, ok = parseLockLine(ln)
		} else {
			rec, cols, ok = parseScopedLine(scope, ln)
		}
		f.rows = append(f.rows, row{raw: ln, ok: ok, cols: cols, rec: rec})
	}
	return f, nil
}

// lines возвращает строки файла для записи.
func (f *tsvFile) lines() []string {
	out := make([]string, len(f.rows))
	for i, r := range f.rows {
		out[i] = r.raw
	}
	return out
}

// matches возвращает индексы строк, относящихся к ключу (scope, profile, proto).
func (f *tsvFile) matches(scope, profile string, proto Proto) []int {
	var idx []int
	for i, r := range f.rows {
		if r.ok && r.rec.Scope == scope && r.rec.Profile == profile && r.rec.Proto == proto {
			idx = append(idx, i)
		}
	}
	return idx
}

// formatLockRow — каноническая 3-колоночная запись.
func formatLockRow(profile string, proto Proto, s Strategy) string {
	return profile + "\t" + string(proto) + "\t" + s.String()
}

// remove удаляет строки по индексам.
func (f *tsvFile) remove(idx []int) {
	if len(idx) == 0 {
		return
	}
	drop := make(map[int]bool, len(idx))
	for _, i := range idx {
		drop[i] = true
	}
	kept := f.rows[:0:0]
	for i, r := range f.rows {
		if !drop[i] {
			kept = append(kept, r)
		}
	}
	f.rows = kept
}

// upsert заменяет первую строку по индексам idx (остальные должны быть пусты)
// либо добавляет новую в конец.
func (f *tsvFile) upsert(idx []int, scope, profile string, proto Proto, s Strategy) {
	rec := Record{Scope: scope, Profile: profile, Proto: proto, Strategy: s}
	nr := row{raw: formatLockRow(profile, proto, s), ok: true, cols: 3, rec: rec}
	if len(idx) > 0 {
		f.rows[idx[0]] = nr
		return
	}
	f.rows = append(f.rows, nr)
}

// Package config разбирает и изменяет shell-style конфигурацию zapret2
// (файл config / config.default).
//
// Файл сохраняется построчно, поэтому комментарии, порядок строк и маркеры
// #Z2R_* не теряются при Set/Save. Семантика соответствует lib/config.sh:
// для переменных побеждает первое вхождение (как `sed ... | head -n1`).
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator/internal/fsutil"
)

// OptVar — имя многострочной переменной с опциями nfqws2.
const OptVar = "NFQWS2_OPT"

var (
	// ErrNotFound возвращается, когда запрошенная переменная или блок отсутствует.
	ErrNotFound = errors.New("config: not found")
	// ErrInvalid возвращается при недопустимом имени или значении.
	ErrInvalid = errors.New("config: invalid argument")
	// ErrNoPath возвращается Save, если у конфига нет пути.
	ErrNoPath = errors.New("config: no file path")
)

var (
	assignRe  = regexp.MustCompile(`^[ \t]*([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	varNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// entry — присваивание переменной (одна или несколько строк файла).
type entry struct {
	name  string
	start int // индекс первой строки
	end   int // индекс последней строки (включительно)
}

// Config — разобранный файл конфигурации.
type Config struct {
	path         string
	lines        []string
	eol          string
	finalNewline bool

	entries []entry
	byName  map[string]int // имя -> индекс первого вхождения в entries
}

// Parse разбирает текст конфигурации.
func Parse(text string) *Config {
	c := &Config{eol: "\n"}
	if strings.Contains(text, "\r\n") {
		c.eol = "\r\n"
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	if text == "" {
		c.lines = nil
	} else {
		c.finalNewline = strings.HasSuffix(text, "\n")
		text = strings.TrimSuffix(text, "\n")
		c.lines = strings.Split(text, "\n")
	}
	c.reindex()
	return c
}

// Load читает и разбирает файл конфигурации.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := Parse(string(data))
	c.path = path
	return c, nil
}

// Path возвращает путь файла, из которого загружен конфиг.
func (c *Config) Path() string { return c.path }

// SetPath задаёт путь для последующего Save.
func (c *Config) SetPath(path string) { c.path = path }

// String возвращает текущее содержимое конфигурации.
func (c *Config) String() string {
	if len(c.lines) == 0 {
		return ""
	}
	s := strings.Join(c.lines, c.eol)
	if c.finalNewline {
		s += c.eol
	}
	return s
}

// Lines возвращает копию строк файла (без перевода строки).
func (c *Config) Lines() []string {
	return append([]string(nil), c.lines...)
}

// Save атомарно записывает конфиг по пути, с которого он был загружен.
func (c *Config) Save() error {
	if c.path == "" {
		return ErrNoPath
	}
	return c.SaveTo(c.path)
}

// SaveTo атомарно записывает конфиг в path (tmp-файл + rename). Права
// существующего файла сохраняются. Межпроцессный mkdir-lock сериализует
// WebUI/CLI/bootstrap (Package C).
func (c *Config) SaveTo(path string) error {
	return fsutil.WithDirLock(path, 0, 0, func() error {
		if err := fsutil.AtomicWriteFile(path, []byte(c.String()), 0); err != nil {
			return fmt.Errorf("config: save %s: %w", path, err)
		}
		return nil
	})
}

// reindex пересобирает индекс присваиваний переменных.
func (c *Config) reindex() {
	c.entries = c.entries[:0]
	c.byName = make(map[string]int)
	for i := 0; i < len(c.lines); i++ {
		m := assignRe.FindStringSubmatch(c.lines[i])
		if m == nil {
			continue
		}
		name, rest := m[1], m[2]
		end := i
		if strings.HasPrefix(rest, `"`) && closingQuote(rest[1:]) < 0 {
			if j := c.multilineEnd(name, i); j > i {
				end = j
			}
		}
		if _, dup := c.byName[name]; !dup {
			c.byName[name] = len(c.entries)
		}
		c.entries = append(c.entries, entry{name: name, start: i, end: end})
		i = end
	}
}

// multilineEnd ищет строку, закрывающую многострочное значение, начатое в from.
// NFQWS2_OPT закрывается строкой, состоящей ровно из `"` (как `^"$` в shell-коде).
func (c *Config) multilineEnd(name string, from int) int {
	for j := from + 1; j < len(c.lines); j++ {
		if name == OptVar {
			if strings.TrimRight(c.lines[j], " \t") == `"` {
				return j
			}
			continue
		}
		if closingQuote(c.lines[j]) >= 0 {
			return j
		}
	}
	return -1
}

// closingQuote возвращает индекс первой незаэкранированной кавычки `"` в s или -1.
func closingQuote(s string) int {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

func (c *Config) find(name string) (entry, bool) {
	idx, ok := c.byName[name]
	if !ok {
		return entry{}, false
	}
	return c.entries[idx], true
}

// raw возвращает сырой текст значения (всё после `=`) вместе с кавычками;
// многострочное значение склеивается через "\n".
func (c *Config) raw(e entry) string {
	first := assignRe.FindStringSubmatch(c.lines[e.start])[2]
	if e.end == e.start {
		return first
	}
	parts := make([]string, 0, e.end-e.start+1)
	parts = append(parts, first)
	parts = append(parts, c.lines[e.start+1:e.end+1]...)
	return strings.Join(parts, "\n")
}

// Has сообщает, задана ли переменная (включая пустое значение).
func (c *Config) Has(name string) bool {
	_, ok := c.find(name)
	return ok
}

// GetRaw возвращает значение переменной в точности как оно записано в файле
// (с кавычками и хвостовым комментарием), аналог config_get_var.
func (c *Config) GetRaw(name string) (string, bool) {
	e, ok := c.find(name)
	if !ok {
		return "", false
	}
	return c.raw(e), true
}

// Get возвращает значение переменной без обрамляющих кавычек и без хвостового
// комментария неквотированного значения. Для отсутствующей переменной — "", false.
func (c *Config) Get(name string) (string, bool) {
	raw, ok := c.GetRaw(name)
	if !ok {
		return "", false
	}
	return unquote(raw), true
}

// GetDefault возвращает значение переменной или def, если её нет.
func (c *Config) GetDefault(name, def string) string {
	if v, ok := c.Get(name); ok {
		return v
	}
	return def
}

// GetInt возвращает значение переменной как целое число.
func (c *Config) GetInt(name string) (int, bool) {
	v, ok := c.Get(name)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, false
	}
	return n, true
}

// Vars возвращает карту всех переменных (первое вхождение каждой, значения
// без кавычек). NFQWS2_OPT в карту не входит — см. NFQWS2Opt.
func (c *Config) Vars() map[string]string {
	out := make(map[string]string, len(c.byName))
	for name, idx := range c.byName {
		if name == OptVar {
			continue
		}
		out[name] = unquote(c.raw(c.entries[idx]))
	}
	return out
}

// Set записывает переменную: `NAME=value`. value вставляется в файл как есть
// (shell-текст, кавычки — на стороне вызывающего), как в config_set_var.
// Если переменная существует, заменяется первое вхождение, иначе строка
// добавляется в конец файла.
func (c *Config) Set(name, value string) error {
	if !varNameRe.MatchString(name) {
		return fmt.Errorf("%w: variable name %q", ErrInvalid, name)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%w: value of %s must be single-line", ErrInvalid, name)
	}
	c.replaceOrAppend(name, []string{name + "=" + value})
	return nil
}

// SetBeforeNFQWS2Opt keeps shell variables used inside the double-quoted
// NFQWS2_OPT assignment defined before that assignment is evaluated by zapret.
func (c *Config) SetBeforeNFQWS2Opt(name, value string) error {
	if !varNameRe.MatchString(name) {
		return fmt.Errorf("%w: variable name %q", ErrInvalid, name)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%w: value of %s must be single-line", ErrInvalid, name)
	}
	opt, ok := c.find(OptVar)
	if !ok {
		return c.Set(name, value)
	}
	if existing, ok := c.find(name); ok {
		if existing.start < opt.start {
			return c.Set(name, value)
		}
		c.spliceLines(existing.start, existing.end+1, nil)
		opt, _ = c.find(OptVar)
	}
	c.spliceLines(opt.start, opt.start, []string{name + "=" + value})
	return nil
}

// SetQuoted записывает переменную, при необходимости обрамляя значение
// двойными кавычками (пустые значения и значения с пробелами/спецсимволами).
func (c *Config) SetQuoted(name, value string) error {
	if strings.ContainsAny(value, "\"\\`\r\n") {
		return fmt.Errorf("%w: value of %s contains unsupported characters", ErrInvalid, name)
	}
	if value != "" && !strings.ContainsAny(value, " \t#$;&|<>()'*?") {
		return c.Set(name, value)
	}
	return c.Set(name, `"`+value+`"`)
}

// NFQWS2Opt возвращает «сырой» текст между кавычками NFQWS2_OPT="...",
// включая переводы строк (обычно начинается и заканчивается "\n").
func (c *Config) NFQWS2Opt() (string, bool) {
	e, ok := c.find(OptVar)
	if !ok {
		return "", false
	}
	raw := c.raw(e)
	if !strings.HasPrefix(raw, `"`) {
		return unquote(raw), true
	}
	body := raw[1:]
	if i := strings.LastIndex(body, `"`); i >= 0 {
		body = body[:i]
	}
	return body, true
}

// SetNFQWS2Opt заменяет содержимое NFQWS2_OPT. Закрывающая кавычка всегда
// оказывается на отдельной строке, поэтому opt дополняется завершающим "\n".
func (c *Config) SetNFQWS2Opt(opt string) error {
	opt = strings.ReplaceAll(opt, "\r\n", "\n")
	for _, ln := range strings.Split(opt, "\n") {
		if strings.TrimRight(ln, " \t") == `"` {
			return fmt.Errorf("%w: NFQWS2_OPT contains a line with a lone quote", ErrInvalid)
		}
	}
	if !strings.HasSuffix(opt, "\n") {
		opt += "\n"
	}
	text := OptVar + `="` + opt + `"`
	c.replaceOrAppend(OptVar, strings.Split(text, "\n"))
	return nil
}

func (c *Config) replaceOrAppend(name string, repl []string) {
	if e, ok := c.find(name); ok {
		c.spliceLines(e.start, e.end+1, repl)
		return
	}
	c.lines = append(c.lines, repl...)
	if len(c.lines) == len(repl) {
		c.finalNewline = true
	}
	c.reindex()
}

// spliceLines заменяет c.lines[from:to] на repl и пересобирает индекс.
func (c *Config) spliceLines(from, to int, repl []string) {
	out := make([]string, 0, len(c.lines)-(to-from)+len(repl))
	out = append(out, c.lines[:from]...)
	out = append(out, repl...)
	out = append(out, c.lines[to:]...)
	c.lines = out
	c.reindex()
}

var trailingCommentRe = regexp.MustCompile(`[ \t]+#.*$`)

// unquote снимает обрамляющие кавычки; для неквотированного значения
// отбрасывает хвостовой shell-комментарий (`VALUE  # comment`).
func unquote(raw string) string {
	switch {
	case strings.HasPrefix(raw, `"`):
		body := raw[1:]
		if !strings.Contains(body, "\n") {
			if j := closingQuote(body); j >= 0 {
				return body[:j]
			}
			return body
		}
		// Многострочное значение: всё до последней кавычки.
		if i := strings.LastIndex(body, `"`); i >= 0 {
			return body[:i]
		}
		return body
	case strings.HasPrefix(raw, `'`):
		body := raw[1:]
		if i := strings.IndexByte(body, '\''); i >= 0 {
			return body[:i]
		}
		return body
	}
	return strings.TrimSpace(trailingCommentRe.ReplaceAllString(raw, ""))
}

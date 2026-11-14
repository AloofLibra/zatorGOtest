package config

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/AloofLibra/zator/internal/fsutil"
)

var (
	shellVarBrace = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	shellVarPlain = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)
)

// DefaultQNum — номер NFQUEUE, если в конфиге нет QNUM.
const DefaultQNum = "200"

// BuildNfqws2Args строит список аргументов nfqws2 из NFQWS2_OPT:
// по одному аргументу на непустую строку (как @file у nfqws2), с подстановкой
// ${VAR}/$VAR из переменных конфига. Добавляет --qnum, если его нет.
func (c *Config) BuildNfqws2Args() []string {
	if c == nil {
		return nil
	}
	raw, ok := c.NFQWS2Opt()
	if !ok {
		return nil
	}
	vars := c.Vars()
	expand := func(s string) string {
		s = shellVarBrace.ReplaceAllStringFunc(s, func(m string) string {
			name := shellVarBrace.FindStringSubmatch(m)[1]
			if v, ok := vars[name]; ok {
				return v
			}
			return m
		})
		s = shellVarPlain.ReplaceAllStringFunc(s, func(m string) string {
			name := shellVarPlain.FindStringSubmatch(m)[1]
			if v, ok := vars[name]; ok {
				return v
			}
			return m
		})
		return s
	}

	var out []string
	hasQnum := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = expand(line)
		// Как shell-разбор NFQWS2_OPT: «--skip --filter-udp=53» и несколько
		// --lua-desync на одной строке — отдельные argv для nfqws2 @file.
		for _, tok := range strings.FieldsFunc(line, unicode.IsSpace) {
			if tok == "" {
				continue
			}
			if strings.HasPrefix(tok, "--qnum=") || tok == "--qnum" {
				hasQnum = true
			}
			out = append(out, tok)
		}
	}
	if !hasQnum {
		q := c.GetDefault("QNUM", DefaultQNum)
		if q == "" {
			q = DefaultQNum
		}
		out = append([]string{"--qnum=" + q}, out...)
	}
	return out
}

// WriteNfqws2Args записывает файл аргументов для `nfqws2 @file`.
func (c *Config) WriteNfqws2Args(path string) error {
	args := c.BuildNfqws2Args()
	if len(args) == 0 {
		return fmt.Errorf("config: NFQWS2_OPT пуст, нечего писать в %s", path)
	}
	var b strings.Builder
	for _, a := range args {
		b.WriteString(a)
		b.WriteByte('\n')
	}
	return fsutil.AtomicWriteFile(path, []byte(b.String()), 0o644)
}

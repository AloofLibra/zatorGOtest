package config

import (
	"fmt"
	"os"
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
const DefaultQNum = "300"

// argsShellMeta — символы, недопустимые в argv nfqws2.args (ломают shell-quoting config и передачу аргументов).
const argsShellMeta = "\n\r\"'`$;\\|&<>()"

// ValidateNfqws2Args проверяет argv перед записью и запуском через nfqws2.args.
func ValidateNfqws2Args(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config: NFQWS2_OPT пуст")
	}
	meaningful := 0
	for i, a := range args {
		if a == "" {
			return fmt.Errorf("config: пустой аргумент #%d", i+1)
		}
		if strings.ContainsAny(a, argsShellMeta) {
			return fmt.Errorf("config: недопустимые символы в аргументе %q", a)
		}
		if !strings.HasPrefix(a, "-") {
			return fmt.Errorf("config: аргумент должен быть флагом: %q", a)
		}
		if i > 0 && a == "--new" && args[i-1] == "--new" {
			return fmt.Errorf("config: дублирующийся --new на позиции #%d (создаёт пустой профиль)", i+1)
		}
		if !strings.HasPrefix(a, "--qnum=") && a != "--qnum" {
			meaningful++
		}
	}
	if meaningful == 0 {
		return fmt.Errorf("config: NFQWS2_OPT пуст (нет флагов кроме --qnum)")
	}
	return nil
}

// BuildNfqws2Args строит список аргументов nfqws2 из NFQWS2_OPT:
// по одному аргументу на токен (nfqws2.args далее передаётся в nfqws2 через argv
// командой zator exec-nfqws2, минуя ограничение nfqws2 @file в 16 КБ),
// с подстановкой ${VAR}/$VAR из переменных конфига. Добавляет --qnum, если его нет.
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
		// --lua-desync на одной строке — отдельные argv для nfqws2.args.
		for _, tok := range strings.FieldsFunc(line, unicode.IsSpace) {
			if tok == "" {
				continue
			}
			if tok == "--new" && len(out) > 0 && out[len(out)-1] == "--new" {
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

// BuildNfqws2RuntimeArgs adds the standard NFQWS2_OPT_BASE arguments supplied
// by zapret2 init.d when it launches nfqws2 directly.
func (c *Config) BuildNfqws2RuntimeArgs(zapretLuaDir, userName, qnum string) []string {
	if c == nil {
		return nil
	}
	base := c.BuildNfqws2Args()
	if len(base) == 0 {
		return nil
	}
	if userName == "" {
		userName = c.GetDefault("WS_USER", "daemon")
	}
	if qnum == "" {
		qnum = "--qnum=" + c.GetDefault("QNUM", DefaultQNum)
		if qnum == "--qnum=" {
			qnum = "--qnum=" + DefaultQNum
		}
	}
	mark := c.GetDefault("DESYNC_MARK", "0x40000000")
	luaDir := strings.TrimRight(strings.ReplaceAll(zapretLuaDir, "\\", "/"), "/")
	out := []string{
		"--user=" + userName,
		"--fwmark=" + mark,
		"--lua-init=@" + luaDir + "/zapret-lib.lua",
		"--lua-init=@" + luaDir + "/zapret-antidpi.lua",
		"--lua-init=@" + luaDir + "/zapret-auto.lua",
		qnum,
	}
	for _, arg := range base {
		switch {
		case arg == "--qnum" || strings.HasPrefix(arg, "--qnum="):
			continue
		case arg == "--user" || strings.HasPrefix(arg, "--user="):
			continue
		case arg == "--fwmark" || strings.HasPrefix(arg, "--fwmark="):
			continue
		case strings.HasPrefix(arg, "--lua-init=@"):
			name := arg[strings.LastIndex(arg, "/")+1:]
			if name == "zapret-lib.lua" || name == "zapret-antidpi.lua" || name == "zapret-auto.lua" {
				continue
			}
		}
		if arg == "--new" && len(out) > 0 && out[len(out)-1] == "--new" {
			continue
		}
		out = append(out, arg)
	}
	return out
}

// CollapseDuplicateNew удаляет лишние подряд идущие разделители `--new` в NFQWS2_OPT
// (пустые профили без активных флагов между ними), сохраняя комментарии и маркеры #Z2R_*.
func (c *Config) CollapseDuplicateNew() (bool, error) {
	if c == nil {
		return false, nil
	}
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return false, nil
	}
	lines := strings.Split(opt, "\n")
	out := make([]string, 0, len(lines))
	seenNew := false
	changed := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			out = append(out, line)
			continue
		}
		toks := strings.FieldsFunc(trim, unicode.IsSpace)
		kept := make([]string, 0, len(toks))
		lineChanged := false
		for _, tok := range toks {
			if tok == "--new" {
				if seenNew {
					lineChanged = true
					changed = true
					continue
				}
				seenNew = true
			} else {
				seenNew = false
			}
			kept = append(kept, tok)
		}
		if !lineChanged {
			out = append(out, line)
		} else if len(kept) > 0 {
			out = append(out, strings.Join(kept, " "))
		}
	}
	if !changed {
		return false, nil
	}
	return true, c.SetNFQWS2Opt(strings.Join(out, "\n"))
}

// ReadNfqws2ArgsFile читает аргументы из файла nfqws2.args без схлопывания токенов,
// чтобы ValidateNfqws2Args мог обнаружить дублирующийся --new и другие ошибки.
func ReadNfqws2ArgsFile(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		for _, tok := range strings.FieldsFunc(ln, unicode.IsSpace) {
			if tok != "" {
				out = append(out, tok)
			}
		}
	}
	return out, nil
}

// WriteNfqws2Args записывает файл аргументов nfqws2.args (по токену на строку;
// передаётся через argv командой zator exec-nfqws2 во избежание лимита @file).
func (c *Config) WriteNfqws2Args(path string) error {
	return c.writeNfqws2Args(path, c.BuildNfqws2Args())
}

// WriteNfqws2RuntimeArgs writes the complete argument file for the Go-managed
// zapret2 service, including its normal init.d Lua and mark defaults.
func (c *Config) WriteNfqws2RuntimeArgs(path, zapretLuaDir, userName string) error {
	return c.writeNfqws2Args(path, c.BuildNfqws2RuntimeArgs(zapretLuaDir, userName, ""))
}

func (c *Config) writeNfqws2Args(path string, args []string) error {
	if err := ValidateNfqws2Args(args); err != nil {
		return fmt.Errorf("%w (нечего писать в %s)", err, path)
	}
	var b strings.Builder
	for _, a := range args {
		b.WriteString(a)
		b.WriteByte('\n')
	}
	return fsutil.AtomicWriteFile(path, []byte(b.String()), 0o644)
}

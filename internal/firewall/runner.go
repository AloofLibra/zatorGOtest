package firewall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// Command — одна внешняя команда (iptables, ip6tables, nft, ipset ...).
// Stdin, если задан, подаётся на стандартный ввод (nft -f -, ipset restore).
type Command struct {
	Name  string
	Args  []string
	Stdin string
}

// String возвращает команду в виде shell-строки (stdin не включается).
func (c Command) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	parts = append(parts, shellQuote(c.Name))
	for _, a := range c.Args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

var shellSafeRe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./!-]+$`)

// shellQuote экранирует слово для POSIX sh.
func shellQuote(s string) string {
	if s != "" && shellSafeRe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Runner выполняет внешние команды. Реализации: ExecRunner (реальный запуск) и
// RecordingRunner (запись команд для тестов).
type Runner interface {
	// Run выполняет команду и возвращает объединённый stdout+stderr.
	// Ненулевой код возврата — ошибка (*CommandError).
	Run(ctx context.Context, cmd Command) ([]byte, error)
	// LookPath проверяет наличие исполняемого файла.
	LookPath(name string) (string, error)
}

// CommandError — ошибка выполнения команды вместе с её выводом.
type CommandError struct {
	Cmd    Command
	Output string
	Err    error
}

func (e *CommandError) Error() string {
	msg := fmt.Sprintf("firewall: %s: %v", e.Cmd.String(), e.Err)
	if out := strings.TrimSpace(e.Output); out != "" {
		msg += ": " + out
	}
	return msg
}

// Unwrap возвращает исходную ошибку.
func (e *CommandError) Unwrap() error { return e.Err }

// ExecRunner запускает команды через os/exec.
type ExecRunner struct {
	// Env — дополнительные переменные окружения (nil — окружение процесса).
	Env []string
}

// Run реализует Runner.
func (r ExecRunner) Run(ctx context.Context, cmd Command) ([]byte, error) {
	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	if r.Env != nil {
		c.Env = r.Env
	}
	if cmd.Stdin != "" {
		c.Stdin = strings.NewReader(cmd.Stdin)
	}
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	if err := c.Run(); err != nil {
		return buf.Bytes(), &CommandError{Cmd: cmd, Output: buf.String(), Err: err}
	}
	return buf.Bytes(), nil
}

// LookPath реализует Runner.
func (ExecRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

// RecordingRunner записывает все команды и позволяет подменять результаты.
// Нулевое значение готово к использованию: все бинарники «найдены», все
// команды успешны с пустым выводом.
type RecordingRunner struct {
	mu sync.Mutex

	// Handler, если задан, вычисляет результат команды (вывод и ошибку).
	Handler func(cmd Command) ([]byte, error)
	// Binaries — набор «установленных» бинарников для LookPath.
	// nil означает, что доступны все.
	Binaries map[string]bool

	calls []Command
}

// NewRecordingRunner создаёт RecordingRunner, в котором «установлены» только
// перечисленные бинарники.
func NewRecordingRunner(binaries ...string) *RecordingRunner {
	m := make(map[string]bool, len(binaries))
	for _, b := range binaries {
		m[b] = true
	}
	return &RecordingRunner{Binaries: m}
}

// Run реализует Runner.
func (r *RecordingRunner) Run(_ context.Context, cmd Command) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, cmd)
	h := r.Handler
	r.mu.Unlock()
	if h == nil {
		return nil, nil
	}
	out, err := h(cmd)
	if err != nil {
		return out, &CommandError{Cmd: cmd, Output: string(out), Err: err}
	}
	return out, nil
}

// LookPath реализует Runner.
func (r *RecordingRunner) LookPath(name string) (string, error) {
	if r.Binaries == nil || r.Binaries[name] {
		return "/usr/sbin/" + name, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// Calls возвращает копию записанных команд.
func (r *RecordingRunner) Calls() []Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Command(nil), r.calls...)
}

// Lines возвращает записанные команды в виде строк.
func (r *RecordingRunner) Lines() []string {
	calls := r.Calls()
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.String()
	}
	return out
}

// Reset очищает журнал вызовов.
func (r *RecordingRunner) Reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}

// errRule — ошибка «правила нет», удобна в обработчиках тестов.
var errRule = errors.New("exit status 1")

// ErrExit1 возвращает типовую ошибку «код возврата 1» для Handler-ов тестов
// (например, iptables -C для отсутствующего правила).
func ErrExit1() error { return errRule }

// --- вспомогательные функции для реализации ---

func run(ctx context.Context, r Runner, name string, args ...string) ([]byte, error) {
	return r.Run(ctx, Command{Name: name, Args: args})
}

func runStdin(ctx context.Context, r Runner, stdin, name string, args ...string) ([]byte, error) {
	return r.Run(ctx, Command{Name: name, Args: args, Stdin: stdin})
}

// splitArgs разбивает строку вывода `iptables -S` на аргументы с учётом
// двойных кавычек (iptables экранирует значения с пробелами).
func splitArgs(s string) []string {
	var (
		out   []string
		cur   strings.Builder
		inQ   bool
		esc   bool
		has   bool
		runes = []rune(s)
	)
	flush := func() {
		if has {
			out = append(out, cur.String())
			cur.Reset()
			has = false
		}
	}
	for _, c := range runes {
		switch {
		case esc:
			cur.WriteRune(c)
			esc, has = false, true
		case c == '\\' && inQ:
			esc = true
		case c == '"':
			inQ, has = !inQ, true
		case (c == ' ' || c == '\t') && !inQ:
			flush()
		default:
			cur.WriteRune(c)
			has = true
		}
	}
	flush()
	return out
}

// CommandsScript формирует POSIX-shell представление списка команд
// (для custom.d / ручного применения). Stdin оформляется heredoc-ом.
func CommandsScript(cmds []Command) string {
	var b strings.Builder
	for _, c := range cmds {
		b.WriteString(c.String())
		if c.Stdin != "" {
			b.WriteString(" <<'ZATOR_EOF'\n")
			b.WriteString(strings.TrimRight(c.Stdin, "\n"))
			b.WriteString("\nZATOR_EOF\n")
			continue
		}
		b.WriteByte('\n')
	}
	return b.String()
}

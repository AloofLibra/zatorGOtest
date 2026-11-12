package sweep

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator/internal/fsutil"
)

// Имена файлов состояния в каталоге прогона.
const (
	fileStatus      = "status"
	fileMediaStatus = "media-status"
	fileWorkers     = "workers.tsv"
	fileCoverage    = "coverage.tsv"
	fileApplied     = "applied.tsv"
	filePrev        = "prev.tsv"
	filePacing      = "pacing.tsv"
	fileCancel      = "cancel"

	// LastReportFile — сохранённый отчёт последнего прогона в ArchiveDir.
	LastReportFile = "last-report.tsv"
)

func progressFile(w string) string    { return "progress." + w + ".tsv" }
func bestFile(w string) string        { return "best." + w }
func doneFile(w string) string        { return "done." + w }
func appliedDoneFile(w string) string { return "applied.done." + w }
func currentFile(w string) string     { return "current." + w }

// appendLine дописывает одну строку одним вызовом Write: читатели видят
// строку целиком или не видят вовсе.
func appendLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(line + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// kv — упорядоченный набор key=value.
type kv [][2]string

func (p kv) bytes() []byte {
	var b strings.Builder
	for _, e := range p {
		b.WriteString(e[0])
		b.WriteByte('=')
		b.WriteString(strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' {
				return ' '
			}
			return r
		}, e[1]))
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func writeKV(path string, p kv) error { return fsutil.AtomicWriteFile(path, p.bytes(), 0o644) }

// readKV читает файл key=value; нет файла — пустая карта.
func readKV(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if i := strings.IndexByte(sc.Text(), '='); i > 0 {
			out[sc.Text()[:i]] = sc.Text()[i+1:]
		}
	}
	return out
}

// readTSV читает строки TSV (пустые пропускаются); нет файла — nil.
func readTSV(path string) [][]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var rows [][]string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readFirstLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := string(b)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func removeIfExists(path string) { _ = os.Remove(path) }

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// isPositiveInt — строка из цифр, значение >= 1.
func isPositiveInt(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && strconv.Itoa(n) == s
}

func intList(s string) []int {
	var out []int
	for _, f := range strings.Fields(s) {
		if n, err := strconv.Atoi(f); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func joinInts(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, " ")
}

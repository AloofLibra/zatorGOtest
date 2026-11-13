package deploy

import (
	"bufio"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/paths"
)

// Каталог и файлы состояния развёртывания (внутри StateDir/deploy).
const (
	// DirName — каталог состояния deploy внутри StateDir.
	DirName = "deploy"
	// VersionFileName — установленная версия (version.env).
	VersionFileName = "version.env"
	// LatestCacheName — кэш последнего latest.json.
	LatestCacheName = "latest.json"
	// TrackingLatest — значение TRACKING по умолчанию.
	TrackingLatest = "latest"
)

// Dir возвращает каталог состояния deploy.
func Dir(p paths.Paths) string { return filepath.Join(p.StateDir, DirName) }

// VersionPath возвращает путь к version.env.
func VersionPath(p paths.Paths) string { return filepath.Join(Dir(p), VersionFileName) }

// Version — содержимое version.env (формат KEY="value", как у shell-версии).
type Version struct {
	ZatorVersion string
	ZatorDate    string
	ZatorCommit  string
	ZatorSHA     string
	WebUIVersion string
	WebUIDate    string
	WebUISHA     string
	// Tracking — "latest" или закреплённый тег релиза.
	Tracking string
}

// IsZero сообщает, что о версии ничего не известно.
func (v Version) IsZero() bool { return v == Version{} }

// ParseVersionEnv разбирает текст version.env. Неизвестные ключи и
// комментарии игнорируются.
func ParseVersionEnv(text string) Version {
	var v Version
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimRight(sc.Text(), "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
		switch strings.TrimSpace(k) {
		case "ZATOR_VERSION":
			v.ZatorVersion = val
		case "ZATOR_DATE":
			v.ZatorDate = val
		case "ZATOR_COMMIT":
			v.ZatorCommit = val
		case "ZATOR_SHA":
			v.ZatorSHA = val
		case "WEBUI_VERSION":
			v.WebUIVersion = val
		case "WEBUI_DATE":
			v.WebUIDate = val
		case "WEBUI_SHA":
			v.WebUISHA = val
		case "TRACKING":
			v.Tracking = val
		}
	}
	return v
}

// String форматирует версию как version.env.
func (v Version) String() string {
	var b strings.Builder
	clean := strings.NewReplacer(`"`, "", "\r", "", "\n", "", `\`, "")
	w := func(k, val string) { b.WriteString(k + `="` + clean.Replace(val) + "\"\n") }
	or := func(s, def string) string {
		if s == "" {
			return def
		}
		return s
	}
	w("ZATOR_VERSION", or(v.ZatorVersion, "unknown"))
	w("ZATOR_DATE", v.ZatorDate)
	w("ZATOR_COMMIT", v.ZatorCommit)
	w("ZATOR_SHA", v.ZatorSHA)
	w("WEBUI_VERSION", or(v.WebUIVersion, "unknown"))
	w("WEBUI_DATE", v.WebUIDate)
	w("WEBUI_SHA", v.WebUISHA)
	w("TRACKING", or(v.Tracking, TrackingLatest))
	return b.String()
}

// ReadVersion читает установленную версию; отсутствующий файл даёт нулевую Version.
func ReadVersion(p paths.Paths) (Version, error) {
	data, err := readFileOpt(VersionPath(p))
	if err != nil {
		return Version{}, err
	}
	return ParseVersionEnv(string(data)), nil
}

// WriteVersion записывает version.env атомарно.
func WriteVersion(p paths.Paths, v Version) error {
	return fsutil.AtomicWriteFile(VersionPath(p), []byte(v.String()), 0o644)
}

// LatestCachePath возвращает путь к кэшу latest.json.
func LatestCachePath(p paths.Paths) string {
	return filepath.Join(Dir(p), LatestCacheName)
}

// ReadCachedLatest читает кэш последнего CheckLatest; при отсутствии — (nil, nil).
func ReadCachedLatest(p paths.Paths) (*Latest, error) {
	data, err := readFileOpt(LatestCachePath(p))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	return ParseLatest(data)
}

// CompareVersions сравнивает версии: -1 если a < b, 0 если равны, 1 если a > b.
//
// Правила (семантическое версионирование с послаблениями): ведущая "v"
// отбрасывается; метаданные сборки после "+" игнорируются; ядро (до первого
// "-") сравнивается по точечным сегментам — числовые числом, иначе строкой,
// отсутствующие сегменты равны нулю; версия с пререлизом ("-rc1") меньше
// такой же без него; идентификаторы пререлиза сравниваются по правилам semver.
func CompareVersions(a, b string) int {
	ac, ap := splitVersion(a)
	bc, bp := splitVersion(b)
	if c := compareCore(ac, bc); c != 0 {
		return c
	}
	switch {
	case ap == "" && bp == "":
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	return comparePre(strings.Split(ap, "."), strings.Split(bp, "."))
}

func splitVersion(s string) (core, pre string) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func numeric(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func compareCore(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		x, y := "0", "0"
		if i < len(as) && as[i] != "" {
			x = as[i]
		}
		if i < len(bs) && bs[i] != "" {
			y = bs[i]
		}
		xn, xok := numeric(x)
		yn, yok := numeric(y)
		switch {
		case xok && yok:
			if c := cmpInt(xn, yn); c != 0 {
				return c
			}
		case xok:
			return -1 // числовой сегмент меньше строкового
		case yok:
			return 1
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	return 0
}

func comparePre(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		xn, xok := numeric(a[i])
		yn, yok := numeric(b[i])
		switch {
		case xok && yok:
			if c := cmpInt(xn, yn); c != 0 {
				return c
			}
		case xok:
			return -1
		case yok:
			return 1
		default:
			if c := strings.Compare(a[i], b[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(a), len(b))
}

// IsNewer сообщает, что candidate новее current.
func IsNewer(current, candidate string) bool { return CompareVersions(candidate, current) > 0 }

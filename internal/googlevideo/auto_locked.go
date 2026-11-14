package googlevideo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// AutoLock — выученная стратегия для hostkey (обычно sn-<id>) в профиле askey.
type AutoLock struct {
	Askey    string `json:"askey"`
	Scope    string `json:"scope"`
	Host     string `json:"host"`
	Strategy int    `json:"strategy"`
}

// ParseAutoLockedTSV читает auto_locked.tsv:
// askey\thost\tstrat  или  askey\tscope\thost\tstrat
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
		var askey, scope, host, stratRaw string
		switch len(fields) {
		case 3:
			askey, scope, host, stratRaw = fields[0], "default", fields[1], fields[2]
		case 4:
			askey, scope, host, stratRaw = fields[0], fields[1], fields[2], fields[3]
		default:
			continue
		}
		askey = strings.TrimSpace(askey)
		scope = strings.TrimSpace(scope)
		host = strings.TrimSpace(strings.ToLower(host))
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
		out = append(out, AutoLock{Askey: askey, Scope: scope, Host: host, Strategy: n})
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

package deploy

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AloofLibra/zator/internal/fsutil"
	"github.com/AloofLibra/zator/internal/paths"
)

const managedHashesName = "managed-resources.tsv"

func managedHashesPath(p paths.Paths) string { return filepath.Join(Dir(p), managedHashesName) }

func readManagedHashes(p paths.Paths) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(managedHashesPath(p))
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "\t")
		if ok && key != "" && sha256Re.MatchString(value) {
			out[key] = strings.ToLower(value)
		}
	}
	return out, sc.Err()
}

func writeManagedHashes(p paths.Paths, values map[string]string) error {
	if err := os.MkdirAll(Dir(p), 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		if !sha256Re.MatchString(values[k]) {
			return fmt.Errorf("deploy: invalid managed hash for %q", k)
		}
		fmt.Fprintf(&b, "%s\t%s\n", k, strings.ToLower(values[k]))
	}
	return fsutil.AtomicWriteFile(managedHashesPath(p), []byte(b.String()), 0o600)
}

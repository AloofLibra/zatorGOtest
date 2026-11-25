package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SlotBlobFile возвращает имя файла (например `tls_clienthello_5.bin`) из
// декларации `--blob=<slot>:@/opt/(zapret2|zator)/files/fake/<file>` (включая `maxru` и `z2r_prof_N`).
func (c *Config) SlotBlobFile(slot string) (string, bool) {
	slot = strings.TrimSpace(slot)
	if slot == "" {
		return "", false
	}
	re := regexp.MustCompile(`--blob=` + regexp.QuoteMeta(slot) + `:@/opt/(?:zapret2|zator)/files/fake/([^\s/]+)`)
	for _, ln := range c.lines {
		if m := re.FindStringSubmatch(ln); m != nil {
			return m[1], true
		}
	}
	return "", false
}

// SetSlotBlobFile обновляет декларацию `--blob=<slot>:@/opt/(zapret2|zator)/files/fake/<file>`
// на `/opt/zator/files/fake/<file>`.
func (c *Config) SetSlotBlobFile(slot, file string) error {
	slot = strings.TrimSpace(slot)
	file = strings.TrimSpace(file)
	if slot == "" || file == "" {
		return fmt.Errorf("%w: empty slot or file", ErrInvalid)
	}
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, OptVar)
	}
	re := regexp.MustCompile(`(?m)^(\s*--blob=` + regexp.QuoteMeta(slot) + `:@)/opt/(?:zapret2|zator)/files/fake/[^\s/]+`)
	if !re.MatchString(opt) {
		return fmt.Errorf("%w: слот --blob=%s не найден в конфиге", ErrNotFound, slot)
	}
	next := re.ReplaceAllString(opt, "${1}/opt/zator/files/fake/"+file)
	return c.SetNFQWS2Opt(next)
}

// SwitchTLSBlob переключает глобальный режим maxru <-> fake_default_tls в стратегиях
// (пропуская strategy=26) и обновляет путь --blob=maxru:@... при выборе файла.
func (c *Config) SwitchTLSBlob(value string) error {
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, OptVar)
	}
	lines := strings.Split(opt, "\n")
	for i, ln := range lines {
		if !strings.Contains(ln, "--lua-desync=") || strings.Contains(ln, "strategy=26") {
			continue
		}
		if value == "fake_default_tls" {
			lines[i] = strings.ReplaceAll(ln, "blob=maxru", "blob=fake_default_tls")
		} else {
			lines[i] = strings.ReplaceAll(ln, "blob=fake_default_tls", "blob=maxru")
		}
	}
	out := strings.Join(lines, "\n")
	if err := c.SetNFQWS2Opt(out); err != nil {
		return err
	}
	if value != "fake_default_tls" {
		return c.SetSlotBlobFile("maxru", value)
	}
	return nil
}

// ApplyBlobFiles восстанавливает выбор блобов из `blob_files.tsv` (глобальный `maxru` и слоты `z2r_prof_N`).
// Файлы проверяются на наличие в fakeDir. Возвращает true, если конфиг изменился.
func (c *Config) ApplyBlobFiles(files map[string]string, fakeDir string) (bool, error) {
	if len(files) == 0 {
		return false, nil
	}
	before := c.String()
	if v, ok := files["maxru"]; ok && v != "" {
		if v == "fake_default_tls" {
			_ = c.SwitchTLSBlob(v)
		} else if fakeDir == "" || fileExists(filepath.Join(fakeDir, v)) {
			_ = c.SwitchTLSBlob(v)
		}
	}
	for slot, file := range files {
		if slot == "maxru" || file == "" {
			continue
		}
		if fakeDir != "" && !fileExists(filepath.Join(fakeDir, file)) {
			continue
		}
		_ = c.SetSlotBlobFile(slot, file)
	}
	return c.String() != before, nil
}

// EnsureSlotBlob гарантирует наличие строки `--blob=<slot>:@/opt/zator/files/fake/<defaultFile>`
// в NFQWS2_OPT (вставляя её перед `#Z2R_CUSTOM_BLOBS_BEGIN` или `--template=z2r_tcp_tls_common`, если слот отсутствует).
func (c *Config) EnsureSlotBlob(slot, defaultFile string) (bool, error) {
	slot = strings.TrimSpace(slot)
	defaultFile = strings.TrimSpace(defaultFile)
	if slot == "" || defaultFile == "" {
		return false, fmt.Errorf("%w: empty slot or file", ErrInvalid)
	}
	if _, ok := c.SlotBlobFile(slot); ok {
		return false, nil
	}
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrNotFound, OptVar)
	}
	decl := fmt.Sprintf("--blob=%s:@/opt/zator/files/fake/%s", slot, defaultFile)
	lines := strings.Split(opt, "\n")
	insertAt := len(lines)
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == CustomBlobsBegin || tplRegionStart.MatchString(t) {
			insertAt = i
			break
		}
	}
	next := make([]string, 0, len(lines)+1)
	next = append(next, lines[:insertAt]...)
	next = append(next, decl)
	next = append(next, lines[insertAt:]...)
	if err := c.SetNFQWS2Opt(strings.Join(next, "\n")); err != nil {
		return false, err
	}
	return true, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

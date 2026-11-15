package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// EnsureWssizeRampProfile adds the standard Googlevideo WSSIZE profile if an
// older zapret config has the migration variables but no matching NFQWS rule.
// The existing ENABLE/SKIP setting still controls whether the profile runs.
func (c *Config) EnsureWssizeRampProfile(zatorRoot string) (bool, error) {
	if c == nil {
		return false, nil
	}
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrNotFound, OptVar)
	}
	if strings.Contains(opt, "--lua-desync=wssize_ramp") {
		return false, nil
	}
	exclude := filepath.ToSlash(filepath.Join(zatorRoot, "lists", "netrogat_substrings.txt"))
	block := []string{
		"# Z2R WSSIZE Googlevideo late-packet profile",
		"${Z2R_WSSIZE_RAMP_SKIP} --filter-l3=ipv4 --filter-tcp=443 --filter-mark=${Z2R_WSSIZE_MARK}/${Z2R_WSSIZE_MARK}",
		"--payload=tls_client_hello,http_req,http_reply,unknown,tls_server_hello",
		"--out-range=-s34228",
		"--out-range=a",
		"--in-range=a",
		"--payload=all,empty",
		"--lua-desync=wssize_ramp${Z2R_WSSIZE_RAMP_ARGS}",
		"--in-range=-s32768 --lua-desync=rst_guard_locked:key=2:exclude_substrings=" + exclude + ":payload=tls_client_hello",
		"--import=z2r_tcp_tls_common",
	}
	lines := strings.Split(opt, "\n")
	insertAt := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "#Z2R_AUTO_STANDARD_BEGIN" {
			insertAt = i + 1
			break
		}
	}
	// A standard profile must end before the next --new/profile marker.
	needsNew := true
	for i := insertAt; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		needsNew = t != "--new"
		break
	}
	if needsNew {
		block = append(block, "--new")
	}
	updated := make([]string, 0, len(lines)+len(block))
	updated = append(updated, lines[:insertAt]...)
	updated = append(updated, block...)
	updated = append(updated, lines[insertAt:]...)
	if err := c.SetNFQWS2Opt(strings.Join(updated, "\n")); err != nil {
		return false, err
	}
	return true, nil
}

var (
	wssizeShellIfRe     = regexp.MustCompile(`(?i)^\s*if\s+\[\s*"\$Z2R_WSSIZE_RAMP_ENABLE"`)
	softSuccessInRe     = regexp.MustCompile(`soft_success_in=[0-9]+`)
	softSuccessBytesRe  = regexp.MustCompile(`soft_success_bytes=[0-9]+`)
	profile2SoftSuccess = "soft_success_in=0:soft_success_bytes=65536"
)

// SyncWssizeRampVars пересчитывает Z2R_WSSIZE_RAMP_ARGS и Z2R_WSSIZE_RAMP_SKIP
// из ENABLE/MODE/WSIZE/SCALE/OPEN без shell-if. Нужно, чтобы Go BuildNfqws2Args
// и shell-источник /opt/zapret2/config видели одно и то же.
func (c *Config) SyncWssizeRampVars() (bool, error) {
	if c == nil {
		return false, nil
	}
	enable := c.GetDefault("Z2R_WSSIZE_RAMP_ENABLE", "0") == "1"
	mode := strings.ToLower(strings.TrimSpace(c.GetDefault("Z2R_WSSIZE_RAMP_MODE", "lite")))
	if mode != "full" && mode != "lite" {
		mode = "lite"
	}
	wsize := c.GetDefault("Z2R_WSSIZE_RAMP_WSIZE", "1")
	scale := c.GetDefault("Z2R_WSSIZE_RAMP_SCALE", "1")
	open := c.GetDefault("Z2R_WSSIZE_RAMP_OPEN", "1")

	wantArgs := ""
	wantSkip := "--skip"
	if enable {
		wantArgs = fmt.Sprintf(":mode=%s:wsize=%s:scale=%s:open=%s", mode, wsize, scale, open)
		wantSkip = ""
	}

	changed := false
	if _, ok := c.Get("Z2R_WSSIZE_MARK"); !ok {
		if err := c.Set("Z2R_WSSIZE_MARK", "0x04000000"); err != nil {
			return false, err
		}
		changed = true
	}
	if cur, ok := c.Get("Z2R_WSSIZE_RAMP_MODE"); !ok || strings.ToLower(cur) != mode {
		if err := c.Set("Z2R_WSSIZE_RAMP_MODE", mode); err != nil {
			return false, err
		}
		changed = true
	}
	if cur, ok := c.Get("Z2R_WSSIZE_RAMP_ARGS"); !ok || cur != wantArgs {
		if err := c.Set("Z2R_WSSIZE_RAMP_ARGS", wantArgs); err != nil {
			return false, err
		}
		changed = true
	}
	if cur, ok := c.Get("Z2R_WSSIZE_RAMP_SKIP"); !ok || cur != wantSkip {
		if err := c.Set("Z2R_WSSIZE_RAMP_SKIP", wantSkip); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

// StripWssizeShellIf удаляет legacy-блок `if [ "$Z2R_WSSIZE_RAMP_ENABLE" = 1 ]; then … fi`,
// который перезаписывал ARGS/SKIP при source в shell и не исполнялся Go-парсером.
func (c *Config) StripWssizeShellIf() bool {
	if c == nil || len(c.lines) == 0 {
		return false
	}
	out := make([]string, 0, len(c.lines))
	skipping := false
	changed := false
	for _, ln := range c.lines {
		trim := strings.TrimSpace(ln)
		if !skipping && wssizeShellIfRe.MatchString(trim) {
			skipping = true
			changed = true
			continue
		}
		if skipping {
			if trim == "fi" {
				skipping = false
			}
			continue
		}
		out = append(out, ln)
	}
	if !changed {
		return false
	}
	c.lines = out
	c.reindex()
	return true
}

// EnsureProfile2SoftSuccess гарантирует soft_success_in=0:soft_success_bytes=65536
// в circular_quality:key=2 и circular_locked:key=2 (анти false-success на tiny ~300B;
// 64KiB ближе к media-классу play).
// Locked-строка нужна: при активном локе quality-строка профиля 2 отсутствует в NFQWS2_OPT.
func (c *Config) EnsureProfile2SoftSuccess() (bool, error) {
	if c == nil {
		return false, nil
	}
	opt, ok := c.NFQWS2Opt()
	if !ok {
		return false, nil
	}
	needles := []string{"circular_quality:key=2", "circular_locked:key=2"}
	lines := strings.Split(opt, "\n")
	changed := false
	for i, ln := range lines {
		hit := false
		for _, needle := range needles {
			if strings.Contains(ln, needle) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		next := ln
		if softSuccessInRe.MatchString(next) {
			next = softSuccessInRe.ReplaceAllString(next, "soft_success_in=0")
		} else if strings.Contains(next, "default_strategy=") {
			next = regexp.MustCompile(`default_strategy=[0-9]+`).ReplaceAllStringFunc(next, func(m string) string {
				return m + ":" + profile2SoftSuccess
			})
		} else {
			// Вставка перед exclude_substrings или в конец lua-desync-токена.
			if strings.Contains(next, ":exclude_substrings=") {
				next = strings.Replace(next, ":exclude_substrings=", ":"+profile2SoftSuccess+":exclude_substrings=", 1)
			} else {
				next = next + ":" + profile2SoftSuccess
			}
		}
		if softSuccessBytesRe.MatchString(next) {
			next = softSuccessBytesRe.ReplaceAllString(next, "soft_success_bytes=65536")
		} else if strings.Contains(next, "soft_success_in=0") {
			next = strings.Replace(next, "soft_success_in=0", profile2SoftSuccess, 1)
		}
		if next != ln {
			lines[i] = next
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	return true, c.SetNFQWS2Opt(strings.Join(lines, "\n"))
}

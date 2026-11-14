package config

import (
	"fmt"
	"regexp"
	"strings"
)

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

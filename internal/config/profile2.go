package config

import (
	"regexp"
	"strings"
)

var profile2AllowNoHostArg = regexp.MustCompile(`:allow_nohost=[^:\s]+`)

// EnsureProfile2AllowNoHost lets the stable profile-2 circular_locked handler
// apply its exact SNI lock when nfqws2 has no conntrack metadata. In that case
// circular_locked uses a local per-packet record; the handler and strategy
// order stay unchanged.
func (c *Config) EnsureProfile2AllowNoHost() (bool, error) {
	block, ok := c.AutoStandardBlock("2")
	if !ok {
		return false, nil
	}
	changed := false
	for i, line := range block.Lines {
		updated := line
		for _, arg := range strings.Fields(line) {
			const handler = "--lua-desync=circular_locked:key=2"
			if !strings.HasPrefix(arg, handler) {
				continue
			}
			suffix := strings.TrimPrefix(arg, handler)
			if suffix != "" && !strings.HasPrefix(suffix, ":") {
				continue
			}
			updatedArg := arg
			if profile2AllowNoHostArg.MatchString(arg) {
				updatedArg = profile2AllowNoHostArg.ReplaceAllString(arg, ":allow_nohost=1")
			} else {
				updatedArg = handler + ":allow_nohost=1" + suffix
			}
			updated = strings.Replace(line, arg, updatedArg, 1)
			break
		}
		if updated != line {
			block.Lines[i] = updated
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	return true, c.ReplaceBlockBody(block.Name, block.Lines)
}

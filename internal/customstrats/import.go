package customstrats

import (
	"fmt"
	"strings"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/customblobs"
	"github.com/AloofLibra/zator/internal/fsutil"
)

// ImportError описывает строку, отклонённую при импорте.
type ImportError struct {
	Line   string `json:"line"`
	Reason string `json:"reason"`
}

// ImportResult содержит сводку пакетного импорта стратегий.
type ImportResult struct {
	Added  int           `json:"added"`
	Dups   int           `json:"dups"`
	Failed int           `json:"failed"`
	Notes  []string      `json:"notes"`
	Errors []ImportError `json:"errors"`
}

// JoinWraps склеивает разорванные переносами строки и последовательные строки `--lua-desync=...:strategy=N`
// с одинаковым непустым номером стратегии (комбо из конфига).
func JoinWraps(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var joined []string
	var buf string

	flush := func() {
		if buf != "" {
			joined = append(joined, buf)
			buf = ""
		}
	}

	for _, raw := range lines {
		ln := strings.TrimRight(raw, "\r")
		trim := strings.TrimLeft(ln, " \t")
		if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "*") {
			continue
		}
		if strings.Contains(ln, " : nfqws2 ") || strings.HasPrefix(ln, "nfqws2 ") ||
			strings.Contains(ln, " : nfqws ") || strings.HasPrefix(ln, "nfqws ") || strings.HasPrefix(ln, "tpws ") {
			flush()
			buf = trim
			continue
		}
		if strings.Contains(ln, "--lua-desync=") {
			if strings.HasPrefix(buf, "--lua-desync=") {
				tailBuf := extractStrategyTail(buf)
				tailCur := extractStrategyTail(trim)
				if tailBuf != "" && tailBuf == tailCur {
					buf += " " + trim
					continue
				}
			}
			flush()
			buf = trim
			continue
		}
		buf += trim
	}
	flush()
	if len(joined) == 0 {
		return ""
	}
	return strings.Join(joined, "\n") + "\n"
}

func extractStrategyTail(s string) string {
	m := strategyTailRe.FindString(s)
	if m == "" {
		return ""
	}
	return strings.TrimPrefix(m, ":strategy=")
}

// Import парсит многострочный вывод blockcheck2 / команды nfqws2 / строки --lua-desync=,
// подменяет неизвестные блобы на объявленные, дедуплицирует и пакетно сохраняет валидные записи.
func (s *Store) Import(block, text string, cfg *config.Config) (ImportResult, error) {
	block = strings.ToLower(strings.TrimSpace(block))
	info, ok := LookupBlock(block)
	if !ok {
		return ImportResult{}, fmt.Errorf("%w: Неизвестный блок: %s", ErrInvalid, block)
	}

	res := ImportResult{
		Notes:  []string{},
		Errors: []ImportError{},
	}
	knownBlobs := KnownBlobNames(cfg, s.customBlobsTSV)

	type candidate struct {
		spec    string
		comment string
	}

	wrapped := JoinWraps(text)
	if wrapped == "" {
		return res, nil
	}

	batchSeen := make(map[string]bool)
	var batch []candidate

	for _, ln := range strings.Split(strings.TrimSuffix(wrapped, "\n"), "\n") {
		if ln == "" {
			continue
		}
		if !strings.Contains(ln, "nfqws2") && !strings.Contains(ln, "--lua-desync=") {
			reason := "строка не похожа на стратегию или вывод блокчека"
			if strings.Contains(ln, "nfqws ") || strings.Contains(ln, "tpws ") || strings.Contains(ln, "--dpi-desync=") {
				reason = "стратегии zapret v1 не поддерживаются (нужен zapret2 / --lua-desync=)"
			}
			res.Errors = append(res.Errors, ImportError{Line: strings.ReplaceAll(ln, "\t", " "), Reason: reason})
			res.Failed++
			continue
		}

		groups, bcDomain, cmdNotes, parseErr := parseImportLine(ln)
		res.Notes = append(res.Notes, cmdNotes...)
		if parseErr != nil {
			res.Errors = append(res.Errors, ImportError{
				Line:   strings.ReplaceAll(ln, "\t", " "),
				Reason: parseErr.Error(),
			})
			res.Failed++
			continue
		}

		cloneTargets := collectCloneTargets(groups)
		fixedGroups := make([]string, 0, len(groups))
		for _, g := range groups {
			fg, fixNotes := fixGroupBlobs(g, info.Transport, knownBlobs, cloneTargets)
			res.Notes = append(res.Notes, fixNotes...)
			fixedGroups = append(fixedGroups, fg)
		}
		spec, normErr := NormalizeSpec(strings.Join(fixedGroups, " --lua-desync="))
		if normErr != nil {
			res.Errors = append(res.Errors, ImportError{
				Line:   strings.ReplaceAll(ln, "\t", " "),
				Reason: normErr.Error(),
			})
			res.Failed++
			continue
		}
		if err := Validate(block, spec, knownBlobs); err != nil {
			res.Errors = append(res.Errors, ImportError{
				Line:   strings.ReplaceAll(spec, "\t", " "),
				Reason: err.Error(),
			})
			res.Failed++
			continue
		}
		if batchSeen[spec] {
			res.Dups++
			continue
		}
		batchSeen[spec] = true
		comment := "импорт"
		if bcDomain != "" {
			comment = "blockcheck " + bcDomain
		}
		batch = append(batch, candidate{spec: spec, comment: comment})
	}

	if len(batch) == 0 {
		return res, nil
	}

	err := fsutil.WithDirLock(s.tsvPath, 0, 0, func() error {
		entries, err := s.readUnlocked()
		if err != nil {
			return err
		}
		existingBlock := make(map[string]bool)
		for _, e := range entries {
			if e.Block == block {
				existingBlock[e.Spec] = true
			}
		}
		nextID := nextStratID(entries)
		var toAppend []StrategyEntry
		for _, c := range batch {
			if existingBlock[c.spec] {
				res.Dups++
				continue
			}
			if len(entries)+len(toAppend) >= MaxEntries {
				res.Errors = append(res.Errors, ImportError{
					Line:   c.spec,
					Reason: ErrLimitReached.Error(),
				})
				res.Failed++
				continue
			}
			existingBlock[c.spec] = true
			toAppend = append(toAppend, StrategyEntry{
				ID:      nextID,
				Block:   block,
				Spec:    c.spec,
				Enabled: true,
				Comment: customblobs.SanitizeComment(c.comment),
			})
			nextID++
			res.Added++
		}
		if len(toAppend) == 0 {
			return nil
		}
		entries = append(entries, toAppend...)
		return s.writeUnlocked(entries)
	})
	if err != nil {
		res.Errors = append(res.Errors, ImportError{Line: "*", Reason: err.Error()})
		res.Failed += res.Added
		res.Added = 0
		return res, err
	}
	return res, nil
}

func parseImportLine(line string) (groups []string, bcDomain string, notes []string, err error) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
	if head, args, ok := strings.Cut(trimmed, " : nfqws2 "); ok {
		fields := strings.Fields(head)
		if len(fields) >= 3 {
			bcDomain = fields[2]
		}
		groups, notes, err = extractCommandGroups(args)
		return groups, bcDomain, notes, err
	}
	if strings.HasPrefix(trimmed, "nfqws2 ") {
		args := strings.TrimPrefix(trimmed, "nfqws2 ")
		groups, notes, err = extractCommandGroups(args)
		return groups, "", notes, err
	}

	rawGroups := strings.Split(trimmed, " --lua-desync=")
	for i, g := range rawGroups {
		g = strings.TrimSpace(g)
		if i == 0 {
			g = strings.TrimPrefix(g, "--lua-desync=")
		}
		if g == "" {
			continue
		}
		groups = append(groups, stripStrategyTails(g))
	}
	if len(groups) == 0 {
		return nil, "", nil, fmt.Errorf("не удалось разобрать строку")
	}
	return groups, "", nil, nil
}

func extractCommandGroups(args string) (groups []string, notes []string, err error) {
	for _, tok := range strings.Fields(args) {
		if strings.HasPrefix(tok, "--lua-desync=") {
			g := stripStrategyTails(strings.TrimPrefix(tok, "--lua-desync="))
			if g != "" {
				groups = append(groups, g)
			}
			continue
		}
		if isIgnoredDaemonFlag(tok) {
			continue
		}
		notes = append(notes, "вырезано лишнее: "+tok)
	}
	if len(groups) == 0 {
		return nil, notes, fmt.Errorf("не удалось разобрать строку")
	}
	return groups, notes, nil
}

func isIgnoredDaemonFlag(tok string) bool {
	prefixes := []string{
		"--blob=", "--lua-init=", "--payload=", "--template=", "--out-range=", "--in-range=",
		"--import=", "--filter-", "--hostlist", "--daemon", "--pidfile=", "--uid=", "--gid=",
		"--user=", "--qnum=", "--fwmark=", "--debug=", "--comment=",
	}
	if tok == "--new" || tok == "--skip" || tok == "--daemon" || tok == "--dry-run" {
		return true
	}
	for _, p := range prefixes {
		if strings.HasPrefix(tok, p) {
			return true
		}
	}
	return false
}

func stripStrategyTails(g string) string {
	for strategyTailRe.MatchString(g) {
		g = strategyTailRe.ReplaceAllString(g, "")
	}
	return g
}

func fixGroupBlobs(group, transport string, knownBlobs, cloneTargets map[string]bool) (string, []string) {
	if !strings.Contains(group, ":") {
		return group, nil
	}
	var notes []string
	toks := strings.Split(group, ":")
	isClonePrim := toks[0] == "tls_client_hello_clone"
	for i := 1; i < len(toks); i++ {
		key, val, ok := strings.Cut(toks[i], "=")
		if !ok || !blobRefKeys[key] {
			continue
		}
		if (isClonePrim && key == "blob") || cloneTargets[val] {
			continue
		}
		if strings.HasPrefix(val, "0x") || digitsRe.MatchString(val) {
			continue
		}
		if knownBlobs[val] {
			continue
		}
		replacement := RemapUnknownBlob(val, transport)
		notes = append(notes, fmt.Sprintf("блоб «%s» не объявлен — заменён на %s", val, replacement))
		toks[i] = key + "=" + replacement
	}
	return strings.Join(toks, ":"), notes
}

// RemapUnknownBlob подбирает штатный блоб при импорте чужой стратегии (как custom_strategies_bc_blob_target).
func RemapUnknownBlob(name, transport string) string {
	if name == "tlsgoogle" || name == "google_www" || strings.HasPrefix(name, "tls_clienthello") {
		return "maxru"
	}
	if transport == "udp" {
		return "quic_google"
	}
	return "maxru"
}

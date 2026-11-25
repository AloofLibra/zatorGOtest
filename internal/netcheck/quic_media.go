package netcheck

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	LabelYouTubeQUIC  = "YouTube QUIC"
	defaultProbeBytes = 1048576 // 1 MiB
	maxQUICRedirects  = 3
)

// QUICProbeResult — результат одной HTTP/3 byte-range пробы через h3-curl.
type QUICProbeResult struct {
	Offset      int64  `json:"offset"`
	Count       int64  `json:"count"`
	State       string `json:"state"`  // OK | FAIL | UNKNOWN
	Reason      string `json:"reason"` // media_h3 | not_h3 | cdn_403 | incomplete_transfer | network_error | timeout | ...
	HTTPCode    int    `json:"http_code"`
	HTTPVersion string `json:"http_version"`
	Bytes       int64  `json:"bytes"`
	TTFB        string `json:"ttfb,omitempty"`
	Total       string `json:"total,omitempty"`
	Speed       string `json:"speed,omitempty"`
}

// QUICProbeSummary — сводка HTTP/3 проверки медиапотока для TargetResult.
type QUICProbeSummary struct {
	OKCount     int               `json:"ok_count"`
	TotalProbes int               `json:"total_probes"`
	Rounds      int               `json:"rounds"`
	Reason      string            `json:"reason,omitempty"`
	Details     []QUICProbeResult `json:"details,omitempty"`
}

// QUICRunner выполняет запуск curl (инъектируется в тестах для кроссплатформенности).
type QUICRunner func(ctx context.Context, curlBin string, args []string, extraEnv []string) (stdout string, exitCode int, err error)

func defaultQUICRunner(ctx context.Context, curlBin string, args []string, extraEnv []string) (string, int, error) {
	cmd := exec.CommandContext(ctx, curlBin, args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	out, err := cmd.Output()
	if err == nil {
		return string(out), 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode(), nil
	}
	return string(out), -1, err
}

func validateHTTPSMediaURL(raw string) (*url.URL, error) {
	if raw == "" || strings.ContainsAny(raw, "\r\n\x00\"") {
		return nil, fmt.Errorf("некорректный URL медиапотока")
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return nil, fmt.Errorf("допустимы только HTTPS-ссылки")
	}
	return u, nil
}

func writeSecretCurlConfig(mediaURL string) (string, error) {
	if _, err := validateHTTPSMediaURL(mediaURL); err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "z2r-quic-cfg-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if err := os.Chmod(path, 0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	escaped := strings.ReplaceAll(mediaURL, `\`, `\\`)
	_, wErr := fmt.Fprintf(f, "url = \"%s\"\n", escaped)
	cErr := f.Close()
	if wErr != nil || cErr != nil {
		_ = os.Remove(path)
		if wErr != nil {
			return "", wErr
		}
		return "", cErr
	}
	return path, nil
}

// ProbeQUICRange выполняет одну HTTP/3 range-пробу, передавая подписанный URL строго
// через временный файл `--config` с правами 0600 (удаляется в defer, никогда не попадает в argv).
func ProbeQUICRange(
	ctx context.Context,
	curlBin, caBundle, rawURL, mime string,
	totalLen, offset, count int64,
	ua string,
	runner QUICRunner,
) QUICProbeResult {
	res := QUICProbeResult{Offset: offset, Count: count, State: "UNKNOWN", Reason: "invalid_params"}
	if curlBin == "" || totalLen < 4096 || count <= 0 {
		return res
	}
	if maxQuarter := totalLen / 4; maxQuarter > 0 && count > maxQuarter {
		count = maxQuarter
	}
	if offset > totalLen-count {
		offset = totalLen - count
	}
	if offset < 0 {
		offset = 0
	}
	end := offset + count - 1
	res.Offset = offset
	res.Count = count

	if ua == "" {
		ua = DefaultUserAgent
	}
	if runner == nil {
		runner = defaultQUICRunner
	}

	var extraEnv []string
	if caBundle != "" {
		extraEnv = append(extraEnv, "CURL_CA_BUNDLE="+caBundle)
	}

	curURL := rawURL
	for attempt := 0; attempt <= maxQUICRedirects; attempt++ {
		if ctx.Err() != nil {
			res.State, res.Reason = "UNKNOWN", "cancelled"
			return res
		}

		cfgPath, err := writeSecretCurlConfig(curURL)
		if err != nil {
			res.State, res.Reason = "UNKNOWN", "invalid_url"
			return res
		}
		hdrFile, hErr := os.CreateTemp("", "z2r-quic-hdr-*")
		if hErr != nil {
			_ = os.Remove(cfgPath)
			res.State, res.Reason = "UNKNOWN", "temp_error"
			return res
		}
		hdrPath := hdrFile.Name()
		_ = hdrFile.Close()
		_ = os.Chmod(hdrPath, 0o600)

		args := []string{
			"-q", "-4", "--noproxy", "*", "--http3-only", "-s",
			"--config", cfgPath,
			"-A", ua,
			"--range", fmt.Sprintf("%d-%d", offset, end),
			"--max-filesize", strconv.FormatInt(count, 10),
			"--limit-rate", "2M",
			"--connect-timeout", "5",
			"--max-time", "15",
			"--speed-limit", "1024",
			"--speed-time", "10",
			"-D", hdrPath,
			"-o", os.DevNull,
			"-w", "%{http_code}|%{size_download}|%{http_version}|%{time_connect}|%{time_appconnect}|%{time_starttransfer}|%{time_total}|%{speed_download}",
		}

		metrics, exitCode, runErr := runner(ctx, curlBin, args, extraEnv)
		hdrRaw, _ := os.ReadFile(hdrPath)
		_ = os.Remove(hdrPath)
		_ = os.Remove(cfgPath)

		if runErr != nil && exitCode < 0 {
			res.State, res.Reason = "FAIL", "exec_error"
			return res
		}

		code, dlBytes, ver, ttfb, total, speed := parseCurlMetrics(metrics)
		res.HTTPCode = code
		res.Bytes = dlBytes
		res.HTTPVersion = ver
		res.TTFB = ttfb
		res.Total = total
		res.Speed = speed

		if exitCode == 0 && (code == 301 || code == 302 || code == 307 || code == 308) {
			loc := parseHeaderValue(string(hdrRaw), "location")
			if attempt < maxQUICRedirects && loc != "" {
				if _, uErr := validateHTTPSMediaURL(loc); uErr == nil {
					curURL = loc
					continue
				}
			}
		}

		rangeValid := validateQUICHeaders(string(hdrRaw), offset, end, totalLen, mime)
		switch code {
		case 403:
			res.State, res.Reason = "UNKNOWN", "cdn_403"
		case 429:
			res.State, res.Reason = "UNKNOWN", "rate_limited"
		case 503:
			res.State, res.Reason = "UNKNOWN", "cdn_503"
		case 206:
			switch {
			case exitCode == 0 && ver == "3" && rangeValid && dlBytes == count:
				res.State, res.Reason = "OK", "media_h3"
			case ver != "3":
				res.State, res.Reason = "UNKNOWN", "not_h3"
			case rangeValid && (exitCode == 0 || exitCode == 18 || exitCode == 28 || exitCode == 56):
				res.State, res.Reason = "FAIL", "incomplete_transfer"
			default:
				res.State, res.Reason = "UNKNOWN", "invalid_response"
			}
		case 0:
			if exitCode == 28 {
				res.State, res.Reason = "FAIL", "timeout"
			} else {
				res.State, res.Reason = "FAIL", "network_error"
			}
		default:
			res.State, res.Reason = "UNKNOWN", fmt.Sprintf("http_%d", code)
		}
		return res
	}
	res.State, res.Reason = "UNKNOWN", "too_many_redirects"
	return res
}

func parseCurlMetrics(raw string) (code int, bytes int64, ver, ttfb, total, speed string) {
	parts := strings.Split(strings.TrimSpace(raw), "|")
	if len(parts) < 3 {
		return 0, 0, "", "", "", ""
	}
	code, _ = strconv.Atoi(strings.TrimSpace(parts[0]))
	bytes, _ = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	ver = strings.TrimSpace(parts[2])
	if len(parts) >= 8 {
		ttfb = strings.TrimSpace(parts[5])
		total = strings.TrimSpace(parts[6])
		speed = strings.TrimSpace(parts[7])
	}
	return
}

func parseHeaderValue(rawHdr, wantKey string) string {
	wantKey = strings.ToLower(wantKey)
	for _, ln := range strings.Split(strings.ReplaceAll(rawHdr, "\r\n", "\n"), "\n") {
		k, v, ok := strings.Cut(ln, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), wantKey) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func validateQUICHeaders(rawHdr string, offset, end, totalLen int64, wantMIME string) bool {
	var lastRange, lastType string
	for _, ln := range strings.Split(strings.ReplaceAll(rawHdr, "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(ln)
		if strings.HasPrefix(trim, "HTTP/") {
			lastRange, lastType = "", ""
			continue
		}
		k, v, ok := strings.Cut(trim, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "content-range":
			lastRange = strings.TrimSpace(v)
		case "content-type":
			typ, _, _ := strings.Cut(strings.TrimSpace(v), ";")
			lastType = strings.TrimSpace(strings.ToLower(typ))
		}
	}
	expectedRange := fmt.Sprintf("bytes %d-%d/%d", offset, end, totalLen)
	if lastRange != expectedRange {
		return false
	}
	if wantMIME != "" && !strings.EqualFold(lastType, strings.TrimSpace(wantMIME)) {
		return false
	}
	return true
}

// CheckQUICMedia прогоняет серию HTTP/3 проб (по 5 позиций * rounds раундов) и строит TargetResult.
func (c *Checker) CheckQUICMedia(ctx context.Context, curlBin string, media YouTubeMedia, rounds int) TargetResult {
	if rounds < 1 {
		rounds = 1
	}
	if rounds > 5 {
		rounds = 5
	}

	rawURL, mime, totalLen, host := selectQUICStream(media)
	if host == "" {
		host = "googlevideo.com"
	}
	target := "quic://" + host
	if rawURL == "" || totalLen < 4096 {
		return TargetResult{
			Label:   LabelYouTubeQUIC,
			Target:  target,
			Verdict: VerdictUnknown,
			Text:    "Нет подходящего медиапотока для QUIC-проверки",
		}
	}

	chunk := int64(defaultProbeBytes)
	if maxQuarter := totalLen / 4; maxQuarter > 0 && chunk > maxQuarter {
		chunk = maxQuarter
	}
	if chunk < 4096 {
		chunk = 4096
	}
	offsets := buildQUICOffsets(totalLen, chunk)
	caBundle := c.H3Env.FindCABundle()
	ua := c.Options.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}

	var details []QUICProbeResult
	okCount, cdnRefused := 0, 0
	var totalBytes int64
	var lastCode int
	var sumSec float64
	startAll := time.Now()

	for r := 0; r < rounds; r++ {
		for _, off := range offsets {
			if ctx.Err() != nil {
				break
			}
			pr := ProbeQUICRange(ctx, curlBin, caBundle, rawURL, mime, totalLen, off, chunk, ua, c.QUICRunner)
			details = append(details, pr)
			if pr.HTTPCode != 0 {
				lastCode = pr.HTTPCode
			}
			totalBytes += pr.Bytes
			if pr.Total != "" {
				if sec, err := strconv.ParseFloat(pr.Total, 64); err == nil {
					sumSec += sec
				}
			}
			switch pr.State {
			case "OK":
				okCount++
			case "UNKNOWN":
				if pr.Reason == "cdn_403" || pr.Reason == "rate_limited" || pr.Reason == "cdn_503" {
					cdnRefused++
				}
			}
		}
	}

	totalProbes := len(details)
	summary := &QUICProbeSummary{
		OKCount:     okCount,
		TotalProbes: totalProbes,
		Rounds:      rounds,
		Details:     details,
	}

	var verdict, text string
	switch {
	case totalProbes == 0:
		verdict = VerdictUnknown
		text = "HTTP/3 проверка отменена"
		summary.Reason = "cancelled"
	case okCount == totalProbes:
		verdict = VerdictOK
		text = fmt.Sprintf("HTTP/3 OK (%d/%d фрагментов)", okCount, totalProbes)
		summary.Reason = "media_h3"
	case okCount > 0:
		verdict = VerdictWarn
		text = fmt.Sprintf("QUIC нестабилен (%d/%d фрагментов)", okCount, totalProbes)
		summary.Reason = "partial_h3"
	case cdnRefused == totalProbes:
		verdict = VerdictUnknown
		text = fmt.Sprintf("CDN отклонил запрос (0/%d, %s)", totalProbes, details[0].Reason)
		summary.Reason = details[0].Reason
	default:
		verdict = VerdictFail
		reason := details[0].Reason
		if reason == "" {
			reason = "network_error"
		}
		summary.Reason = reason
		text = fmt.Sprintf("HTTP/3 недоступен (0/%d, %s)", totalProbes, reason)
	}

	if sumSec <= 0 {
		sumSec = time.Since(startAll).Seconds()
	}
	dlState := DLOK
	if okCount == 0 {
		dlState = DLFail
	} else if okCount < totalProbes {
		dlState = DLCut
	}
	return TargetResult{
		Label:   LabelYouTubeQUIC,
		Target:  target,
		Verdict: verdict,
		Text:    text,
		Download: &Download{
			Code:  lastCode,
			Size:  int(totalBytes),
			Time:  strconv.FormatFloat(sumSec, 'f', 3, 64),
			State: dlState,
			Text:  text,
		},
		QUIC: summary,
	}
}

func selectQUICStream(m YouTubeMedia) (rawURL, mime string, length int64, host string) {
	if m.VideoURL != "" && m.VideoLength >= 4096 {
		return m.VideoURL, m.VideoMIME, m.VideoLength, m.VideoHost
	}
	return m.AudioURL, m.AudioMIME, m.AudioLength, m.AudioHost
}

func buildQUICOffsets(totalLen, chunk int64) []int64 {
	clamp := func(off int64) int64 {
		if off < 0 {
			return 0
		}
		if off+chunk > totalLen {
			return totalLen - chunk
		}
		return off
	}
	return []int64{
		clamp(0),
		clamp(chunk),
		clamp(2 * chunk),
		clamp(totalLen/2 - chunk/2),
		clamp(totalLen - chunk),
	}
}

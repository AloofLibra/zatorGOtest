package config

import "strings"

// VoicePorts — порты голосовых сервисов профиля 6 (config_profile_voice_ports).
const VoicePorts = "50000-50099,1400,3478-3481,5349,19294-19344"

// DNSPorts — порт DNS-профиля 10 (config_profile_dns_ports).
const DNSPorts = "53"

// CSVContains сообщает, есть ли token в списке csv (разделитель — запятая).
func CSVContains(csv, token string) bool {
	if csv == "" {
		return false
	}
	for {
		i := strings.IndexByte(csv, ',')
		if i < 0 {
			return csv == token
		}
		if csv[:i] == token {
			return true
		}
		csv = csv[i+1:]
	}
}

// CSVAdd добавляет в конец csv токены из tokens (через запятую), которых там ещё нет.
func CSVAdd(csv, tokens string) string {
	var out strings.Builder
	out.Grow(len(csv) + len(tokens))
	out.WriteString(csv)
	seen := make(map[string]struct{})
	for _, tok := range strings.Split(csv, ",") {
		seen[tok] = struct{}{}
	}
	for _, tok := range strings.Split(tokens, ",") {
		if tok == "" {
			continue
		}
		if _, ok := seen[tok]; ok {
			continue
		}
		if out.Len() > 0 {
			out.WriteByte(',')
		}
		out.WriteString(tok)
		seen[tok] = struct{}{}
	}
	return out.String()
}

// CSVRemove удаляет из csv токены из tokens; пустые элементы отбрасываются.
func CSVRemove(csv, tokens string) string {
	remove := map[string]struct{}{}
	for _, tok := range strings.Split(tokens, ",") {
		remove[tok] = struct{}{}
	}
	var keep []string
	for _, cur := range strings.Split(csv, ",") {
		if _, ok := remove[cur]; cur == "" || ok {
			continue
		}
		keep = append(keep, cur)
	}
	return strings.Join(keep, ",")
}

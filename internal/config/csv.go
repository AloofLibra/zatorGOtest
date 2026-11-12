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
	for _, t := range strings.Split(csv, ",") {
		if t == token {
			return true
		}
	}
	return false
}

// CSVAdd добавляет в конец csv токены из tokens (через запятую), которых там ещё нет.
func CSVAdd(csv, tokens string) string {
	out := csv
	for _, tok := range strings.Split(tokens, ",") {
		if tok == "" || CSVContains(out, tok) {
			continue
		}
		if out != "" {
			out += ","
		}
		out += tok
	}
	return out
}

// CSVRemove удаляет из csv токены из tokens; пустые элементы отбрасываются.
func CSVRemove(csv, tokens string) string {
	remove := map[string]bool{}
	for _, tok := range strings.Split(tokens, ",") {
		remove[tok] = true
	}
	var keep []string
	for _, cur := range strings.Split(csv, ",") {
		if cur == "" || remove[cur] {
			continue
		}
		keep = append(keep, cur)
	}
	return strings.Join(keep, ",")
}

// Package googlevideo — центральный список IPv4-адресов googlevideo,
// локальный пассивный коллектор DNS-наблюдений и очередь их отправки.
// Порт lib/googlevideo_ips.sh и tools/googlevideo-dns-listener.sh.
//
// Состав:
//   - ParseList/ValidateList и CentralList — загрузка и проверка центрального
//     списка (заголовок, лимиты, только публичный IPv4, кеш 6 ч);
//   - Collector — состояние коллектора: local.tsv, pending.tsv, reported.tsv;
//   - Uploader — очередь отправки (с проверкой согласия на телеметрию);
//   - Manager — менеджер слушателя (внешний C-сниффер или режим ubus).
package googlevideo

import (
	"strconv"
	"strings"
)

// ParseIPv4 разбирает строгую десятичную запись a.b.c.d: ровно четыре октета
// 0..255 без ведущих нулей, пробелов и прочих символов.
func ParseIPv4(s string) (o [4]int, ok bool) {
	if s == "" || strings.Trim(s, "0123456789.") != "" {
		return o, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return o, false
	}
	for i, p := range parts {
		if p == "" || len(p) > 3 || (len(p) > 1 && p[0] == '0') {
			return o, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n > 255 {
			return o, false
		}
		o[i] = n
	}
	return o, true
}

// PublicIPv4 сообщает, что s — строгий IPv4 вне зарезервированных диапазонов
// (googlevideo_ipv4_public): 0/8, 10/8, 127/8, 224/3, 169.254/16, 172.16/12,
// 192.168/16, 192.0.0/24 и 192.0.2/24, 192.88.99/24, 192.31.196/24, 192.52.193/24,
// 192.175.48/24, 198.18/15, 198.51.100/24, 203.0.113/24, 100.64/10.
func PublicIPv4(s string) bool {
	o, ok := ParseIPv4(s)
	if !ok {
		return false
	}
	a, b, c := o[0], o[1], o[2]
	switch {
	case a == 0, a == 10, a == 127, a >= 224:
		return false
	case a == 169 && b == 254:
		return false
	case a == 172 && b >= 16 && b <= 31:
		return false
	case a == 192 && b == 168:
		return false
	case a == 192 && b == 0 && c <= 2:
		return false
	case a == 192 && b == 88 && c == 99:
		return false
	case a == 192 && b == 31 && c == 196:
		return false
	case a == 192 && b == 52 && c == 193:
		return false
	case a == 192 && b == 175 && c == 48:
		return false
	case a == 198 && (b == 18 || b == 19):
		return false
	case a == 198 && b == 51 && c == 100:
		return false
	case a == 203 && b == 0 && c == 113:
		return false
	case a == 100 && b >= 64 && b <= 127:
		return false
	}
	return true
}

// NormalizeHostname приводит имя к нижнему регистру без завершающей точки и
// проверяет, что это поддомен googlevideo.com из символов [a-z0-9.-] без
// пустых меток и без ведущей точки (googlevideo_hostname_valid).
func NormalizeHostname(host string) (string, bool) {
	h := strings.ToLower(host)
	h = strings.TrimSuffix(h, ".")
	if h == "" || strings.Trim(h, "abcdefghijklmnopqrstuvwxyz0123456789.-") != "" {
		return "", false
	}
	if strings.HasPrefix(h, ".") || strings.Contains(h, "..") || strings.HasSuffix(h, ".") {
		return "", false
	}
	if !strings.HasSuffix(h, ".googlevideo.com") || h == "googlevideo.com" {
		return "", false
	}
	return h, true
}

// ValidHostname — NormalizeHostname без возврата имени.
func ValidHostname(host string) bool {
	_, ok := NormalizeHostname(host)
	return ok
}

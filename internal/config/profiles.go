package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Диапазон идентификаторов профилей. ID 5 зарезервирован: отдельный QUIC/UDP
// 443 профиль заменён общим UDP-профилем 7.
const (
	MinProfile = 1
	MaxProfile = 10
)

// Профили с особой логикой.
const (
	ProfileUDPGames  = 7
	ProfileFallbackT = 8 // fallback TLS
	ProfileFallbackH = 9 // fallback HTTP
	ProfileDNS       = 10
)

// Profile — метаданные профиля стратегий (формат совместим с status.cgi).
type Profile struct {
	ID          int      `json:"profile"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Title       string   `json:"title,omitempty"`
	Protos      []string `json:"protos"`
	MaxStrategy int      `json:"max_strategy"`

	IsUDPGames  bool `json:"is_udp_games,omitempty"`
	IsFallback  bool `json:"is_fallback,omitempty"`
	IsDNSDesync bool `json:"is_dns_desync,omitempty"`
}

type profileMeta struct {
	label, description, title string
	protos                    []string
	defaultMax                int
}

// profileTable — значения из docs/API.md и исторического lib/config.sh
// (config_profile_title / config_profile_proto_list).
var profileTable = [MaxProfile + 1]profileMeta{
	1:  {"YouTube TCP", "Основной TCP профиль для YouTube", "TCP 443 (YouTube)", []string{"tls", "http"}, 44},
	2:  {"Googlevideo", "Видео-домены YouTube (стратегия из lock)", "TCP 443 (Googlevideo)", []string{"tls"}, 44},
	3:  {"RKN Лист", "Основные блокировки сайтов", "TCP 443 (RKN)", []string{"tls"}, 44},
	4:  {"Discord TCP", "TCP профиль Discord", "TCP 443 (Discord)", []string{"tls"}, 28},
	6:  {"Voice UDP", "Discord/STUN и голосовые сервисы", "UDP Voice (Discord/STUN)", []string{"udp"}, 32},
	7:  {"UDP Games", "Игровой UDP (порты 1026-65531)", "UDP Games (1026-65531)", []string{"udp"}, 20},
	8:  {"Fallback TLS", "Безразборный режим TLS (profile 8)", "Fallback TLS", []string{"tls"}, 44},
	9:  {"Fallback HTTP", "Безразборный режим HTTP (profile 9)", "Fallback HTTP", []string{"http"}, 8},
	10: {"DNS Антиспуф", "Защита UDP:53 от подмены DNS-ответов (клон с малым TTL)", "DNS антиспуф UDP:53", []string{"udp"}, 20},
}

// ValidProfile сообщает, есть ли профиль с таким ID.
func ValidProfile(id int) bool { return id >= MinProfile && id <= MaxProfile && id != 5 }

// DefaultMaxStrategy возвращает максимум стратегий профиля для штатного
// config.default; 0 для неизвестного или удалённого профиля.
func DefaultMaxStrategy(id int) int {
	if !ValidProfile(id) {
		return 0
	}
	return profileTable[id].defaultMax
}

// ProfileProtos возвращает протоколы профиля (tls|http|udp); nil для неизвестного.
func ProfileProtos(id int) []string {
	if !ValidProfile(id) {
		return nil
	}
	return append([]string(nil), profileTable[id].protos...)
}

// ProfileHasProto сообщает, допустим ли протокол для профиля.
func ProfileHasProto(id int, proto string) bool {
	for _, p := range ProfileProtos(id) {
		if p == proto {
			return true
		}
	}
	return false
}

// ProfileLabel возвращает короткую подпись профиля («YouTube TCP»).
func ProfileLabel(id int) string {
	if !ValidProfile(id) {
		return fmt.Sprintf("Профиль %d", id)
	}
	return profileTable[id].label
}

// ProfileTitle возвращает меню-заголовок профиля («TCP 443 (YouTube)»).
func ProfileTitle(id int) string {
	if !ValidProfile(id) {
		return fmt.Sprintf("Профиль %d", id)
	}
	return profileTable[id].title
}

// DefaultProfile возвращает метаданные профиля с максимумами по умолчанию.
func DefaultProfile(id int) (Profile, bool) {
	if !ValidProfile(id) {
		return Profile{}, false
	}
	m := profileTable[id]
	return Profile{
		ID:          id,
		Label:       m.label,
		Description: m.description,
		Title:       m.title,
		Protos:      append([]string(nil), m.protos...),
		MaxStrategy: m.defaultMax,
		IsUDPGames:  id == ProfileUDPGames,
		IsFallback:  id == ProfileFallbackT || id == ProfileFallbackH,
		IsDNSDesync: id == ProfileDNS,
	}, true
}

// DefaultProfiles возвращает активные профили с максимумами по умолчанию.
func DefaultProfiles() []Profile {
	out := make([]Profile, 0, MaxProfile-1)
	for id := MinProfile; id <= MaxProfile; id++ {
		if p, ok := DefaultProfile(id); ok {
			out = append(out, p)
		}
	}
	return out
}

// Profiles возвращает активные профили; MaxStrategy вычисляется по конфигу
// (ProfileMaxStrategy), а при невозможности определить — берётся значение
// по умолчанию.
func (c *Config) Profiles() []Profile {
	out := DefaultProfiles()
	for i := range out {
		if n := c.ProfileMaxStrategy(out[i].ID); n > 0 {
			out[i].MaxStrategy = n
		}
	}
	return out
}

// ProfileMaxStrategyOrDefault — ProfileMaxStrategy с откатом на значение по умолчанию.
func (c *Config) ProfileMaxStrategyOrDefault(id int) int {
	if n := c.ProfileMaxStrategy(id); n > 0 {
		return n
	}
	return DefaultMaxStrategy(id)
}

var (
	templateLineRe = regexp.MustCompile(`^--template=`)
	importLineRe   = regexp.MustCompile(`^--import([=\s]|$)`)
	newLineRe      = regexp.MustCompile(`^\s*--new\s*$`)
)

func templateName(line string) string {
	v := strings.TrimPrefix(line, "--template=")
	if i := strings.IndexAny(v, " \t"); i >= 0 {
		v = v[:i]
	}
	return v
}

func importName(line string) string {
	v := strings.TrimPrefix(line, "--import")
	v = strings.TrimLeft(v, "= \t")
	if i := strings.IndexAny(v, " \t"); i >= 0 {
		v = v[:i]
	}
	return v
}

// ProfileMaxStrategy вычисляет максимальный номер стратегии профиля по
// вхождениям `strategy=N` — порт awk-скрипта config_profile_max_strategy.
//
// Приоритет: стратегии блока с логическим ключом key=<id>
// (circular_locked / circular_quality / rst_guard_locked), затем legacy
// fallback-блок (#Z2R_FALLBACK_*, профили 8/9), затем позиционный номер блока
// в NFQWS2_OPT. Стратегии шаблонов (--template=…) учитываются через --import.
// Возвращает 0, если стратегий не найдено.
func (c *Config) ProfileMaxStrategy(id int) int {
	if !ValidProfile(id) {
		return 0
	}
	var beginMark, endMark string
	switch id {
	case ProfileFallbackT:
		beginMark, endMark = "#Z2R_FALLBACK_BEGIN", "#Z2R_FALLBACK_END"
	case ProfileFallbackH:
		beginMark, endMark = "#Z2R_FALLBACK_HTTP_BEGIN", "#Z2R_FALLBACK_HTTP_END"
	}
	keyRe := regexp.MustCompile(`--lua-desync=(circular_locked|circular_quality|rst_guard_locked):key=` +
		strconv.Itoa(id) + `([^0-9]|$)`)

	var (
		inOpt, inTemplate, inFallback bool
		keyActive, posActive          bool
		tpl                           string
		prof                          int
		keyedMax, fallbackMax, posMax int
	)
	tplMax := map[string]int{}

	scan := func(line string) {
		for _, m := range strategyRe.FindAllStringSubmatch(line, -1) {
			n, _ := strconv.Atoi(m[1])
			if inTemplate {
				if n > tplMax[tpl] {
					tplMax[tpl] = n
				}
				continue
			}
			if keyActive && n > keyedMax {
				keyedMax = n
			}
			if posActive && prof == id && n > posMax {
				posMax = n
			}
			if inFallback && n > fallbackMax {
				fallbackMax = n
			}
		}
	}

	for _, line := range c.lines {
		if strings.HasPrefix(line, OptVar+`="`) {
			inOpt = true
		}
		if beginMark != "" && strings.Contains(line, beginMark) {
			inFallback = true
		}
		if endMark != "" && strings.Contains(line, endMark) {
			inFallback = false
		}

		if inOpt {
			switch {
			case templateLineRe.MatchString(line):
				inTemplate = true
				tpl = templateName(line)
				keyActive = false
			case newLineRe.MatchString(line):
				inTemplate, tpl = false, ""
				keyActive, posActive = false, false
			case !inTemplate:
				// Начало позиционного профиля: первая «опционная» строка блока.
				if !posActive && strings.HasPrefix(line, "--") &&
					!strings.HasPrefix(line, "--new") &&
					!strings.HasPrefix(line, "--lua-init") &&
					!strings.HasPrefix(line, "--blob=") &&
					!strings.HasPrefix(line, "--reasm-disable") {
					prof++
					posActive = true
				}
				if keyRe.MatchString(line) {
					keyActive = true
				}
			}

			if !inTemplate && importLineRe.MatchString(line) {
				imp := importName(line)
				if keyActive && tplMax[imp] > keyedMax {
					keyedMax = tplMax[imp]
				}
				if posActive && prof == id && tplMax[imp] > posMax {
					posMax = tplMax[imp]
				}
			}
		}

		scan(line)
		if inOpt && line == `"` {
			break
		}
	}

	switch {
	case keyedMax > 0:
		return keyedMax
	case fallbackMax > 0:
		return fallbackMax
	default:
		return posMax
	}
}

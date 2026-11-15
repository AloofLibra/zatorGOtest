// Package netcheck — проверки доступности сайтов и DNS: порт lib/netcheck.sh.
//
// Движок параллельно пробует TLS 1.2 и TLS 1.3 (GET с ограничением тела,
// редиректы следуют, проверка сертификата опциональна — как curl -L -k),
// затем при 2xx/3xx докачивает до 64 КБ и выносит вердикт ok|warn|fail|unknown.
// JSON-структуры совпадают с ответом check.cgi (api_check).
package netcheck

import (
	"strconv"
	"strings"
	"time"
)

// Значения по умолчанию (Z2R_TLS_* из netcheck.sh).
const (
	DefaultConnectTimeout  = 3 * time.Second
	DefaultMaxTime         = 10 * time.Second
	DefaultDownloadMaxTime = 10 * time.Second
	DefaultDownloadStall   = 1 * time.Second
	DefaultDownloadRange   = 65536
	DefaultUserAgent       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"
)

// Вердикты проверки цели.
const (
	VerdictOK      = "ok"
	VerdictWarn    = "warn"
	VerdictFail    = "fail"
	VerdictUnknown = "unknown"
)

// Состояния версии TLS (z2r_tls_version_state).
const (
	StateOK          = "ok"          // 2xx/3xx
	StateHTTP        = "http"        // любой другой HTTP-код: транспорт жив
	StateAborted     = "aborted"     // проба остановлена: другая версия уже ответила
	StateDNS         = "dns"         // домен не разрешается
	StateTimeout     = "timeout"     // таймаут
	StateConn        = "conn"        // TCP не устанавливается
	StateTLS         = "tls"         // сбой рукопожатия / обрыв после connect
	StateUnsupported = "unsupported" // версия TLS не поддерживается локально
	StateNone        = "none"        // прочее
)

// Состояния этапа докачки (z2r_tls_download_state).
const (
	DLOK   = "ok"   // тело дошло
	DLCut  = "cut"  // байты пошли, но поток оборвался
	DLZero = "zero" // 0 байт
	DLFail = "fail" // ошибка/таймаут без данных
)

// TLSVersion — версия TLS для пробы.
type TLSVersion int

// Поддерживаемые версии.
const (
	TLS12 TLSVersion = 12
	TLS13 TLSVersion = 13
)

// Label возвращает "1.2" или "1.3".
func (v TLSVersion) Label() string {
	switch v {
	case TLS12:
		return "1.2"
	case TLS13:
		return "1.3"
	}
	return strconv.Itoa(int(v))
}

// TLSPref — требование к версиям TLS при оценке стратегии.
type TLSPref string

// Допустимые значения (аргумент tls_pref в z2r_tls_short_result).
const (
	PrefAny  TLSPref = "any"
	Pref12   TLSPref = "12"
	Pref13   TLSPref = "13"
	PrefBoth TLSPref = "both"
)

// ParseTLSPref разбирает значение; неизвестное — PrefAny.
func ParseTLSPref(s string) TLSPref {
	switch TLSPref(strings.ToLower(strings.TrimSpace(s))) {
	case Pref12:
		return Pref12
	case Pref13:
		return Pref13
	case PrefBoth:
		return PrefBoth
	}
	return PrefAny
}

// ErrKind — класс транспортной ошибки (аналог кода возврата curl).
type ErrKind string

// Классы ошибок.
const (
	ErrNone    ErrKind = ""
	ErrDNS     ErrKind = "dns"
	ErrTimeout ErrKind = "timeout"
	ErrConn    ErrKind = "conn"
	ErrTLS     ErrKind = "tls"
	ErrAborted ErrKind = "aborted"
	ErrOther   ErrKind = "other"
)

// VersionDetail — результат пробы одной версии TLS (tls12_detail/tls13_detail).
type VersionDetail struct {
	Code  int    `json:"code"`  // HTTP-код, 0 — ответа нет
	Proto string `json:"proto"` // "HTTP/2", "HTTP/1.1" или "-"
	Time  string `json:"time"`  // секунды или "-"
	IP    string `json:"ip"`    // удалённый адрес или "-"
	State string `json:"state"` // см. State*
	Text  string `json:"text"`

	// Err — класс транспортной ошибки (в JSON не попадает).
	Err ErrKind `json:"-"`
}

// Answered сообщает, что транспорт ответил любым HTTP-кодом.
func (d VersionDetail) Answered() bool { return d.State == StateOK || d.State == StateHTTP }

// CodeOK сообщает, что код 2xx/3xx.
func (d VersionDetail) CodeOK() bool { return CodeOK(d.Code) }

// Download — результат этапа докачки (поле download).
type Download struct {
	Code  int    `json:"code"`
	Size  int    `json:"size"`
	Time  string `json:"time"`
	State string `json:"state"` // ok|zero|cut|fail
	Text  string `json:"text"`

	// Retry — успех достигнут со второй попытки.
	Retry bool `json:"-"`
	// Err — класс ошибки (в JSON не попадает).
	Err ErrKind `json:"-"`
}

// Seconds возвращает время докачки в секундах (0, если неизвестно).
func (d Download) Seconds() float64 {
	f, err := strconv.ParseFloat(d.Time, 64)
	if err != nil || f < 0 {
		return 0
	}
	return f
}

// Speed возвращает скорость докачки в байтах/с (0, если неизвестна).
func (d Download) Speed() float64 {
	t := d.Seconds()
	if t <= 0 {
		return 0
	}
	return float64(d.Size) / t
}

// TargetResult — результат проверки одной цели; формат элемента
// check.results из docs/API.md.
type TargetResult struct {
	Label       string        `json:"label"`
	Target      string        `json:"target"`
	TLS12       int           `json:"tls12"` // legacy: 1 — транспорт ответил
	TLS13       int           `json:"tls13"`
	Verdict     string        `json:"verdict"`
	Text        string        `json:"text"`
	TLS12Detail VersionDetail `json:"tls12_detail"`
	TLS13Detail VersionDetail `json:"tls13_detail"`
	Download    *Download     `json:"download"` // nil -> null (этап пропущен)
	// Media — классификация googlevideo mid-stream / tiny (Package B); nil если не применимо.
	Media *MediaProbe `json:"media,omitempty"`
}

// CheckResponse — тело ответа check.cgi.
type CheckResponse struct {
	Results []TargetResult `json:"results"`
	Message string         `json:"message,omitempty"`

	// Метаданные profile-check (заполняет webapi): что реально проверялось.
	Profile        int    `json:"profile,omitempty"`
	Scope          string `json:"scope,omitempty"`
	Lock           string `json:"lock,omitempty"`            // auto | 0 | N
	Zapret2Running *bool  `json:"zapret2_running,omitempty"` // nil = неизвестно
	WaitedApply    bool   `json:"waited_apply,omitempty"`
	Applied        *bool  `json:"applied,omitempty"` // expect_lock совпал со store
	Retried        bool   `json:"retried,omitempty"` // была повторная TLS-попытка
}

// CodeOK сообщает, что HTTP-код 2xx или 3xx (z2r_tls_code_ok).
func CodeOK(code int) bool { return code >= 200 && code < 400 }

// formatSeconds форматирует длительность как время curl (секунды).
func formatSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}

// Package orchestra управляет состоянием orchestra: фиксациями (lock) стратегий
// в TSV-файлах и переопределениями профилей (blob/SNI/режим/размер клона).
//
// Формат и семантика повторяют lib/orchestra_state.sh и orchestra/locked.lua:
//
//	locked.tsv         строки default-scope: profile\tproto\tstrategy
//	                   (legacy: profile\tstrategy = tls; scope\tprofile\tproto\tstrategy)
//	locked.manual.tsv  то же для профилей 8/9 (fallback)
//	scopes/mark_N.tsv  фиксации client scope mark:N: profile\tproto\tstrategy
//	blob_override.tsv, sni_override.tsv, mode_override.tsv, clonesize.tsv:
//	                   profile\tvalue
//
// Отсутствие строки означает стратегию "auto".
package orchestra

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Ошибки пакета.
var (
	// ErrInvalid — недопустимый scope/профиль/протокол/стратегия/значение.
	ErrInvalid = errors.New("orchestra: invalid argument")
	// ErrConflict — для ключа в файле несколько строк с разными значениями
	// (или дубликаты, мешающие безопасной записи).
	ErrConflict = errors.New("orchestra: conflicting duplicate rows")
	// ErrLockTimeout — не удалось получить файловую блокировку записи.
	ErrLockTimeout = errors.New("orchestra: write lock timeout")
)

// Proto — протокол фиксации.
type Proto string

// Допустимые протоколы.
const (
	ProtoTLS  Proto = "tls"
	ProtoHTTP Proto = "http"
	ProtoUDP  Proto = "udp"
)

// Valid сообщает, является ли p одним из tls|http|udp.
func (p Proto) Valid() bool { return p == ProtoTLS || p == ProtoHTTP || p == ProtoUDP }

// ParseProto разбирает протокол без учёта регистра.
func ParseProto(s string) (Proto, error) {
	p := Proto(strings.ToLower(strings.TrimSpace(s)))
	if !p.Valid() {
		return "", fmt.Errorf("%w: protocol %q", ErrInvalid, s)
	}
	return p, nil
}

// Strategy — зафиксированная стратегия: StrategyAuto (нет фиксации),
// 0 (профиль выключен, skip) либо номер стратегии N >= 1.
type Strategy int

const (
	// StrategyAuto — «авто»: строки в файле нет.
	StrategyAuto Strategy = -1
	// StrategyOff — профиль выключен (в файле хранится 0).
	StrategyOff Strategy = 0
)

// IsAuto сообщает, что фиксации нет.
func (s Strategy) IsAuto() bool { return s < 0 }

// IsOff сообщает, что профиль выключен (0).
func (s Strategy) IsOff() bool { return s == StrategyOff }

// String возвращает "auto" либо десятичный номер.
func (s Strategy) String() string {
	if s.IsAuto() {
		return "auto"
	}
	return strconv.Itoa(int(s))
}

// ParseStrategy разбирает "auto"/"clear"/"" (-> StrategyAuto), "skip" (-> 0)
// или неотрицательное десятичное число.
func ParseStrategy(s string) (Strategy, error) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "auto", "clear":
		return StrategyAuto, nil
	case "skip":
		return StrategyOff, nil
	}
	if !digitsRe.MatchString(s) {
		return 0, fmt.Errorf("%w: strategy %q", ErrInvalid, s)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%w: strategy %q", ErrInvalid, s)
	}
	return Strategy(n), nil
}

// Record — одна фиксация стратегии.
type Record struct {
	Scope    string   // "default" или "mark:N"
	Profile  string   // номер профиля "1".."10" либо hostname кастомного домена
	Proto    Proto    // tls | http | udp
	Strategy Strategy // номер стратегии или 0; в файле не бывает StrategyAuto
}

func (r Record) String() string {
	return r.Scope + "/" + r.Profile + "/" + string(r.Proto) + "=" + r.Strategy.String()
}

// Scope: "default" или "mark:<десятичное число >= 1>".
const ScopeDefault = "default"

var (
	digitsRe   = regexp.MustCompile(`^[0-9]+$`)
	markRe     = regexp.MustCompile(`^mark:([1-9][0-9]*)$`)
	hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)+$`)
)

// MarkScope возвращает имя scope для метки клиента ("mark:N").
func MarkScope(n int) string { return "mark:" + strconv.Itoa(n) }

// ValidScope проверяет scope: default либо mark:N, N >= 1.
func ValidScope(scope string) bool {
	return scope == ScopeDefault || markRe.MatchString(scope)
}

// MarkOf возвращает N из "mark:N"; ok=false для default и некорректных значений.
func MarkOf(scope string) (n int, ok bool) {
	m := markRe.FindStringSubmatch(scope)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// normScope приводит scope к каноническому виду ("" = default).
func normScope(scope string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(scope))
	if s == "" {
		s = ScopeDefault
	}
	if !ValidScope(s) {
		return "", fmt.Errorf("%w: lock scope %q", ErrInvalid, scope)
	}
	return s, nil
}

// IsCustomDomain сообщает, что profile — hostname кастомного домена
// (фиксации для них допустимы только для tls).
func IsCustomDomain(profile string) bool { return hostnameRe.MatchString(profile) }

// normProfile проверяет и нормализует (нижний регистр) идентификатор профиля.
func normProfile(profile string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(profile))
	if p == "" || strings.ContainsAny(p, "\t\r\n") {
		return "", fmt.Errorf("%w: profile %q", ErrInvalid, profile)
	}
	return p, nil
}

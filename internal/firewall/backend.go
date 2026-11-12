package firewall

import (
	"errors"
	"fmt"
	"strings"
)

// Backend — используемый межсетевой экран.
type Backend string

// Поддерживаемые backend-ы.
const (
	BackendNone     Backend = ""
	BackendIptables Backend = "iptables"
	BackendNftables Backend = "nftables"
)

// Ошибки выбора backend-а.
var (
	// ErrNoBackend — ни iptables, ни nft не найдены (или backend не задан).
	ErrNoBackend = errors.New("firewall: no iptables or nft backend available")
	// ErrBackendUnavailable — заданный в конфигурации backend не установлен.
	ErrBackendUnavailable = errors.New("firewall: configured backend binary not found")
)

// ParseBackend разбирает значение FWTYPE ("iptables", "nftables", "nft").
// Для пустого/неизвестного значения возвращает (BackendNone, false).
func ParseBackend(s string) (Backend, bool) {
	switch strings.ToLower(strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"'`))) {
	case "iptables":
		return BackendIptables, true
	case "nftables", "nft":
		return BackendNftables, true
	}
	return BackendNone, false
}

// Binary возвращает имя исполняемого файла backend-а.
func (b Backend) Binary() string {
	switch b {
	case BackendIptables:
		return "iptables"
	case BackendNftables:
		return "nft"
	}
	return ""
}

// String реализует fmt.Stringer.
func (b Backend) String() string { return string(b) }

// DetectBackend определяет backend. Приоритет:
//  1. явное значение configured (FWTYPE из config zapret2) — при условии, что
//     соответствующий бинарник установлен, иначе ErrBackendUnavailable;
//  2. наличие бинарников: если есть только один из nft/iptables — он;
//     если оба — nftables при наличии fw4 (OpenWrt), иначе iptables
//     (как platform.detectFW);
//  3. иначе ErrNoBackend.
func DetectBackend(r Runner, configured string) (Backend, error) {
	if b, ok := ParseBackend(configured); ok {
		if _, err := r.LookPath(b.Binary()); err != nil {
			return BackendNone, fmt.Errorf("%w: %s", ErrBackendUnavailable, b.Binary())
		}
		return b, nil
	}
	_, nftErr := r.LookPath("nft")
	_, iptErr := r.LookPath("iptables")
	hasNft, hasIpt := nftErr == nil, iptErr == nil
	switch {
	case hasNft && hasIpt:
		if _, err := r.LookPath("fw4"); err == nil {
			return BackendNftables, nil
		}
		return BackendIptables, nil
	case hasNft:
		return BackendNftables, nil
	case hasIpt:
		return BackendIptables, nil
	}
	return BackendNone, ErrNoBackend
}

// BackendFromGetter определяет backend по FWTYPE из конфигурации и наличию
// бинарников.
func BackendFromGetter(r Runner, get Getter) (Backend, error) {
	return DetectBackend(r, getTrim(get, "FWTYPE"))
}

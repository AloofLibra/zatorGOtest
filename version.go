// Package zator — корневой пакет модуля; хранит версию из файла VERSION.
package zator

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var versionRaw string

// versionOverride задаётся через -ldflags "-X github.com/AloofLibra/zator.versionOverride=..."
var versionOverride string

// Version возвращает версию zator (ldflags override или файл VERSION).
func Version() string {
	if v := strings.TrimSpace(versionOverride); v != "" {
		return v
	}
	v := strings.TrimSpace(versionRaw)
	if v == "" {
		return "unknown"
	}
	return v
}

// Package assets содержит встроенные в бинарник статические файлы zator.
//
// go:embed не допускает пути с "..", поэтому директивы embed расположены
// здесь, рядом с самими файлами; пакет internal/uiembed предоставляет
// удобные обёртки поверх этих переменных.
package assets

import "embed"

// WebUI — встроенные файлы веб-интерфейса (assets/webui).
//
//go:embed all:webui
var WebUI embed.FS

// ConfigDefault — шаблон конфигурации zapret по умолчанию (assets/config.default).
//
//go:embed config.default
var ConfigDefault []byte

// Lua — Lua-модули zator для nfqws2.
//
//go:embed all:lua
var Lua embed.FS

// Orchestra — Lua orchestra (locked.lua и связанные).
//
//go:embed all:orchestra
var Orchestra embed.FS

// Lists — списки доменов/IP (без огромных внешних дампов).
//
//go:embed all:lists
var Lists embed.FS

// Data — вспомогательные данные (провайдеры, seed и т.п.).
//
//go:embed all:data
var Data embed.FS

// Fake — TLS/QUIC/WG fake-блобы для nfqws2.
//
//go:embed all:fake
var Fake embed.FS

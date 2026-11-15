// Package assets содержит встроенные в бинарник статические файлы zator.
package assets

import "embed"

//go:embed all:webui
var WebUI embed.FS

//go:embed config.default
var ConfigDefault []byte

//go:embed all:lua
var Lua embed.FS

//go:embed all:orchestra
var Orchestra embed.FS

//go:embed all:lists
var Lists embed.FS

//go:embed all:data
var Data embed.FS

//go:embed all:files
var Files embed.FS

//go:embed all:extra_strats
var ExtraStrats embed.FS

//go:embed all:blockcheck2
var Blockcheck2 embed.FS

//go:embed all:init
var Init embed.FS

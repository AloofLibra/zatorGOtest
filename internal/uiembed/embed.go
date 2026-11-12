// Package uiembed предоставляет доступ к встроенным веб-ресурсам zator
// (Web UI и config.default). Сами директивы go:embed находятся в пакете
// assets, так как embed не поддерживает пути с "..".
package uiembed

import (
	"errors"
	"io/fs"
	"testing/fstest"

	"github.com/AloofLibra/zator/assets"
)

const webUIRoot = "webui"

// stubIndex — страница-заглушка, если веб-интерфейс не встроен.
const stubIndex = `<!doctype html><html><head><meta charset="utf-8"><title>zator</title></head>` +
	`<body><h1>zator</h1><p>Web UI не встроен в этот бинарник.</p></body></html>`

// WebUI возвращает файловую систему с корнем в assets/webui.
// Если встроенных файлов нет, возвращается FS-заглушка с минимальным index.html.
func WebUI() fs.FS {
	sub, err := fs.Sub(assets.WebUI, webUIRoot)
	if err != nil {
		return StubFS()
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return StubFS()
	}
	return sub
}

// StubFS возвращает минимальную FS-заглушку для Web UI.
func StubFS() fs.FS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(stubIndex)},
	}
}

// HasWebUI сообщает, встроен ли настоящий Web UI (а не заглушка).
func HasWebUI() bool {
	_, err := fs.Stat(assets.WebUI, webUIRoot+"/index.html")
	return err == nil
}

// ConfigDefault возвращает содержимое встроенного config.default.
func ConfigDefault() ([]byte, error) {
	if len(assets.ConfigDefault) == 0 {
		return nil, errors.New("uiembed: config.default не встроен")
	}
	out := make([]byte, len(assets.ConfigDefault))
	copy(out, assets.ConfigDefault)
	return out, nil
}

// Lua возвращает FS с Lua-модулями zator (корень — содержимое assets/lua).
func Lua() fs.FS { return mustSub(assets.Lua, "lua") }

// Orchestra возвращает FS orchestra (locked.lua и т.п.).
func Orchestra() fs.FS { return mustSub(assets.Orchestra, "orchestra") }

// Lists возвращает FS списков доменов/IP.
func Lists() fs.FS { return mustSub(assets.Lists, "lists") }

// Data возвращает FS вспомогательных данных.
func Data() fs.FS { return mustSub(assets.Data, "data") }

// Fake возвращает FS fake-блобов.
func Fake() fs.FS { return mustSub(assets.Fake, "fake") }

func mustSub(root fs.FS, name string) fs.FS {
	sub, err := fs.Sub(root, name)
	if err != nil {
		return fstest.MapFS{}
	}
	return sub
}

// Package uiembed предоставляет доступ к встроенным ресурсам zator.
package uiembed

import (
	"errors"
	"io/fs"
	"testing/fstest"

	"github.com/AloofLibra/zator/assets"
)

const webUIRoot = "webui"

const stubIndex = `<!doctype html><html><head><meta charset="utf-8"><title>zator</title></head>` +
	`<body><h1>zator</h1><p>Web UI не встроен в этот бинарник.</p></body></html>`

// WebUI возвращает FS веб-интерфейса.
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

// StubFS — минимальная заглушка Web UI.
func StubFS() fs.FS {
	return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(stubIndex)}}
}

// HasWebUI сообщает, встроен ли настоящий Web UI.
func HasWebUI() bool {
	_, err := fs.Stat(assets.WebUI, webUIRoot+"/index.html")
	return err == nil
}

// ConfigDefault возвращает шаблон config.default.
func ConfigDefault() ([]byte, error) {
	if len(assets.ConfigDefault) == 0 {
		return nil, errors.New("uiembed: config.default не встроен")
	}
	out := make([]byte, len(assets.ConfigDefault))
	copy(out, assets.ConfigDefault)
	return out, nil
}

func mustSub(root fs.FS, name string) fs.FS {
	sub, err := fs.Sub(root, name)
	if err != nil {
		return fstest.MapFS{}
	}
	return sub
}

func Lua() fs.FS         { return mustSub(assets.Lua, "lua") }
func Orchestra() fs.FS   { return mustSub(assets.Orchestra, "orchestra") }
func Lists() fs.FS       { return mustSub(assets.Lists, "lists") }
func Data() fs.FS        { return mustSub(assets.Data, "data") }
func Files() fs.FS       { return mustSub(assets.Files, "files") }
func ExtraStrats() fs.FS { return mustSub(assets.ExtraStrats, "extra_strats") }
func Blockcheck2() fs.FS { return mustSub(assets.Blockcheck2, "blockcheck2") }
func Init() fs.FS        { return mustSub(assets.Init, "init") }
func Tools() fs.FS       { return mustSub(assets.Tools, "tools") }

// Fake — совместимость: files/fake.
func Fake() fs.FS {
	sub, err := fs.Sub(assets.Files, "files/fake")
	if err != nil {
		return fstest.MapFS{}
	}
	return sub
}

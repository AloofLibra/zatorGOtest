// Package uiembed provides access to resources installed beside the zator binary.
// The historical name remains for API compatibility; resources are no longer
// embedded in the executable.
package uiembed

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing/fstest"

	"github.com/AloofLibra/zator/internal/paths"
)

const stubIndex = `<!doctype html><html><head><meta charset="utf-8"><title>zator</title></head><body><h1>zator</h1><p>Web UI не установлен.</p></body></html>`

func sourceAssets() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "assets"))
}

// SourceAssetsRoot is a development-only fallback for running from a checkout.
// Release binaries use the separately installed resource archives.
func SourceAssetsRoot() string { return sourceAssets() }

func dirFS(root string) fs.FS {
	if st, err := os.Stat(root); err == nil && st.IsDir() {
		return os.DirFS(root)
	}
	return fstest.MapFS{}
}

func sourceOr(root, rel string) string {
	if _, err := os.Stat(root); err == nil {
		return root
	}
	return filepath.Join(sourceAssets(), rel)
}

func WebUIAt(p paths.Paths) fs.FS {
	root := p.WebUIDir
	if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil {
		root = filepath.Join(sourceAssets(), "webui")
	}
	if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil {
		return StubFS()
	}
	return dirFS(root)
}
func WebUI() fs.FS   { return WebUIAt(paths.Default()) }
func StubFS() fs.FS  { return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(stubIndex)}} }
func HasWebUI() bool { _, err := fs.Stat(WebUI(), "index.html"); return err == nil }

func ConfigDefaultAt(p paths.Paths) ([]byte, error) {
	for _, name := range []string{filepath.Join(p.ConfigDir, "config.default"), filepath.Join(p.ZapretRoot, "config.default"), filepath.Join(sourceAssets(), "config.default")} {
		if b, err := os.ReadFile(name); err == nil && len(b) > 0 {
			return b, nil
		}
	}
	return nil, errors.New("uiembed: config.default не установлен")
}
func ConfigDefault() ([]byte, error) { return ConfigDefaultAt(paths.Default()) }

func LuaAt(p paths.Paths) fs.FS { return dirFS(sourceOr(p.LuaDir, "lua")) }
func OrchestraAt(p paths.Paths) fs.FS {
	return dirFS(sourceOr(filepath.Join(p.ExtraStratsDir, "cache", "orchestra"), "orchestra"))
}
func ListsAt(p paths.Paths) fs.FS { return dirFS(sourceOr(p.ListsDir, "lists")) }
func DataAt(p paths.Paths) fs.FS  { return dirFS(sourceOr(p.DataDir, "data")) }
func FilesAt(p paths.Paths) fs.FS {
	return dirFS(sourceOr(filepath.Join(p.ZatorRoot, "files"), "files"))
}
func ExtraStratsAt(p paths.Paths) fs.FS { return dirFS(sourceOr(p.ExtraStratsDir, "extra_strats")) }
func Blockcheck2At(p paths.Paths) fs.FS {
	return dirFS(sourceOr(filepath.Join(p.ZapretRoot, "blockcheck2.d"), "blockcheck2"))
}
func InitAt(p paths.Paths) fs.FS {
	root := filepath.Join(p.ZatorRoot, "init")
	if _, err := os.Stat(root); err != nil {
		root = filepath.Join(sourceAssets(), "init")
	}
	return dirFS(root)
}
func ToolsAt(p paths.Paths) fs.FS {
	return dirFS(sourceOr(filepath.Join(p.ZatorRoot, "tools"), "tools"))
}

func Lua() fs.FS         { return LuaAt(paths.Default()) }
func Orchestra() fs.FS   { return OrchestraAt(paths.Default()) }
func Lists() fs.FS       { return ListsAt(paths.Default()) }
func Data() fs.FS        { return DataAt(paths.Default()) }
func Files() fs.FS       { return FilesAt(paths.Default()) }
func ExtraStrats() fs.FS { return ExtraStratsAt(paths.Default()) }
func Blockcheck2() fs.FS { return Blockcheck2At(paths.Default()) }
func Init() fs.FS        { return InitAt(paths.Default()) }
func Tools() fs.FS       { return ToolsAt(paths.Default()) }
func Fake() fs.FS        { return dirFS(paths.Default().FakeDir) }

package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"

	"github.com/AloofLibra/zator/internal/uiembed"
)

func TestInstallGooglevideoSniffer(t *testing.T) {
	root := t.TempDir()
	assets := fstest.MapFS{"bin/googlevideo-dns-sniffer-arm64": &fstest.MapFile{Data: []byte("sniffer")}}
	if err := installGooglevideoSniffer(root, assets, "arm64"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "tools", "bin", "googlevideo-dns-sniffer")
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "sniffer" {
		t.Fatalf("installed sniffer = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != 0o755 {
			t.Fatalf("sniffer permissions = %v, %v", st, err)
		}
	}

	if err := installGooglevideoSniffer(root, fstest.MapFS{}, "mipsle"); err != nil {
		t.Fatalf("missing optional sniffer: %v", err)
	}
}

func TestInstallEmbeddedGooglevideoSniffer(t *testing.T) {
	tools := uiembed.Tools()
	asset := filepath.ToSlash(filepath.Join("bin", "googlevideo-dns-sniffer-"+runtime.GOARCH))
	if _, err := fs.Stat(tools, asset); errors.Is(err, fs.ErrNotExist) {
		t.Skipf("release sniffer asset not present in this build")
	} else if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := installGooglevideoSniffer(root, tools, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "tools", "bin", "googlevideo-dns-sniffer"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 4 || string(data[:4]) != "\x7fELF" {
		t.Fatalf("installed embedded helper is not ELF: %d bytes", len(data))
	}
}

package app

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/AloofLibra/zator/internal/fsutil"
)

func installGooglevideoSniffer(root string, tools fs.FS, arch string) error {
	data, err := fs.ReadFile(tools, filepath.ToSlash(filepath.Join("bin", "googlevideo-dns-sniffer-"+arch)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("embedded Googlevideo DNS sniffer for %s is empty", arch)
	}
	return fsutil.AtomicWriteFile(filepath.Join(root, "tools", "bin", "googlevideo-dns-sniffer"), data, 0o755)
}

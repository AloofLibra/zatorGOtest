package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/AloofLibra/zator/internal/app"
)

const maxCLIErrorLogBytes = 256 << 10

func cmdErrors(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "использование: zator errors")
		return 2
	}

	a, appErr := app.Load()
	var warnings []string
	if appErr == nil {
		warnings = a.ServiceWarnings()
	}

	if len(warnings) > 0 {
		fmt.Fprintln(stdout, "Предупреждения служб:")
		for _, warning := range warnings {
			fmt.Fprintln(stdout, warning)
		}
	}

	if writeErrorLog(errorLogCandidates(os.TempDir()), stdout) {
		return 0
	}

	if appErr != nil {
		fmt.Fprintf(stderr, "ошибка конфигурации при чтении предупреждений служб: %v\n", appErr)
		return 1
	}
	if len(warnings) == 0 {
		fmt.Fprintln(stdout, "Предупреждений служб и журнала ошибок nfqws2 нет")
	} else {
		fmt.Fprintln(stdout, "Журнал ошибок nfqws2 недоступен")
	}
	return 0
}

func writeErrorLog(candidates []string, stdout io.Writer) bool {
	for _, path := range candidates {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			continue
		}
		if info.Size() > maxCLIErrorLogBytes {
			if _, err := file.Seek(-maxCLIErrorLogBytes, io.SeekEnd); err != nil {
				_ = file.Close()
				continue
			}
		}
		data, err := io.ReadAll(io.LimitReader(file, maxCLIErrorLogBytes))
		_ = file.Close()
		if err != nil {
			continue
		}
		fmt.Fprintf(stdout, "Журнал ошибок nfqws2 (%s):\n", path)
		_, _ = stdout.Write(data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			fmt.Fprintln(stdout)
		}
		return true
	}
	return false
}

func errorLogCandidates(tempDir string) []string {
	paths := []string{"/tmp/nfqws2_1.err", "/opt/tmp/nfqws2_1.err"}
	if tempDir != "" {
		candidate := filepath.Join(tempDir, "nfqws2_1.err")
		for _, path := range paths {
			if path == candidate {
				return paths
			}
		}
		paths = append(paths, candidate)
	}
	return paths
}

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/AloofLibra/zator/internal/app"
	"github.com/AloofLibra/zator/internal/config"
)

var execReplaceFn = execReplaceProcess

func cmdExecNfqws2(args []string, stderr io.Writer) int {
	var bin, argsFile string
	switch len(args) {
	case 0:
		a, err := app.Load()
		if err != nil {
			fmt.Fprintf(stderr, "exec-nfqws2: ошибка конфигурации: %v\n", err)
			return 1
		}
		bin = filepath.Join(a.Paths.ZapretRoot, "nfq2", "nfqws2")
		argsFile = filepath.Join(a.Paths.ZapretRoot, "nfqws2.args")
	case 2:
		bin = args[0]
		argsFile = args[1]
	default:
		fmt.Fprintln(stderr, "использование: zator exec-nfqws2 [<nfqws2-bin> <nfqws2.args>]")
		return 2
	}

	var err error
	bin, err = validateExecAbsPath("nfqws2", bin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	argsFile, err = validateExecAbsPath("nfqws2.args", argsFile)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	info, err := os.Stat(bin)
	if err != nil {
		fmt.Fprintf(stderr, "exec-nfqws2: %s: %v\n", bin, err)
		return 1
	}
	if !info.Mode().IsRegular() {
		fmt.Fprintf(stderr, "exec-nfqws2: %s не является обычным файлом\n", bin)
		return 1
	}

	nfqArgs, err := config.ReadNfqws2ArgsFile(argsFile)
	if err != nil {
		fmt.Fprintf(stderr, "exec-nfqws2: чтение %s: %v\n", argsFile, err)
		return 1
	}
	if err := config.ValidateNfqws2Args(nfqArgs); err != nil {
		fmt.Fprintf(stderr, "exec-nfqws2: %s: %v\n", argsFile, err)
		return 1
	}

	argv := make([]string, 0, len(nfqArgs)+1)
	argv = append(argv, bin)
	argv = append(argv, nfqArgs...)
	if err := execReplaceFn(bin, argv); err != nil {
		fmt.Fprintf(stderr, "exec-nfqws2: запуск %s: %v\n", bin, err)
		return 1
	}
	return 0
}

func validateExecAbsPath(label, p string) (string, error) {
	if p == "" || strings.ContainsRune(p, '\x00') || !filepath.IsAbs(p) {
		return "", fmt.Errorf("exec-nfqws2: %s должен быть корректным абсолютным путём: %q", label, p)
	}
	return filepath.Clean(p), nil
}

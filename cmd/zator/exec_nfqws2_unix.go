//go:build unix

package main

import (
	"os"
	"syscall"
)

func execReplaceProcess(bin string, argv []string) error {
	return syscall.Exec(bin, argv, os.Environ())
}

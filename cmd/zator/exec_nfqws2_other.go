//go:build !unix

package main

import (
	"os"
	"os/exec"
)

func execReplaceProcess(bin string, argv []string) error {
	var args []string
	if len(argv) > 1 {
		args = argv[1:]
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

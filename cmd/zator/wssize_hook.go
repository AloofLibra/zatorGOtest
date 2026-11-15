package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/AloofLibra/zator/internal/app"
)

func cmdWssizeHook(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || (args[0] != "start" && args[0] != "restart" && args[0] != "stop") {
		fmt.Fprintln(stderr, "использование: zator wssize-hook start|restart|stop")
		return 2
	}
	a, err := app.Load()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if args[0] == "stop" {
		err = a.RemoveWssizeFirewall(ctx)
	} else {
		err = a.ApplyWssizeFirewall(ctx)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

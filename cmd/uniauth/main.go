package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := reportResult(Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr), os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}

func reportResult(err error, out io.Writer) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(out, err)
	return 1
}

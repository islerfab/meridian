// Command meridian is a K8s-native declarative calendar sync engine.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/islerfab/meridian/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	// SIGINT/SIGTERM cancel the context; the reconciliation loop (meridian
	// run) uses this for graceful shutdown between cycles.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.Execute(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "meridian:", err)
		return 1
	}
	return 0
}

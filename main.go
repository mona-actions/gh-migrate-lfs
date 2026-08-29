package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/mona-actions/gh-migrate-lfs/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cmd.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

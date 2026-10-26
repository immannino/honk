package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/immannino/honk/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.New().Run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "honk:", err)
		os.Exit(1)
	}
}

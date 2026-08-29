package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/alx4j/ai4j/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(app.RunContext(ctx, os.Args, os.Stdin, os.Stdout, os.Stderr))
}

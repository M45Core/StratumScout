package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/M45Core/StratumScout/internal/scout"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := scout.Main(ctx); err != nil {
		log.Fatal(err)
	}
}

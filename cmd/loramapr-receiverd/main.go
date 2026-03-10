package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/runtime"
)

func main() {
	configPath := flag.String("config", config.DefaultPath, "path to receiver config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	svc, err := runtime.New(cfg)
	if err != nil {
		log.Fatalf("create runtime: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := svc.Run(ctx); err != nil {
		log.Fatalf("runtime failed: %v", err)
	}
}

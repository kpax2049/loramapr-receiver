package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os/signal"
	"syscall"

	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/logging"
	"github.com/loramapr/loramapr-receiver/internal/runtime"
)

func main() {
	configPath := flag.String("config", config.DefaultPath, "path to receiver config file")
	modeOverride := flag.String("mode", "", "runtime mode override: auto|setup|service")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config failed", "err", err, "config", *configPath)
		return
	}

	if *modeOverride != "" {
		cfg.Service.Mode = config.RunMode(*modeOverride)
		if err := cfg.Validate(); err != nil {
			slog.Error("invalid mode override", "err", err, "mode", *modeOverride)
			return
		}
	}

	logger, err := logging.New(cfg.Logging)
	if err != nil {
		slog.Error("initialize logger failed", "err", err)
		return
	}

	svc, err := runtime.New(cfg, logger)
	if err != nil {
		logger.Error("create runtime failed", "err", err)
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := svc.Run(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("runtime canceled")
			return
		}
		logger.Error("runtime failed", "err", err)
		return
	}

	logger.Info("runtime stopped")
}

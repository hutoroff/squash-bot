package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vkhutorov/squash_bot/internal/booking"
	"github.com/vkhutorov/squash_bot/internal/config"
	"github.com/vkhutorov/squash_bot/internal/eversports"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
var Version = "dev"

func main() {
	cfg, err := config.LoadBooking()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	logLevel := slog.LevelInfo
	if cfg.LogLevel == "DEBUG" {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	if _, err := loadTimezone(cfg.Timezone); err != nil {
		slog.Error("load timezone", "timezone", cfg.Timezone, "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	esClient := eversports.New(cfg.EversportsEmail, cfg.EversportsPassword, logger)

	h := booking.NewHandler(esClient, logger, Version, cfg.EversportsFacilityID, cfg.EversportsFacilityUUID, cfg.EversportsSportUUID, cfg.EversportsFacilitySlug, cfg.EversportsSportID, cfg.EversportsSportSlug, cfg.EversportsSportName)
	srv := booking.NewServer(":"+cfg.ServerPort, h, cfg.InternalAPISecret)

	slog.Info("sports-booking-service starting", "port", cfg.ServerPort, "version", Version)
	if err := booking.Run(ctx, srv, logger); err != nil {
		slog.Error("HTTP server error", "err", err)
		os.Exit(1)
	}
	slog.Info("sports-booking-service stopped")
}

func loadTimezone(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q: %w", name, err)
	}
	return loc, nil
}

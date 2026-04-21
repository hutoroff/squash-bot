package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/booking"
	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
	"github.com/hutoroff/squash-bot/internal/config"
	"gopkg.in/natefinch/lumberjack.v2"
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
	out := io.Writer(os.Stdout)
	if logDir := os.Getenv("LOG_DIR"); logDir != "" {
		out = io.MultiWriter(os.Stdout, &lumberjack.Logger{
			Filename:   logDir + "/app.log",
			MaxSize:    10,
			MaxBackups: 5,
			Compress:   true,
		})
	}
	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	loc, err := loadTimezone(cfg.Timezone)
	if err != nil {
		slog.Error("load timezone", "timezone", cfg.Timezone, "err", err)
		os.Exit(1)
	}
	time.Local = loc

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	esClient := eversports.New(cfg.EversportsEmail, cfg.EversportsPassword, logger)

	h := booking.NewHandler(esClient, logger, Version, cfg.EversportsFacilityID, cfg.EversportsFacilityUUID, cfg.EversportsFacilitySlug)
	srv := booking.NewServer(":"+cfg.ServerPort, h, cfg.InternalAPISecret)

	slog.Info("booking starting", "port", cfg.ServerPort, "version", Version)
	if err := booking.Run(ctx, srv, logger); err != nil {
		slog.Error("HTTP server error", "err", err)
		os.Exit(1)
	}
	slog.Info("booking stopped")
}

func loadTimezone(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q: %w", name, err)
	}
	return loc, nil
}

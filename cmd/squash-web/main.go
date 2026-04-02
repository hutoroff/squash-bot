package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vkhutorov/squash_bot/internal/config"
	"github.com/vkhutorov/squash_bot/internal/webserver"
	squashweb "github.com/vkhutorov/squash_bot/web"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
var Version = "dev"

func main() {
	cfg, err := config.LoadWeb()
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

	loc, err := loadTimezone(cfg.Timezone)
	if err != nil {
		slog.Error("load timezone", "timezone", cfg.Timezone, "err", err)
		os.Exit(1)
	}
	time.Local = loc

	distFS, err := fs.Sub(squashweb.FS, "frontend/dist")
	if err != nil {
		slog.Error("sub frontend/dist", "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	auth := webserver.NewAuthHandler(
		cfg.TelegramBotToken,
		cfg.TelegramBotName,
		cfg.JWTSecret,
		cfg.ManagementServiceURL,
		cfg.InternalAPISecret,
		logger,
	)
	games := webserver.NewGamesHandler(auth, cfg.ManagementServiceURL, cfg.InternalAPISecret)
	h := webserver.NewHandler(distFS, Version, logger, auth, games)
	srv := webserver.NewServer(":"+cfg.ServerPort, h)

	slog.Info("squash-web starting", "port", cfg.ServerPort, "version", Version)
	if err := webserver.Run(ctx, srv, logger); err != nil {
		slog.Error("HTTP server error", "err", err)
		os.Exit(1)
	}
	slog.Info("squash-web stopped")
}

func loadTimezone(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q: %w", name, err)
	}
	return loc, nil
}

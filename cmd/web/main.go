package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hutoroff/squash-bot/cmd/web/webserver"
	"github.com/hutoroff/squash-bot/internal/config"
	squashweb "github.com/hutoroff/squash-bot/web"
	"gopkg.in/natefinch/lumberjack.v2"
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
	audit := webserver.NewAuditHandler(auth, cfg.ManagementServiceURL, cfg.InternalAPISecret)
	h := webserver.NewHandler(distFS, Version, logger, auth, games, audit)
	srv := webserver.NewServer(":"+cfg.ServerPort, h)

	slog.Info("web starting", "port", cfg.ServerPort, "version", Version)
	if err := webserver.Run(ctx, srv, logger); err != nil {
		slog.Error("HTTP server error", "err", err)
		os.Exit(1)
	}
	slog.Info("web stopped")
}

func loadTimezone(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q: %w", name, err)
	}
	return loc, nil
}

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/cmd/telegram/client"
	"github.com/hutoroff/squash-bot/cmd/telegram/telegram"
	"github.com/hutoroff/squash-bot/internal/config"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
var Version = "dev"

func main() {
	cfg, err := config.LoadTelegram()
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

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	tgAPI, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		slog.Error("create bot API", "err", err)
		os.Exit(1)
	}
	logger.Info("authorized on account", "username", tgAPI.Self.UserName)

	mgmtClient := client.New(cfg.ManagementServiceURL, cfg.InternalAPISecret)

	mgmtVersion, err := mgmtClient.GetVersion(ctx)
	if err != nil {
		slog.Error("check management service version", "err", err)
		os.Exit(1)
	}
	if majorPart(Version) != majorPart(mgmtVersion) {
		slog.Error("management service major version mismatch",
			"bot_version", Version, "management_version", mgmtVersion)
		os.Exit(1)
	}
	slog.Info("version compatibility check passed", "bot", Version, "management", mgmtVersion)

	bot := telegram.New(tgAPI, loc, mgmtClient, cfg.ServiceAdminIDs, logger)

	slog.Info("telegram starting", "version", Version)
	bot.Start(ctx)
	slog.Info("telegram stopped")
}

func majorPart(v string) string {
	if i := strings.IndexByte(v, '.'); i >= 0 {
		return v[:i]
	}
	return v
}

func loadTimezone(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q: %w", name, err)
	}
	return loc, nil
}

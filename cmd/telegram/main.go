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
	"github.com/google/uuid"
	"github.com/hutoroff/squash-bot/cmd/telegram/client"
	"github.com/hutoroff/squash-bot/cmd/telegram/telegram"
	"github.com/hutoroff/squash-bot/internal/config"
	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/hutoroff/squash-bot/internal/observability"
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

	bot := telegram.New(tgAPI, loc, mgmtClient, logger)
	apiEndpoint := ""
	if cfg.WebhookURL != "" {
		apiEndpoint = "http://telegram:" + cfg.ServerPort
	}
	adminSrv, err := observability.NewAdminServer(observability.AdminConfig{
		Addr: ":" + cfg.AdminServerPort, Token: cfg.MonitoringToken, Version: Version,
		Descriptor: discovery.Descriptor{
			SchemaVersion: discovery.SchemaVersion, InstanceID: uuid.NewString(), ServiceKey: cfg.ServiceKey,
			ServiceKind: "telegram", Implementation: "squash-telegram", ReleaseVersion: Version,
			Endpoints: discovery.Endpoints{API: apiEndpoint, Admin: "http://telegram:" + cfg.AdminServerPort},
			Contracts: []discovery.Contract{{Name: "telegram-transport", Major: 1, Minor: 0}}, State: discovery.StateReady,
		},
		Readiness: func(context.Context) observability.Readiness {
			return observability.Readiness{Ready: true, Checks: map[string]bool{"transport": true}}
		},
	})
	if err != nil {
		slog.Error("configure telegram admin listener", "err", err)
		os.Exit(1)
	}

	slog.Info("telegram starting", "admin_port", cfg.AdminServerPort, "version", Version)
	errCh := make(chan error, 2)
	go func() { errCh <- observability.Run(ctx, adminSrv, logger) }()
	go func() {
		if cfg.WebhookURL != "" {
			opts := telegram.WebhookOptions{PublicURL: cfg.WebhookURL, ListenPort: cfg.ServerPort, Secret: cfg.WebhookSecret}
			if runErr := bot.StartWebhook(ctx, opts); runErr != nil && ctx.Err() == nil {
				slog.Warn("webhook mode unavailable, falling back to long-polling", "err", runErr)
				bot.Start(ctx)
			}
		} else {
			bot.Start(ctx)
		}
		errCh <- nil
	}()
	if err := <-errCh; err != nil {
		slog.Error("telegram listener stopped", "err", err)
		cancel()
		<-errCh
		os.Exit(1)
	}
	cancel()
	if err := <-errCh; err != nil {
		slog.Error("telegram listener shutdown", "err", err)
	}
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

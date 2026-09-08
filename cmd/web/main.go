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

	"github.com/google/uuid"
	"github.com/hutoroff/squash-bot/cmd/web/webserver"
	"github.com/hutoroff/squash-bot/internal/config"
	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/hutoroff/squash-bot/internal/observability"
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
	groups := webserver.NewGroupsHandler(auth, cfg.ManagementServiceURL, cfg.InternalAPISecret)
	venues := webserver.NewVenuesHandler(auth, cfg.ManagementServiceURL, cfg.InternalAPISecret)
	prefs := webserver.NewPrefsHandler(auth, cfg.ManagementServiceURL, cfg.InternalAPISecret)
	users := webserver.NewUsersHandler(auth, cfg.ManagementServiceURL, cfg.InternalAPISecret)
	h := webserver.NewHandler(distFS, Version, logger, auth, games, audit, groups, venues, prefs, users)
	h.SetFeatureFlags(webserver.NewFeatureFlagsHandler(auth, cfg.ManagementServiceURL, cfg.InternalAPISecret))
	srv := webserver.NewServer(":"+cfg.ServerPort, h)
	adminSrv, err := observability.NewAdminServer(observability.AdminConfig{
		Addr: ":" + cfg.AdminServerPort, Token: cfg.MonitoringToken, Version: Version,
		Descriptor: discovery.Descriptor{
			SchemaVersion: discovery.SchemaVersion, InstanceID: uuid.NewString(), ServiceKey: cfg.ServiceKey,
			ServiceKind: "web", Implementation: "squash-web", ReleaseVersion: Version,
			Endpoints: discovery.Endpoints{API: "http://web:" + cfg.ServerPort, Admin: "http://web:" + cfg.AdminServerPort},
			Contracts: []discovery.Contract{{Name: "web-api", Major: 1, Minor: 0}}, State: discovery.StateReady,
		},
		Readiness: func(context.Context) observability.Readiness {
			return observability.Readiness{Ready: true, Checks: map[string]bool{"listener": true}}
		},
	})
	if err != nil {
		slog.Error("configure web admin listener", "err", err)
		os.Exit(1)
	}

	slog.Info("web starting", "port", cfg.ServerPort, "admin_port", cfg.AdminServerPort, "version", Version)
	errCh := make(chan error, 2)
	go func() { errCh <- webserver.Run(ctx, srv, logger) }()
	go func() { errCh <- observability.Run(ctx, adminSrv, logger) }()
	if err := <-errCh; err != nil {
		slog.Error("web listener stopped", "err", err)
		cancel()
		<-errCh
		os.Exit(1)
	}
	cancel()
	if err := <-errCh; err != nil {
		slog.Error("web listener shutdown", "err", err)
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

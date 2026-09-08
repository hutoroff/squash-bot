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

	managementTarget := webserver.NewStaticManagementTarget(cfg.ManagementServiceURL)
	if cfg.DiscoveryMode == config.DiscoveryModeRegistry {
		resolver, resolverErr := discovery.NewRegistryResolver(cfg.DiscoveryURL, cfg.InternalAPISecret)
		if resolverErr != nil {
			slog.Error("configure management discovery", "err", resolverErr)
			os.Exit(1)
		}
		managementTarget = webserver.NewDiscoveredManagementTarget(resolver, discovery.Requirement{ServiceKey: cfg.ManagementServiceKey, Contract: "management-api", Major: 1, RequiredCapabilities: []string{"management.api"}})
	}
	auth := webserver.NewAuthHandlerWithTarget(cfg.TelegramBotToken, cfg.TelegramBotName, cfg.JWTSecret, managementTarget, cfg.InternalAPISecret, logger)
	games := webserver.NewGamesHandlerWithTarget(auth, managementTarget, cfg.InternalAPISecret)
	audit := webserver.NewAuditHandlerWithTarget(auth, managementTarget, cfg.InternalAPISecret)
	groups := webserver.NewGroupsHandlerWithTarget(auth, managementTarget, cfg.InternalAPISecret)
	venues := webserver.NewVenuesHandlerWithTarget(auth, managementTarget, cfg.InternalAPISecret)
	prefs := webserver.NewPrefsHandlerWithTarget(auth, managementTarget, cfg.InternalAPISecret)
	users := webserver.NewUsersHandlerWithTarget(auth, managementTarget, cfg.InternalAPISecret)
	h := webserver.NewHandler(distFS, Version, logger, auth, games, audit, groups, venues, prefs, users)
	h.SetFeatureFlags(webserver.NewFeatureFlagsHandlerWithTarget(auth, managementTarget, cfg.InternalAPISecret))
	srv := webserver.NewServer(":"+cfg.ServerPort, h)
	descriptor := discovery.Descriptor{
		SchemaVersion: discovery.SchemaVersion, InstanceID: uuid.NewString(), ServiceKey: cfg.ServiceKey,
		ServiceKind: "web", Implementation: "squash-web", ReleaseVersion: Version,
		Endpoints: discovery.Endpoints{API: "http://web:" + cfg.ServerPort, Admin: "http://web:" + cfg.AdminServerPort},
		Contracts: []discovery.Contract{{Name: "web-api", Major: 1, Minor: 0}}, State: discovery.StateReady,
	}
	registryURL := cfg.DiscoveryURL
	if registryURL != "" {
		if host, hostErr := discovery.ResolveAdvertiseHost(cfg.ServiceAdvertiseHost, cfg.DiscoveryNetworkInterface); hostErr != nil {
			logger.Warn("service discovery registration disabled", "err", hostErr)
			registryURL = ""
		} else {
			descriptor.Endpoints = discovery.Endpoints{API: "http://" + host + ":" + cfg.ServerPort, Admin: "http://" + host + ":" + cfg.AdminServerPort}
		}
	}
	localReady := func(context.Context) bool { return true }
	adminSrv, err := observability.NewAdminServer(observability.AdminConfig{
		Addr: ":" + cfg.AdminServerPort, Token: cfg.MonitoringToken, Version: Version, Descriptor: descriptor,
		Readiness: func(context.Context) observability.Readiness {
			return observability.Readiness{Ready: true, Checks: map[string]bool{"listener": true}}
		},
	})
	if err != nil {
		slog.Error("configure web admin listener", "err", err)
		os.Exit(1)
	}

	lifecycle, lifecycleErr := discovery.StartLifecycle(ctx, registryURL, cfg.InternalAPISecret, descriptor, localReady, logger)
	if lifecycleErr != nil {
		logger.Warn("service discovery lifecycle unavailable", "err", lifecycleErr)
	}
	defer discovery.ShutdownLifecycle(lifecycle)

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

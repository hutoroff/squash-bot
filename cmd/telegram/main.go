package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
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

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	adminCtx, cancelAdmin := context.WithCancel(context.Background())
	defer cancelAdmin()
	var transportReady atomic.Bool
	var lifecycleRef atomic.Pointer[discovery.Lifecycle]
	go func() {
		<-signalCtx.Done()
		transportReady.Store(false)
		discovery.BeginDrainLifecycle(lifecycleRef.Load())
		cancel()
	}()

	apiEndpoint := ""
	adminEndpoint := "http://telegram:" + cfg.AdminServerPort
	registryURL := cfg.DiscoveryURL
	if cfg.WebhookURL != "" {
		apiEndpoint = "http://telegram:" + cfg.ServerPort
	}
	if registryURL != "" {
		if host, hostErr := discovery.ResolveAdvertiseHost(cfg.ServiceAdvertiseHost, cfg.DiscoveryNetworkInterface); hostErr != nil {
			logger.Warn("service discovery registration disabled", "err", hostErr)
			registryURL = ""
		} else {
			adminEndpoint = "http://" + host + ":" + cfg.AdminServerPort
			if cfg.WebhookURL != "" {
				apiEndpoint = "http://" + host + ":" + cfg.ServerPort
			}
		}
	}
	descriptor := discovery.Descriptor{
		SchemaVersion: discovery.SchemaVersion, InstanceID: uuid.NewString(), ServiceKey: cfg.ServiceKey,
		ServiceKind: "telegram", Implementation: "squash-telegram", ReleaseVersion: Version,
		Endpoints: discovery.Endpoints{API: apiEndpoint, Admin: adminEndpoint},
		Contracts: []discovery.Contract{{Name: "telegram-transport", Major: 1, Minor: 0}}, State: discovery.StateReady,
	}
	localReady := func(context.Context) bool { return transportReady.Load() }
	adminSrv, err := observability.NewAdminServer(observability.AdminConfig{
		Addr: ":" + cfg.AdminServerPort, Token: cfg.MonitoringToken, Version: Version, Descriptor: descriptor,
		Readiness: func(context.Context) observability.Readiness {
			ready := transportReady.Load()
			return observability.Readiness{Ready: ready, Checks: map[string]bool{"transport": ready}}
		},
	})
	if err != nil {
		slog.Error("configure telegram admin listener", "err", err)
		os.Exit(1)
	}

	slog.Info("telegram starting", "admin_port", cfg.AdminServerPort, "version", Version)
	errCh := make(chan error, 2)
	go func() { errCh <- observability.Run(adminCtx, adminSrv, logger) }()
	lifecycle, lifecycleErr := discovery.StartLifecycle(ctx, registryURL, cfg.InternalAPISecret, descriptor, localReady, logger)
	if lifecycleErr != nil {
		logger.Warn("service discovery lifecycle unavailable", "err", lifecycleErr)
	}
	lifecycleRef.Store(lifecycle)
	defer discovery.ShutdownLifecycle(lifecycle)

	// Bootstrap authorization is deliberately bounded; long polling retains its
	// existing unbounded transport after the bot identity has been verified.
	tgAPI, err := tgbotapi.NewBotAPIWithClient(cfg.TelegramBotToken, tgbotapi.APIEndpoint, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		slog.Error("create bot API", "err", err)
		os.Exit(1)
	}
	tgAPI.Client = &http.Client{}
	logger.Info("authorized on account", "username", tgAPI.Self.UserName)

	mgmtClient := client.New(cfg.ManagementServiceURL, cfg.InternalAPISecret)
	if cfg.DiscoveryMode == config.DiscoveryModeRegistry {
		resolver, resolverErr := discovery.NewRegistryResolver(cfg.DiscoveryURL, cfg.InternalAPISecret)
		if resolverErr != nil {
			slog.Error("configure management discovery", "err", resolverErr)
			os.Exit(1)
		}
		requirement := discovery.Requirement{ServiceKey: cfg.ManagementServiceKey, Contract: "management-api", Major: 1, RequiredCapabilities: []string{"management.api"}}
		if activateErr := resolver.Activate(ctx, requirement); activateErr != nil {
			slog.Error("activate management discovery", "err", activateErr)
			os.Exit(1)
		}
		mgmtClient = client.NewDiscovered(resolver, requirement, cfg.InternalAPISecret)
	}

	if cfg.DiscoveryMode != config.DiscoveryModeRegistry {
		mgmtVersion, versionErr := mgmtClient.GetVersion(ctx)
		if versionErr != nil {
			slog.Error("check management service version", "err", versionErr)
			os.Exit(1)
		}
		if majorPart(Version) != majorPart(mgmtVersion) {
			slog.Error("management service major version mismatch", "bot_version", Version, "management_version", mgmtVersion)
			os.Exit(1)
		}
		slog.Info("version compatibility check passed", "bot", Version, "management", mgmtVersion)
	}

	bot := telegram.New(tgAPI, loc, mgmtClient, logger)
	if cfg.DiscoveryMode == config.DiscoveryModeRegistry {
		for ctx.Err() == nil {
			checkCtx, checkCancel := context.WithTimeout(ctx, 3*time.Second)
			_, versionErr := mgmtClient.GetVersion(checkCtx)
			checkCancel()
			if versionErr == nil {
				slog.Info("management contract compatibility check passed", "contract", "management-api", "major", 1)
				break
			}
			logger.Warn("waiting for compatible management service", "err", versionErr)
			select {
			case <-ctx.Done():
				break
			case <-time.After(5 * time.Second):
			}
		}
		if ctx.Err() != nil {
			discovery.BeginDrainLifecycle(lifecycle)
			discovery.DeregisterLifecycle(lifecycle)
			cancelAdmin()
			<-errCh
			return
		}
	}
	go func() {
		if cfg.WebhookURL != "" {
			opts := telegram.WebhookOptions{
				PublicURL: cfg.WebhookURL, ListenPort: cfg.ServerPort, Secret: cfg.WebhookSecret,
				OnReady: func() { transportReady.Store(true) },
			}
			if runErr := bot.StartWebhook(ctx, opts); runErr != nil && ctx.Err() == nil {
				slog.Warn("webhook mode unavailable, falling back to long-polling", "err", runErr)
				bot.StartWithReady(ctx, func() { transportReady.Store(true) })
			}
		} else {
			bot.StartWithReady(ctx, func() { transportReady.Store(true) })
		}
		errCh <- nil
	}()
	if err := <-errCh; err != nil {
		slog.Error("telegram listener stopped", "err", err)
		transportReady.Store(false)
		discovery.BeginDrainLifecycle(lifecycle)
		cancel()
		discovery.DeregisterLifecycle(lifecycle)
		cancelAdmin()
		<-errCh
		os.Exit(1)
	}
	transportReady.Store(false)
	discovery.BeginDrainLifecycle(lifecycle)
	cancel()
	discovery.DeregisterLifecycle(lifecycle)
	cancelAdmin()
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

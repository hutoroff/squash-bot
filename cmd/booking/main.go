package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/hutoroff/squash-bot/cmd/booking/booking"
	"github.com/hutoroff/squash-bot/internal/config"
	"github.com/hutoroff/squash-bot/internal/discovery"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
var Version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("booking stopped with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadBooking()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)
	loc, err := loadTimezone(cfg.Timezone)
	if err != nil {
		return err
	}
	time.Local = loc

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	type serverResult struct {
		name string
		err  error
	}
	errCh := make(chan serverResult, 2)
	metrics := booking.NewMetrics()
	health := &runtimeHealthSource{}
	registryURL := cfg.DiscoveryURL
	advertiseHost := "booking"
	if registryURL != "" {
		var hostErr error
		advertiseHost, hostErr = discovery.ResolveAdvertiseHost(cfg.ServiceAdvertiseHost, cfg.DiscoveryNetworkInterface)
		if hostErr != nil {
			logger.Warn("service discovery registration disabled", "err", hostErr)
			registryURL = ""
			advertiseHost = "booking"
		}
	}
	adminConfig := booking.AdminConfig{
		Addr: ":" + cfg.AdminServerPort, Secret: cfg.MonitoringSecret, Version: Version,
		ServiceKey: cfg.ServiceKey, InstanceID: uuid.NewString(),
		APIEndpoint: "http://" + advertiseHost + ":" + cfg.ServerPort, AdminEndpoint: "http://" + advertiseHost + ":" + cfg.AdminServerPort,
		Scope: configuredScope(cfg), Readiness: health.readiness,
		Running: health.running, LedgerHealth: health.ledgerHealth, Metrics: metrics,
	}
	adminSrv, _, err := booking.NewAdminServer(adminConfig)
	if err != nil {
		return fmt.Errorf("configure booking admin server: %w", err)
	}
	go func() { errCh <- serverResult{name: "admin", err: adminSrv.ListenAndServe()} }()
	logger.Info("booking admin listener starting", "admin_port", cfg.AdminServerPort)
	descriptor, err := booking.AdminDescriptor(adminConfig)
	if err != nil {
		return err
	}
	lifecycle, lifecycleErr := discovery.StartLifecycle(ctx, registryURL, cfg.InternalAPISecret, descriptor, func(readyCtx context.Context) bool {
		return health.readiness(readyCtx).Ready
	}, logger)
	if lifecycleErr != nil {
		logger.Warn("service discovery lifecycle unavailable", "err", lifecycleErr)
	}
	defer discovery.ShutdownLifecycle(lifecycle)

	startupCtx, cancelStartup := context.WithTimeout(ctx, 45*time.Second)
	runtime, handler, err := buildReliableRuntime(startupCtx, cfg, logger, metrics)
	cancelStartup()
	if err != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = adminSrv.Shutdown(shutdownCtx)
		shutdownCancel()
		if signalCtx.Err() != nil {
			logger.Info("booking startup cancelled")
			return nil
		}
		return fmt.Errorf("initialize reliable booking runtime: %w", err)
	}
	health.runtime.Store(runtime)
	select {
	case result := <-errCh:
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = runtime.shutdown(shutdownCtx)
		shutdownCancel()
		return fmt.Errorf("%s HTTP server failed during startup: %w", result.name, result.err)
	default:
	}

	go runtime.runRetention(ctx, cfg.RetentionInterval)
	go func() {
		select {
		case <-runtime.guard.Lost():
			logger.Error("booking singleton authority lost; stopping process incarnation")
			cancel()
		case <-ctx.Done():
		}
	}()
	go func() {
		<-ctx.Done()
		runtime.coordinator.CloseAdmission()
	}()

	businessSrv := booking.NewObservedServerWithWriteTimeout(
		":"+cfg.ServerPort, handler, cfg.InternalAPISecret, metrics, cfg.HTTPResponseWait+10*time.Second,
	)
	logger.Info("booking starting", "port", cfg.ServerPort, "admin_port", cfg.AdminServerPort, "version", Version, "reliable_api", true)
	go func() { errCh <- serverResult{name: "business", err: businessSrv.ListenAndServe()} }()

	var runErr error
	select {
	case result := <-errCh:
		if result.err != nil && !errors.Is(result.err, http.ErrServerClosed) {
			runErr = fmt.Errorf("%s HTTP server: %w", result.name, result.err)
		}
		cancel()
	case <-ctx.Done():
	}
	// Exclude this singleton from new discovery resolution before closing
	// admission or the business listener. Keep the admin listener up through
	// tracked-executor draining so its not-ready state remains observable.
	discovery.BeginDrainLifecycle(lifecycle)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	if err := businessSrv.Shutdown(shutdownCtx); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("shut down booking business HTTP server: %w", err))
	}
	if err := runtime.shutdown(shutdownCtx); err != nil {
		runErr = errors.Join(runErr, err)
	}
	discovery.DeregisterLifecycle(lifecycle)
	if err := adminSrv.Shutdown(shutdownCtx); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("shut down booking admin HTTP server: %w", err))
	}
	if runErr != nil {
		return runErr
	}
	logger.Info("booking stopped")
	return nil
}

func newLogger(level string) *slog.Logger {
	logLevel := slog.LevelInfo
	if level == "DEBUG" {
		logLevel = slog.LevelDebug
	}
	out := io.Writer(os.Stdout)
	if logDir := os.Getenv("LOG_DIR"); logDir != "" {
		out = io.MultiWriter(os.Stdout, &lumberjack.Logger{
			Filename: logDir + "/app.log", MaxSize: 10, MaxBackups: 5, Compress: true,
		})
	}
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: logLevel}))
}

func loadTimezone(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q: %w", name, err)
	}
	return loc, nil
}

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/hutoroff/squash-bot/cmd/management/api"
	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/config"
	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/hutoroff/squash-bot/internal/observability"
	"github.com/hutoroff/squash-bot/migrations"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/robfig/cron/v3"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
var Version = "dev"

func main() {
	cfg, err := config.LoadManagement()
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
	// Keep lifecycle health observable while database, migrations and external
	// Telegram initialization run. Readiness remains false until all local
	// requirements and the business listener are available.
	health := &managementHealthSource{}
	var lifecycleRef atomic.Pointer[discovery.Lifecycle]
	go func() {
		<-signalCtx.Done()
		health.draining.Store(true)
		discovery.BeginDrainLifecycle(lifecycleRef.Load())
		cancel()
	}()
	descriptor := discovery.Descriptor{
		SchemaVersion: discovery.SchemaVersion, InstanceID: uuid.NewString(), ServiceKey: cfg.ServiceKey,
		ServiceKind: "management", Implementation: "squash-management", ReleaseVersion: Version,
		Endpoints:    discovery.Endpoints{API: "http://management:" + cfg.ServerPort, Admin: "http://management:" + cfg.AdminServerPort},
		Contracts:    []discovery.Contract{{Name: "management-api", Major: 1, Minor: 0}},
		Capabilities: []discovery.Capability{{Name: "management.api", Contract: "management-api", Major: 1}}, State: discovery.StateReady,
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
	expectedServices, err := discovery.ParseMonitoredServices(cfg.MonitoredServiceKeys)
	if err != nil {
		slog.Error("parse monitored service inventory", "err", err)
		os.Exit(1)
	}
	if cfg.BookingServiceKey != "" {
		present := false
		for _, expected := range expectedServices {
			present = present || expected.ServiceKey == cfg.BookingServiceKey
		}
		if !present {
			expectedServices = append(expectedServices, discovery.MonitoredService{ServiceKey: cfg.BookingServiceKey, Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor, Capabilities: []string{"booking.courts.list", "booking.slots.list", "booking.create", "booking.cancel", "booking.operations.get", "booking.operations.reconcile", "booking.operations.resolve"}, Scope: map[string]string{"facility_id": cfg.BookingFacilityID, "facility_uuid": cfg.BookingFacilityUUID, "facility_slug": cfg.BookingFacilitySlug, "sport": "squash"}})
		}
	}
	var registryRef atomic.Pointer[service.ServiceRegistryService]
	registrationMetrics := observability.NewRegistrationMetrics()
	httpMetrics := observability.NewHTTPMetrics()
	registryCollector := observability.NewRegistryCollector(expectedServices, func(snapshotCtx context.Context) ([]discovery.MonitoringInstance, error) {
		registry := registryRef.Load()
		if registry == nil {
			return nil, fmt.Errorf("service registry not initialized")
		}
		return registry.MonitoringInstances(snapshotCtx)
	})
	adminSrv, err := observability.NewAdminServer(observability.AdminConfig{
		Addr: ":" + cfg.AdminServerPort, Token: cfg.MonitoringToken, Version: Version, Descriptor: descriptor,
		Readiness: health.readiness, Collectors: append([]prometheus.Collector{registryCollector, registrationMetrics}, httpMetrics.Collectors()...),
	})
	if err != nil {
		slog.Error("configure management admin listener", "err", err)
		os.Exit(1)
	}
	errCh := make(chan error, 2)
	go func() { errCh <- observability.Run(adminCtx, adminSrv, logger) }()

	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect to database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	health.pool.Store(pool)

	if err := runMigrations(cfg.DatabaseURL); err != nil {
		slog.Error("run migrations", "err", err)
		os.Exit(1)
	}
	health.migrationsComplete.Store(true)
	slog.Info("migrations applied")

	tgAPI, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		slog.Error("create telegram bot API", "err", err)
		os.Exit(1)
	}
	logger.Info("telegram bot authorized", "username", tgAPI.Self.UserName)

	gameRepo := storage.NewGameRepo(pool)
	playerRepo := storage.NewPlayerRepo(pool)
	participationRepo := storage.NewParticipationRepo(pool)
	guestRepo := storage.NewGuestRepo(pool)
	groupRepo := storage.NewGroupRepo(pool)
	venueRepo := storage.NewVenueRepo(pool)
	venueCredRepo := storage.NewVenueCredentialRepo(pool)
	autoBookingResultRepo := storage.NewAutoBookingResultRepo(pool)
	courtBookingRepo := storage.NewCourtBookingRepo(pool)
	bookingIntentRepo := storage.NewBookingIntentRepo(pool)
	auditEventRepo := storage.NewAuditEventRepo(pool)
	gameResultRepo := storage.NewGameResultRepo(pool)
	playerRatingRepo := storage.NewPlayerRatingRepo(pool)
	ratingChangeRepo := storage.NewRatingChangeRepo(pool)
	userRepo := storage.NewUserRepo(pool)
	registryService, err := service.NewServiceRegistryService(storage.NewServiceRegistryRepo(pool), cfg.DiscoveryAllowedCIDRs)
	if err != nil {
		slog.Error("configure service registry", "err", err)
		os.Exit(1)
	}
	registryRef.Store(registryService)

	auditSvc := service.NewAuditService(auditEventRepo, logger)
	serverOwnerIDs := parseAdminIDs(cfg.ServiceAdminIDs)

	// Grant-only, idempotent bootstrap: SERVICE_ADMIN_IDS seeds server owners
	// in the DB but never revokes. Revocation is web-Users-page-only.
	seedIDs := make([]int64, 0, len(serverOwnerIDs))
	for id := range serverOwnerIDs {
		seedIDs = append(seedIDs, id)
	}
	if err := userRepo.GrantServerOwnersByTelegramID(ctx, seedIDs); err != nil {
		slog.Error("seed server owners", "err", err)
		os.Exit(1)
	}

	venueService := service.NewVenueService(venueRepo, courtBookingRepo)

	var venueCredService *service.VenueCredentialService
	if cfg.CredentialsEncryptionKey != "" {
		enc, err := service.NewEncryptor(cfg.CredentialsEncryptionKey)
		if err != nil {
			slog.Error("init credentials encryptor", "err", err)
			os.Exit(1)
		}
		venueCredService = service.NewVenueCredentialService(venueCredRepo, venueRepo, courtBookingRepo, enc)
		slog.Info("venue credentials encryption enabled")
	} else {
		slog.Info("venue credentials encryption disabled (CREDENTIALS_ENCRYPTION_KEY not set)")
	}
	pollWindow, err := parsePollWindow(cfg.CronPoll)
	if err != nil {
		slog.Error("unsupported CRON_POLL value", "spec", cfg.CronPoll, "err", err)
		os.Exit(1)
	}

	var bookingClient service.BookingServiceClient
	var bookingIntentSvc *service.BookingIntentService
	var reliableBookingExecutor *service.ReliableBookingExecutor
	var reliableBookingRecovery *service.ReliableBookingRecovery
	var reliableBookingNotifier *service.ReliableBookingNotifier
	var bookingScope bookingapi.Scope
	bookingEnabled := cfg.SportsBookingServiceURL != "" || (cfg.DiscoveryMode == config.DiscoveryModeRegistry && cfg.BookingServiceKey != "")
	if bookingEnabled {
		bookingIntentSvc, err = service.NewBookingIntentService(bookingIntentRepo)
		if err != nil {
			slog.Error("init durable booking intent service", "err", err)
			os.Exit(1)
		}
		bookingScope = bookingapi.Scope{
			ServiceKey: cfg.BookingServiceKey, Provider: "eversports", FacilityID: cfg.BookingFacilityID,
			FacilityUUID: cfg.BookingFacilityUUID, FacilitySlug: cfg.BookingFacilitySlug, Sport: "squash",
		}
		var reliableClient service.ReliableBookingServiceClient
		if cfg.DiscoveryMode == config.DiscoveryModeRegistry {
			requirement := discovery.Requirement{
				ServiceKey: cfg.BookingServiceKey, Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor,
				RequiredCapabilities: []string{"booking.courts.list", "booking.slots.list", "booking.create", "booking.cancel", "booking.operations.get", "booking.operations.reconcile", "booking.operations.resolve"},
				Scope:                map[string]string{"facility_id": cfg.BookingFacilityID, "facility_uuid": cfg.BookingFacilityUUID, "facility_slug": cfg.BookingFacilitySlug, "sport": "squash"},
			}
			bookingClient = service.NewDiscoveredReliableBookingReadClient(registryService, requirement, cfg.InternalAPISecret)
			reliableClient = service.NewDiscoveredReliableHTTPBookingClient(registryService, requirement, cfg.InternalAPISecret)
		} else {
			bookingClient = service.NewReliableBookingReadClient(cfg.SportsBookingServiceURL, cfg.InternalAPISecret)
			reliableClient = service.NewReliableHTTPBookingClient(cfg.SportsBookingServiceURL, cfg.InternalAPISecret)
		}
		reliableBookingExecutor, err = service.NewReliableBookingExecutor(
			reliableClient, bookingIntentRepo, bookingScope, cfg.BookingResponseWait, cfg.BookingStatusPoll,
		)
		if err != nil {
			slog.Error("init reliable booking executor", "err", err)
			os.Exit(1)
		}
		reliableBookingRecovery, err = service.NewReliableBookingRecovery(bookingIntentRepo, bookingIntentSvc, reliableBookingExecutor, venueCredService, courtBookingRepo, bookingScope, logger, 100)
		if err != nil {
			slog.Error("init reliable booking recovery", "err", err)
			os.Exit(1)
		}
		reliableBookingNotifier, err = service.NewReliableBookingNotifier(bookingIntentRepo, tgAPI, logger, 100)
		if err != nil {
			slog.Error("init reliable booking notifier", "err", err)
			os.Exit(1)
		}
		slog.Info("keyed v2 booking service enabled", "discovery_mode", cfg.DiscoveryMode, "booking_service", cfg.SportsBookingServiceURL, "service_key", cfg.BookingServiceKey)
	} else {
		slog.Info("booking service disabled; auto-cancellation and auto-booking disabled")
	}

	gameService := service.NewGameService(gameRepo, venueRepo, participationRepo, guestRepo, groupRepo, auditSvc, tgAPI, loc, logger, courtBookingRepo, bookingClient, venueCredService, autoBookingResultRepo, cfg.ResultWindowDays)
	gameService.SetReliableBooking(bookingIntentSvc, reliableBookingExecutor, bookingScope)

	gameNotifier := service.NewGameNotifier(tgAPI, gameRepo, participationRepo, guestRepo, groupRepo, loc, logger)
	gameService.SetNotifier(gameNotifier)
	partService := service.NewParticipationService(playerRepo, participationRepo, guestRepo, gameNotifier)
	gameResultSvc := service.NewGameResultService(pool, gameResultRepo, gameRepo, playerRepo, participationRepo, auditSvc, cfg.ResultWindowDays, userRepo)
	ratingSvc := service.NewRatingService(pool, playerRatingRepo, ratingChangeRepo, groupRepo, auditSvc, logger)
	flagRepo := storage.NewFeatureFlagRepo(pool)
	flagSvc := service.NewFeatureFlagService(flagRepo, userRepo, groupRepo, auditSvc)
	ratingSvc.SetFeatureFlags(flagRepo)
	gameResultSvc.SetRatingService(ratingSvc)

	cancellationJob := service.NewCancellationReminderJob(tgAPI, gameRepo, participationRepo, guestRepo, groupRepo, gameNotifier, bookingClient, courtBookingRepo, autoBookingResultRepo, venueCredService, auditSvc, loc, logger, pollWindow)
	cancellationJob.SetReliableBooking(bookingIntentSvc, reliableBookingExecutor, bookingScope)
	finalCourtCheckJob := service.NewFinalCourtCheckJob(tgAPI, gameRepo, participationRepo, guestRepo, groupRepo, gameNotifier, cancellationJob, loc, logger, pollWindow)
	halfwayCourtCheckJob := service.NewHalfwayCourtCheckJob(tgAPI, gameRepo, participationRepo, guestRepo, groupRepo, gameNotifier, cancellationJob, loc, logger)
	bookingReminderJob := service.NewBookingReminderJob(tgAPI, gameRepo, gameService, groupRepo, venueRepo, autoBookingResultRepo, loc, logger)
	dayAfterJob := service.NewDayAfterCleanupJob(tgAPI, gameRepo, participationRepo, guestRepo, groupRepo, loc, logger, courtBookingRepo)
	autoBookingJob := service.NewAutoBookingJob(tgAPI, groupRepo, venueRepo, gameRepo, bookingClient, venueCredService, autoBookingResultRepo, courtBookingRepo, auditSvc, loc, logger, cfg.CredentialErrorCooldown)
	autoBookingJob.SetReliableBooking(bookingIntentSvc, reliableBookingExecutor, bookingScope)
	autoApproveResultsJob := service.NewAutoApproveResultsJob(tgAPI, pool, gameResultRepo, playerRepo, auditSvc, logger)
	autoApproveResultsJob.SetRatingService(ratingSvc)
	postLeaderboardJob := service.NewPostLeaderboardJob(tgAPI, groupRepo, gameRepo, gameResultRepo, ratingSvc, loc, logger)
	scheduler := service.NewScheduler(logger, cancellationJob, halfwayCourtCheckJob, finalCourtCheckJob, bookingReminderJob, dayAfterJob, autoBookingJob, autoApproveResultsJob, postLeaderboardJob)

	c := cron.New(cron.WithLocation(loc))
	if _, err := c.AddFunc(cfg.CronPoll, scheduler.RunScheduledTasks); err != nil {
		slog.Error("add poll cron", "spec", cfg.CronPoll, "err", err)
		os.Exit(1)
	}
	if reliableBookingRecovery != nil {
		if _, err := c.AddFunc(cfg.CronPoll, func() {
			recoveryCtx, cancel := context.WithTimeout(context.Background(), cfg.BookingResponseWait)
			defer cancel()
			if recoveryErr := reliableBookingRecovery.RunOnce(recoveryCtx); recoveryErr != nil {
				logger.Warn("reliable booking recovery incomplete", "err", recoveryErr)
			}
			if notificationErr := reliableBookingNotifier.RunOnce(recoveryCtx); notificationErr != nil {
				logger.Warn("reliable booking operator notifications incomplete", "err", notificationErr)
			}
		}); err != nil {
			slog.Error("add reliable booking recovery cron", "spec", cfg.CronPoll, "err", err)
			os.Exit(1)
		}
	}
	retentionDays := cfg.AuditRetentionDays
	if _, err := c.AddFunc("0 2 * * *", func() {
		auditSvc.RunRetention(context.Background(), retentionDays)
	}); err != nil {
		slog.Error("add audit retention cron", "err", err)
		os.Exit(1)
	}
	c.Start()
	defer c.Stop()
	slog.Info("cron scheduler started", "poll_interval", cfg.CronPoll)

	stateRepo := storage.NewServiceStateRepo(pool)
	service.AnnounceChangelog(ctx, tgAPI, groupRepo, stateRepo, loc, logger, Version)

	adminResolver := service.NewAdminGroupsResolver(groupRepo, userRepo, tgAPI, logger)
	h := api.NewHandler(gameService, gameResultSvc, ratingSvc, partService, venueService, venueCredService, groupRepo, playerRepo, userRepo, auditSvc, adminResolver, logger, Version, cfg.CredentialErrorCooldown)
	h.SetFeatureFlags(flagSvc)
	h.SetServiceRegistry(registryService)
	h.SetReliableBookingAdmin(bookingIntentSvc, bookingIntentRepo, reliableBookingExecutor, bookingScope)
	srv := api.NewObservedServer(":"+cfg.ServerPort, h, cfg.InternalAPISecret, cfg.MonitoringToken, httpMetrics)
	localReady := func(readyCtx context.Context) bool { return health.readiness(readyCtx).Ready }

	var lifecycle *discovery.Lifecycle
	if registryURL != "" {
		// Management owns the registry service. Register locally instead of
		// routing its own lifecycle traffic through the business HTTP listener.
		lifecycle, err = discovery.StartLifecycleWithRegistrarObserved(ctx, registryService, descriptor, localReady, logger, registrationMetrics)
		if err != nil {
			logger.Warn("service discovery lifecycle unavailable", "err", err)
		}
	}
	lifecycleRef.Store(lifecycle)
	defer discovery.ShutdownLifecycle(lifecycle)

	slog.Info("management starting", "port", cfg.ServerPort, "admin_port", cfg.AdminServerPort, "version", Version)
	go func() {
		errCh <- api.RunWithReady(ctx, srv, logger, func() {
			health.businessListening.Store(true)
		})
	}()
	if err := <-errCh; err != nil {
		slog.Error("management listener stopped", "err", err)
		discovery.BeginDrainLifecycle(lifecycle)
		health.draining.Store(true)
		cancel()
		discovery.DeregisterLifecycle(lifecycle)
		cancelAdmin()
		<-errCh
		os.Exit(1)
	}
	discovery.BeginDrainLifecycle(lifecycle)
	health.draining.Store(true)
	cancel()
	discovery.DeregisterLifecycle(lifecycle)
	cancelAdmin()
	if err := <-errCh; err != nil {
		slog.Error("management listener shutdown", "err", err)
	}
	slog.Info("management stopped")
}

func loadTimezone(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q: %w", name, err)
	}
	return loc, nil
}

// parsePollWindow derives the reminder timing gate from a cron spec.
// Only "*/N * * * *" patterns are supported; the window is N/2 minutes.
// Reject unsupported patterns at startup so misconfigurations are caught early.
func parsePollWindow(spec string) (time.Duration, error) {
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return 0, fmt.Errorf("expected 5 cron fields, got %d in %q", len(fields), spec)
	}
	for _, f := range fields[1:] {
		if f != "*" {
			return 0, fmt.Errorf("only */N * * * * cron patterns are supported (got %q)", spec)
		}
	}
	minField := fields[0]
	if !strings.HasPrefix(minField, "*/") {
		return 0, fmt.Errorf("minute field must be */N (got %q in %q)", minField, spec)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(minField, "*/"))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid poll interval %q in cron spec %q", minField, spec)
	}
	return time.Duration(n) * time.Minute / 2, nil
}

func parseAdminIDs(raw string) map[int64]bool {
	result := map[int64]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			result[id] = true
		}
	}
	return result
}

func runMigrations(databaseURL string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return err
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}

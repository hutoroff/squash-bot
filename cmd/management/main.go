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
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/hutoroff/squash-bot/internal/config"
	inboundhttp "github.com/hutoroff/squash-bot/internal/management/adapters/inbound/http"
	bookingadapter "github.com/hutoroff/squash-bot/internal/management/adapters/outbound/booking"
	cryptoadapter "github.com/hutoroff/squash-bot/internal/management/adapters/outbound/crypto"
	"github.com/hutoroff/squash-bot/internal/management/adapters/outbound/postgres"
	tgadapter "github.com/hutoroff/squash-bot/internal/management/adapters/outbound/telegram"
	"github.com/hutoroff/squash-bot/internal/management/application/audit"
	"github.com/hutoroff/squash-bot/internal/management/application/changelog"
	"github.com/hutoroff/squash-bot/internal/management/application/game"
	"github.com/hutoroff/squash-bot/internal/management/application/group"
	"github.com/hutoroff/squash-bot/internal/management/application/participation"
	"github.com/hutoroff/squash-bot/internal/management/application/player"
	"github.com/hutoroff/squash-bot/internal/management/application/ports/inbound"
	"github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"
	appsched "github.com/hutoroff/squash-bot/internal/management/application/scheduler"
	"github.com/hutoroff/squash-bot/internal/management/application/venue"
	"github.com/hutoroff/squash-bot/migrations"
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

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect to database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := runMigrations(cfg.DatabaseURL); err != nil {
		slog.Error("run migrations", "err", err)
		os.Exit(1)
	}
	slog.Info("migrations applied")

	tgAPI, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		slog.Error("create telegram bot API", "err", err)
		os.Exit(1)
	}
	logger.Info("telegram bot authorized", "username", tgAPI.Self.UserName)

	// Outbound adapters (storage)
	gameRepo := postgres.NewGameRepo(pool)
	playerRepo := postgres.NewPlayerRepo(pool)
	participationRepo := postgres.NewParticipationRepo(pool)
	guestRepo := postgres.NewGuestRepo(pool)
	groupRepo := postgres.NewGroupRepo(pool)
	venueRepo := postgres.NewVenueRepo(pool)
	venueCredRepo := postgres.NewVenueCredentialRepo(pool)
	autoBookingResultRepo := postgres.NewAutoBookingResultRepo(pool)
	courtBookingRepo := postgres.NewCourtBookingRepo(pool)
	auditEventRepo := postgres.NewAuditEventRepo(pool)
	stateRepo := postgres.NewServiceStateRepo(pool)

	// Application services
	auditSvc := audit.NewAuditService(auditEventRepo, logger)
	serverOwnerIDs := parseAdminIDs(cfg.ServiceAdminIDs)

	gameSvc := game.NewService(gameRepo, venueRepo)
	venueSvc := venue.NewService(venueRepo, courtBookingRepo)

	var venueCredSvc inbound.VenueCredentialUseCases
	if cfg.CredentialsEncryptionKey != "" {
		enc, err := cryptoadapter.NewEncryptor(cfg.CredentialsEncryptionKey)
		if err != nil {
			slog.Error("init credentials encryptor", "err", err)
			os.Exit(1)
		}
		venueCredSvc = venue.NewCredentialService(venueCredRepo, venueRepo, courtBookingRepo, enc)
		slog.Info("venue credentials encryption enabled")
	} else {
		slog.Info("venue credentials encryption disabled (CREDENTIALS_ENCRYPTION_KEY not set)")
	}

	pollWindow, err := parsePollWindow(cfg.CronPoll)
	if err != nil {
		slog.Error("unsupported CRON_POLL value", "spec", cfg.CronPoll, "err", err)
		os.Exit(1)
	}

	var bookingClient outbound.BookingServiceClient
	if cfg.SportsBookingServiceURL != "" {
		bookingClient = bookingadapter.NewHTTPBookingClient(cfg.SportsBookingServiceURL, cfg.InternalAPISecret)
		slog.Info("booking service enabled", "booking_service", cfg.SportsBookingServiceURL)
	} else {
		slog.Info("booking service disabled (SPORTS_BOOKING_SERVICE_URL not set); auto-cancellation and auto-booking disabled")
	}

	gameNotifier := tgadapter.NewGameNotifier(tgAPI, gameRepo, participationRepo, guestRepo, groupRepo, loc, logger)
	partSvc := participation.NewService(playerRepo, participationRepo, guestRepo, gameNotifier)

	groupSvc := group.NewService(groupRepo)
	playerSvc := player.NewService(playerRepo, gameRepo)

	cancellationJob := appsched.NewCancellationReminderJob(tgAPI, gameRepo, participationRepo, guestRepo, groupRepo, gameNotifier, bookingClient, courtBookingRepo, autoBookingResultRepo, venueCredSvc, auditSvc, loc, logger, pollWindow)
	bookingReminderJob := appsched.NewBookingReminderJob(tgAPI, gameRepo, groupRepo, venueRepo, autoBookingResultRepo, loc, logger)
	dayAfterJob := appsched.NewDayAfterCleanupJob(tgAPI, gameRepo, participationRepo, guestRepo, groupRepo, loc, logger, courtBookingRepo)
	autoBookingJob := appsched.NewAutoBookingJob(tgAPI, groupRepo, venueRepo, bookingClient, venueCredSvc, autoBookingResultRepo, courtBookingRepo, auditSvc, loc, logger, cfg.CredentialErrorCooldown)
	scheduler := appsched.NewScheduler(logger, cancellationJob, bookingReminderJob, dayAfterJob, autoBookingJob)

	c := cron.New(cron.WithLocation(loc))
	if _, err := c.AddFunc(cfg.CronPoll, scheduler.RunScheduledTasks); err != nil {
		slog.Error("add poll cron", "spec", cfg.CronPoll, "err", err)
		os.Exit(1)
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

	changelog.AnnounceChangelog(ctx, tgAPI, groupRepo, stateRepo, loc, logger, Version)

	adminResolver := audit.NewAdminGroupsResolver(groupRepo, tgAPI, logger)
	h := inboundhttp.NewHandler(gameSvc, partSvc, venueSvc, venueCredSvc, groupSvc, playerSvc, scheduler, auditSvc, adminResolver, serverOwnerIDs, logger, Version)
	srv := inboundhttp.NewServer(":"+cfg.ServerPort, h, cfg.InternalAPISecret)

	slog.Info("management starting", "port", cfg.ServerPort, "version", Version)
	if err := inboundhttp.Run(ctx, srv, logger); err != nil {
		slog.Error("HTTP server error", "err", err)
		os.Exit(1)
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

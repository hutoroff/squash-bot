package main

import (
	"context"
	"fmt"
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
	"github.com/hutoroff/squash-bot/cmd/management/api"
	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/config"
	"github.com/hutoroff/squash-bot/migrations"
	"github.com/robfig/cron/v3"
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
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	loc, err := loadTimezone(cfg.Timezone)
	if err != nil {
		slog.Error("load timezone", "timezone", cfg.Timezone, "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
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

	gameRepo := storage.NewGameRepo(pool)
	playerRepo := storage.NewPlayerRepo(pool)
	participationRepo := storage.NewParticipationRepo(pool)
	guestRepo := storage.NewGuestRepo(pool)
	groupRepo := storage.NewGroupRepo(pool)
	venueRepo := storage.NewVenueRepo(pool)
	venueCredRepo := storage.NewVenueCredentialRepo(pool)
	autoBookingResultRepo := storage.NewAutoBookingResultRepo(pool)
	courtBookingRepo := storage.NewCourtBookingRepo(pool)

	gameService := service.NewGameService(gameRepo, venueRepo)
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
	if cfg.SportsBookingServiceURL != "" {
		if cfg.AutoBookingCourtsCount <= 0 {
			slog.Error("AUTO_BOOKING_COURTS_COUNT must be a positive integer", "value", cfg.AutoBookingCourtsCount)
			os.Exit(1)
		}
		bookingClient = service.NewHTTPBookingClient(cfg.SportsBookingServiceURL, cfg.InternalAPISecret)
		slog.Info("booking service enabled",
			"booking_service", cfg.SportsBookingServiceURL,
			"auto_booking_courts", cfg.AutoBookingCourtsCount)
	} else {
		slog.Info("booking service disabled (SPORTS_BOOKING_SERVICE_URL not set); auto-cancellation and auto-booking disabled")
	}

	gameNotifier := service.NewGameNotifier(tgAPI, gameRepo, participationRepo, guestRepo, groupRepo, loc, logger)
	partService := service.NewParticipationService(playerRepo, participationRepo, guestRepo, gameNotifier)

	cancellationJob := service.NewCancellationReminderJob(tgAPI, gameRepo, participationRepo, guestRepo, groupRepo, gameNotifier, bookingClient, courtBookingRepo, autoBookingResultRepo, venueCredService, loc, logger, pollWindow)
	bookingReminderJob := service.NewBookingReminderJob(tgAPI, gameRepo, groupRepo, venueRepo, autoBookingResultRepo, loc, logger)
	dayAfterJob := service.NewDayAfterCleanupJob(tgAPI, gameRepo, participationRepo, guestRepo, groupRepo, loc, logger, courtBookingRepo)
	autoBookingJob := service.NewAutoBookingJob(tgAPI, groupRepo, venueRepo, bookingClient, venueCredService, autoBookingResultRepo, courtBookingRepo, loc, logger, cfg.AutoBookingCourtsCount, cfg.CredentialErrorCooldown)
	scheduler := service.NewScheduler(logger, cancellationJob, bookingReminderJob, dayAfterJob, autoBookingJob)

	c := cron.New(cron.WithLocation(loc))
	if _, err := c.AddFunc(cfg.CronPoll, scheduler.RunScheduledTasks); err != nil {
		slog.Error("add poll cron", "spec", cfg.CronPoll, "err", err)
		os.Exit(1)
	}
	c.Start()
	defer c.Stop()
	slog.Info("cron scheduler started", "poll_interval", cfg.CronPoll)

	h := api.NewHandler(gameService, partService, venueService, venueCredService, groupRepo, playerRepo, scheduler, logger, Version)
	srv := api.NewServer(":"+cfg.ServerPort, h, cfg.InternalAPISecret)

	slog.Info("management starting", "port", cfg.ServerPort, "version", Version)
	if err := api.Run(ctx, srv, logger); err != nil {
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

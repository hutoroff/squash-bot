package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/robfig/cron/v3"
	"github.com/vkhutorov/squash_bot/internal/api"
	"github.com/vkhutorov/squash_bot/internal/config"
	"github.com/vkhutorov/squash_bot/internal/service"
	"github.com/vkhutorov/squash_bot/internal/storage"
	"github.com/vkhutorov/squash_bot/migrations"
)

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

	gameService := service.NewGameService(gameRepo, venueRepo)
	partService := service.NewParticipationService(playerRepo, participationRepo, guestRepo)
	venueService := service.NewVenueService(venueRepo)
	scheduler := service.NewSchedulerService(tgAPI, gameRepo, participationRepo, guestRepo, groupRepo, loc, logger)

	c := cron.New(cron.WithLocation(loc))
	if _, err := c.AddFunc(cfg.CronDayBefore, scheduler.RunDayBeforeCheck); err != nil {
		slog.Error("add day-before cron", "spec", cfg.CronDayBefore, "err", err)
		os.Exit(1)
	}
	if _, err := c.AddFunc(cfg.CronDayAfter, scheduler.RunDayAfterCleanup); err != nil {
		slog.Error("add day-after cron", "spec", cfg.CronDayAfter, "err", err)
		os.Exit(1)
	}
	if _, err := c.AddFunc(cfg.CronWeeklyReminder, scheduler.RunWeeklyReminder); err != nil {
		slog.Error("add weekly-reminder cron", "spec", cfg.CronWeeklyReminder, "err", err)
		os.Exit(1)
	}
	c.Start()
	defer c.Stop()
	slog.Info("cron scheduler started",
		"day_before", cfg.CronDayBefore,
		"day_after", cfg.CronDayAfter,
		"weekly_reminder", cfg.CronWeeklyReminder,
	)

	h := api.NewHandler(gameService, partService, venueService, groupRepo, scheduler, logger)
	srv := api.NewServer(":"+cfg.ServerPort, h, cfg.InternalAPISecret)

	slog.Info("squash-games-management starting", "port", cfg.ServerPort)
	if err := api.Run(ctx, srv, logger); err != nil {
		slog.Error("HTTP server error", "err", err)
		os.Exit(1)
	}
	slog.Info("squash-games-management stopped")
}

func loadTimezone(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q: %w", name, err)
	}
	return loc, nil
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

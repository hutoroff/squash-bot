package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/vkhutorov/squash_bot/internal/config"
	"github.com/vkhutorov/squash_bot/internal/service"
	"github.com/vkhutorov/squash_bot/internal/storage"
	"github.com/vkhutorov/squash_bot/internal/telegram"
	"github.com/vkhutorov/squash_bot/migrations"
)

func main() {
	cfg, err := config.Load()
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

	gameRepo := storage.NewGameRepo(pool)
	playerRepo := storage.NewPlayerRepo(pool)
	participationRepo := storage.NewParticipationRepo(pool)

	gameService := service.NewGameService(gameRepo)
	partService := service.NewParticipationService(playerRepo, participationRepo)

	bot, err := telegram.New(cfg, gameService, partService, logger)
	if err != nil {
		slog.Error("create bot", "err", err)
		os.Exit(1)
	}

	slog.Info("Bot starting...")
	bot.Start(ctx)
	slog.Info("Bot stopped")
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

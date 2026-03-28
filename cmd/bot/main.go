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

	api, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		slog.Error("create bot API", "err", err)
		os.Exit(1)
	}
	logger.Info("authorized on account", "username", api.Self.UserName)

	gameRepo := storage.NewGameRepo(pool)
	playerRepo := storage.NewPlayerRepo(pool)
	participationRepo := storage.NewParticipationRepo(pool)
	guestRepo := storage.NewGuestRepo(pool)
	groupRepo := storage.NewGroupRepo(pool)

	// Seed groups from GROUP_CHAT_IDS config for backward compatibility.
	// New groups are registered automatically via my_chat_member events.
	// Telegram does not replay my_chat_member for groups the bot is already in,
	// so this seeding is the only way to discover pre-existing memberships on upgrade.
	for _, chatID := range cfg.GroupChatIDs {
		// GetChatMember is the gate: if the bot cannot access the chat (stale ID,
		// typo, group deleted, bot already removed) we skip entirely so we never
		// persist a bogus row that no future my_chat_member event can clean up.
		member, err := api.GetChatMember(tgbotapi.GetChatMemberConfig{
			ChatConfigWithUser: tgbotapi.ChatConfigWithUser{ChatID: chatID, UserID: api.Self.ID},
		})
		if err != nil {
			slog.Warn("seed group: cannot access chat, skipping", "chat_id", chatID, "err", err)
			continue
		}
		// Bot is no longer in this group — remove any stale row so it does not
		// appear in group-selection keyboards or weekly reminders.
		if member.Status == "left" || member.Status == "kicked" {
			slog.Info("seed group: bot not in group, removing stale entry", "chat_id", chatID, "status", member.Status)
			if err := groupRepo.Remove(ctx, chatID); err != nil {
				slog.Warn("seed group: remove stale entry", "chat_id", chatID, "err", err)
			}
			continue
		}
		title := fmt.Sprintf("Group %d", chatID)
		if chatInfo, err := api.GetChat(tgbotapi.ChatInfoConfig{ChatConfig: tgbotapi.ChatConfig{ChatID: chatID}}); err == nil && chatInfo.Title != "" {
			title = chatInfo.Title
		}
		isAdmin := member.Status == "administrator" || member.Status == "creator"
		if err := groupRepo.Upsert(ctx, chatID, title, isAdmin); err != nil {
			slog.Warn("seed group from config", "chat_id", chatID, "err", err)
		}
	}

	// Warn operators if no groups are known after seeding.
	// This typically means GROUP_CHAT_IDS was not set on an upgraded deployment.
	// Without this the bot cannot post game announcements anywhere.
	if knownGroups, _ := groupRepo.GetAll(ctx); len(knownGroups) == 0 {
		slog.Warn("No groups registered. Set GROUP_CHAT_IDS to seed existing memberships, " +
			"or remove and re-add the bot to each group to register via Telegram events.")
	}

	gameService := service.NewGameService(gameRepo)
	partService := service.NewParticipationService(playerRepo, participationRepo, guestRepo)
	scheduler := service.NewSchedulerService(api, gameRepo, participationRepo, guestRepo, groupRepo, loc, logger)

	c := cron.New()
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

	bot := telegram.New(api, loc, gameService, partService, groupRepo, logger)

	slog.Info("Bot starting...")
	bot.Start(ctx)
	slog.Info("Bot stopped")
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

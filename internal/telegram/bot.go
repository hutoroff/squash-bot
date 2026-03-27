package telegram

import (
	"context"
	"log/slog"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/vkhutorov/squash_bot/internal/config"
	"github.com/vkhutorov/squash_bot/internal/service"
)

type Bot struct {
	api         *tgbotapi.BotAPI
	gameService *service.GameService
	partService *service.ParticipationService
	cfg         *config.Config
	loc         *time.Location
	logger      *slog.Logger
}

func New(api *tgbotapi.BotAPI, cfg *config.Config, loc *time.Location, gameService *service.GameService, partService *service.ParticipationService, logger *slog.Logger) *Bot {
	return &Bot{
		api:         api,
		gameService: gameService,
		partService: partService,
		cfg:         cfg,
		loc:         loc,
		logger:      logger,
	}
}

// Start runs the long-polling update loop until ctx is cancelled.
func (b *Bot) Start(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return
		case update := <-updates:
			go b.processUpdate(ctx, update)
		}
	}
}

func (b *Bot) processUpdate(ctx context.Context, update tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error("panic in update handler", "recover", r)
		}
	}()

	switch {
	case update.Message != nil:
		slog.Debug("incoming message", "from", update.Message.From.ID, "chat", update.Message.Chat.ID)
		b.handleMessage(ctx, update.Message)
	case update.CallbackQuery != nil:
		slog.Debug("incoming callback", "from", update.CallbackQuery.From.ID, "data", update.CallbackQuery.Data)
		b.handleCallback(ctx, update.CallbackQuery)
	default:
		slog.Debug("unhandled update type", "update_id", update.UpdateID)
	}
}

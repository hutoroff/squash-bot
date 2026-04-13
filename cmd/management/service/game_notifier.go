package service

import (
	"context"
	"log/slog"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/gameformat"
	"github.com/hutoroff/squash-bot/internal/i18n"
)

// GameNotifier edits the Telegram group message for a game to reflect current
// participation state. It is invoked on-demand whenever a participation changes
// (join, skip, add guest, remove guest) — not on a schedule.
type GameNotifier struct {
	api       TelegramAPI
	gameRepo  GameRepository
	partRepo  ParticipationRepository
	guestRepo GuestRepository
	groupRepo GroupRepository
	loc       *time.Location // service-default timezone fallback
	logger    *slog.Logger
}

func NewGameNotifier(
	api TelegramAPI,
	gameRepo GameRepository,
	partRepo ParticipationRepository,
	guestRepo GuestRepository,
	groupRepo GroupRepository,
	loc *time.Location,
	logger *slog.Logger,
) *GameNotifier {
	return &GameNotifier{
		api:       api,
		gameRepo:  gameRepo,
		partRepo:  partRepo,
		guestRepo: guestRepo,
		groupRepo: groupRepo,
		loc:       loc,
		logger:    logger,
	}
}

// EditGameMessage fetches the current game state and edits the Telegram group
// message to reflect it. It is a no-op when the game has no message_id yet.
// Errors are logged but not returned — callers should fire this in a goroutine.
func (n *GameNotifier) EditGameMessage(ctx context.Context, gameID int64) {
	game, err := n.gameRepo.GetByID(ctx, gameID)
	if err != nil {
		n.logger.Error("GameNotifier.EditGameMessage: get game", "err", err, "game_id", gameID)
		return
	}
	if game.MessageID == nil {
		return
	}

	participations, err := n.partRepo.GetByGame(ctx, gameID)
	if err != nil {
		n.logger.Error("GameNotifier.EditGameMessage: get participations", "err", err, "game_id", gameID)
		return
	}
	guests, err := n.guestRepo.GetByGame(ctx, gameID)
	if err != nil {
		n.logger.Error("GameNotifier.EditGameMessage: get guests", "err", err, "game_id", gameID)
		return
	}

	group, err := n.groupRepo.GetByID(ctx, game.ChatID)
	if err != nil {
		n.logger.Error("GameNotifier.EditGameMessage: get group", "err", err, "game_id", gameID)
		return
	}
	loc := resolveGroupTimezone(group, n.loc, n.logger)
	lz := i18n.New(i18n.Normalize(group.Language))

	msgText := gameformat.FormatGameMessage(game, participations, guests, loc, time.Now(), lz)
	keyboard := gameformat.GameKeyboard(game.ID, lz)

	msgID := int(*game.MessageID)
	edit := tgbotapi.NewEditMessageText(game.ChatID, msgID, msgText)
	edit.ReplyMarkup = &keyboard
	if _, err := n.api.Send(edit); err != nil {
		n.logger.Error("GameNotifier.EditGameMessage: send", "err", err, "game_id", gameID)
	}
}

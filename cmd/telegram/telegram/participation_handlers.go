package telegram

import (
	"context"
	"log/slog"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/vkhutorov/squash_bot/internal/i18n"
	"github.com/vkhutorov/squash_bot/internal/models"
)

func (b *Bot) handleJoin(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	participations, err := b.client.Join(ctx, gameID, cb.From.ID, cb.From.UserName, cb.From.FirstName, cb.From.LastName)
	if err != nil {
		slog.Error("join game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	game, err := b.client.GetGameByID(ctx, gameID)
	if err != nil {
		slog.Error("get game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	guests, err := b.client.GetGuests(ctx, gameID)
	if err != nil {
		slog.Error("get guests", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	groupLz := b.groupLocalizer(ctx, game.ChatID)
	b.editGameMessage(ctx, cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests, groupLz)
	b.answerCallback(cb.ID, "")
}

func (b *Bot) handleSkip(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	participations, skipped, err := b.client.Skip(ctx, gameID, cb.From.ID, cb.From.UserName, cb.From.FirstName, cb.From.LastName)
	if err != nil {
		slog.Error("skip game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	if !skipped {
		b.answerCallback(cb.ID, "")
		return
	}

	game, err := b.client.GetGameByID(ctx, gameID)
	if err != nil {
		slog.Error("get game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	guests, err := b.client.GetGuests(ctx, gameID)
	if err != nil {
		slog.Error("get guests", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	groupLz := b.groupLocalizer(ctx, game.ChatID)
	b.editGameMessage(ctx, cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests, groupLz)
	b.answerCallback(cb.ID, "")
}

func (b *Bot) handleGuestAdd(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	u := cb.From
	// Capacity enforcement is done atomically inside AddGuest (DB advisory lock +
	// transaction), so there is no TOCTOU race even under concurrent clicks.
	added, participations, guests, err := b.client.AddGuest(ctx, gameID, u.ID, u.UserName, u.FirstName, u.LastName)
	if err != nil {
		slog.Error("add guest", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}
	if !added {
		b.answerCallback(cb.ID, lz.T(i18n.MsgGameFullCapacity))
		return
	}

	game, err := b.client.GetGameByID(ctx, gameID)
	if err != nil {
		slog.Error("get game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	groupLz := b.groupLocalizer(ctx, game.ChatID)
	b.editGameMessage(ctx, cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests, groupLz)
	b.answerCallback(cb.ID, "")
}

func (b *Bot) handleGuestRemove(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	removed, participations, guests, err := b.client.RemoveGuest(ctx, gameID, cb.From.ID)
	if err != nil {
		slog.Error("remove guest", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	if !removed {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNoGuestsToRemove))
		return
	}

	game, err := b.client.GetGameByID(ctx, gameID)
	if err != nil {
		slog.Error("get game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	groupLz := b.groupLocalizer(ctx, game.ChatID)
	b.editGameMessage(ctx, cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests, groupLz)
	b.answerCallback(cb.ID, "")
}

func (b *Bot) editGameMessage(ctx context.Context, chatID int64, messageID int, game *models.Game, participations []*models.GameParticipation, guests []*models.GuestParticipation, lz *i18n.Localizer) {
	loc := b.groupLocation(ctx, game.ChatID)
	msgText := FormatGameMessage(game, participations, guests, loc, time.Now(), lz)
	keyboard := gameKeyboard(game.ID, lz)

	edit := tgbotapi.NewEditMessageText(chatID, messageID, msgText)
	edit.ReplyMarkup = &keyboard

	if _, err := b.api.Send(edit); err != nil {
		slog.Error("edit game message", "err", err, "game_id", game.ID)
	}
}

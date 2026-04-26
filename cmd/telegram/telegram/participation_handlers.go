package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/i18n"
)

// actorDisplayFrom formats a display name for audit from a Telegram user.
func actorDisplayFrom(u *tgbotapi.User) string {
	if u.UserName != "" {
		return "@" + u.UserName
	}
	name := u.FirstName
	if u.LastName != "" {
		if name != "" {
			name += " "
		}
		name += u.LastName
	}
	return name
}

func (b *Bot) handleJoin(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	_, err := b.client.Join(ctx, gameID, cb.Message.Chat.ID, cb.From.ID, cb.From.UserName, cb.From.FirstName, cb.From.LastName)
	if err != nil {
		slog.Error("join game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}
	b.answerCallback(cb.ID, "")
	b.scheduleGameMessageEdit(gameID)
}

func (b *Bot) handleSkip(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	_, skipped, err := b.client.Skip(ctx, gameID, cb.Message.Chat.ID, cb.From.ID, cb.From.UserName, cb.From.FirstName, cb.From.LastName)
	if err != nil {
		slog.Error("skip game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}
	if !skipped {
		b.answerCallback(cb.ID, "")
		return
	}
	b.answerCallback(cb.ID, "")
	b.scheduleGameMessageEdit(gameID)
}

func (b *Bot) handleGuestAdd(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	u := cb.From
	// Capacity enforcement is done atomically inside AddGuest (DB advisory lock +
	// transaction), so there is no TOCTOU race even under concurrent clicks.
	added, _, _, err := b.client.AddGuest(ctx, gameID, cb.Message.Chat.ID, u.ID, u.UserName, u.FirstName, u.LastName)
	if err != nil {
		slog.Error("add guest", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}
	if !added {
		b.answerCallback(cb.ID, lz.T(i18n.MsgGameFullCapacity))
		return
	}
	b.answerCallback(cb.ID, "")
	b.scheduleGameMessageEdit(gameID)
}

func (b *Bot) handleGuestRemove(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	removed, _, _, err := b.client.RemoveGuest(ctx, gameID, cb.Message.Chat.ID, cb.From.ID, cb.From.UserName, cb.From.FirstName, cb.From.LastName)
	if err != nil {
		slog.Error("remove guest", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}
	if !removed {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNoGuestsToRemove))
		return
	}
	b.answerCallback(cb.ID, "")
	b.scheduleGameMessageEdit(gameID)
}

// scheduleGameMessageEdit enqueues a coalesced re-render of the game's group
// announcement. Multiple concurrent calls for the same game collapse into at
// most two sequential Telegram API calls, preventing rate-limit errors under
// burst button activity.
func (b *Bot) scheduleGameMessageEdit(gameID int64) {
	raw, _ := b.editWorkers.LoadOrStore(gameID, &gameEditWorker{})
	raw.(*gameEditWorker).schedule(func() {
		b.doEditGameMessage(context.Background(), gameID)
	})
}

// doEditGameMessage fetches fresh game state and updates the group announcement.
// It handles HTTP 429 rate-limiting with a single sleep-and-retry, and silently
// drops "message is not modified" errors that arise when coalesced edits produce
// identical content.
func (b *Bot) doEditGameMessage(ctx context.Context, gameID int64) {
	for attempt := 0; attempt < 2; attempt++ {
		game, err := b.client.GetGameByID(ctx, gameID)
		if err != nil {
			slog.Error("edit game message: get game", "err", err, "game_id", gameID)
			return
		}
		if game.MessageID == nil {
			return
		}

		participations, err := b.client.GetParticipations(ctx, gameID)
		if err != nil {
			slog.Error("edit game message: get participations", "err", err, "game_id", gameID)
			return
		}

		guests, err := b.client.GetGuests(ctx, gameID)
		if err != nil {
			slog.Error("edit game message: get guests", "err", err, "game_id", gameID)
			return
		}

		loc := b.groupLocation(ctx, game.ChatID)
		groupLz := b.groupLocalizer(ctx, game.ChatID)
		msgText := FormatGameMessage(game, participations, guests, loc, time.Now(), groupLz)
		keyboard := gameKeyboard(game.ID, groupLz)

		edit := tgbotapi.NewEditMessageText(game.ChatID, int(*game.MessageID), msgText)
		edit.ReplyMarkup = &keyboard

		if _, err = b.api.Send(edit); err == nil {
			return
		}

		var tgErr *tgbotapi.Error
		if errors.As(err, &tgErr) {
			switch tgErr.Code {
			case 429:
				if attempt == 0 {
					slog.Warn("edit game message: rate limited, retrying",
						"retry_after", tgErr.RetryAfter, "game_id", gameID)
					time.Sleep(time.Duration(tgErr.RetryAfter+1) * time.Second)
					continue
				}
			case 400:
				if strings.Contains(tgErr.Message, "message is not modified") {
					return
				}
			}
		}
		slog.Error("edit game message", "err", err, "game_id", gameID)
		return
	}
}

package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/vkhutorov/squash_bot/internal/models"
)

func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	if msg.From == nil || msg.Text == "" {
		return
	}

	isPrivate := msg.Chat.Type == "private"
	isGroupMention := (msg.Chat.Type == "group" || msg.Chat.Type == "supergroup") &&
		msg.Chat.ID == b.cfg.GroupChatID && b.isBotMentioned(msg)

	if !isPrivate && !isGroupMention {
		return
	}

	isAdmin, err := b.isGroupAdmin(msg.From.ID)
	if err != nil {
		slog.Error("check admin status", "err", err, "user_id", msg.From.ID)
		b.reply(msg.Chat.ID, msg.MessageID, "Failed to verify permissions")
		return
	}
	if !isAdmin {
		b.reply(msg.Chat.ID, msg.MessageID, "Only group administrators can create games")
		return
	}

	text := msg.Text
	if isGroupMention {
		text = stripBotMention(text, b.api.Self.UserName, msg.Entities)
	}

	gameDate, courts, err := parseAdminCommand(text, b.loc)
	if err != nil {
		b.reply(msg.Chat.ID, msg.MessageID, "Invalid format. Use:\n2024-03-15 18:00\ncourts: 2,3,4")
		return
	}

	game, err := b.gameService.CreateGame(ctx, b.cfg.GroupChatID, gameDate, courts)
	if err != nil {
		slog.Error("create game", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, "Failed to create game")
		return
	}

	msgText := FormatGameMessage(game, nil, b.loc)
	keyboard := gameKeyboard(game.ID)

	announcement := tgbotapi.NewMessage(b.cfg.GroupChatID, msgText)
	announcement.ReplyMarkup = keyboard

	sent, err := b.api.Send(announcement)
	if err != nil {
		slog.Error("send game message", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, "Game created but failed to send announcement")
		return
	}

	pin := tgbotapi.PinChatMessageConfig{
		ChatID:              b.cfg.GroupChatID,
		MessageID:           sent.MessageID,
		DisableNotification: true,
	}
	if _, err := b.api.Request(pin); err != nil {
		slog.Error("pin message", "err", err)
	}

	if err := b.gameService.UpdateMessageID(ctx, game.ID, int64(sent.MessageID)); err != nil {
		slog.Error("update message_id", "err", err)
	}

	slog.Info("Game created", "date", gameDate.Format(time.DateOnly), "courts", courts, "message_id", sent.MessageID)
	b.reply(msg.Chat.ID, msg.MessageID, "Game created and pinned ✓")
}

func (b *Bot) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	if cb.Data == "" || cb.Message == nil {
		b.answerCallback(cb.ID, "")
		return
	}

	parts := strings.SplitN(cb.Data, ":", 2)
	if len(parts) != 2 {
		slog.Debug("invalid callback data", "data", cb.Data)
		b.answerCallback(cb.ID, "")
		return
	}

	gameID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		slog.Debug("invalid game_id in callback", "data", cb.Data)
		b.answerCallback(cb.ID, "")
		return
	}

	switch parts[0] {
	case "join":
		b.handleJoin(ctx, cb, gameID)
	case "skip":
		b.handleSkip(ctx, cb, gameID)
	default:
		slog.Debug("unknown callback action", "action", parts[0])
		b.answerCallback(cb.ID, "")
	}
}

func (b *Bot) handleJoin(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	participations, err := b.partService.Join(ctx, gameID, cb.From.ID, cb.From.UserName, cb.From.FirstName, cb.From.LastName)
	if err != nil {
		slog.Error("join game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	game, err := b.gameService.GetByID(ctx, gameID)
	if err != nil {
		slog.Error("get game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	b.editGameMessage(cb.Message.Chat.ID, cb.Message.MessageID, game, participations)
	b.answerCallback(cb.ID, "")
}

func (b *Bot) handleSkip(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	participations, skipped, err := b.partService.Skip(ctx, gameID, cb.From.ID, cb.From.UserName, cb.From.FirstName, cb.From.LastName)
	if err != nil {
		slog.Error("skip game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	if !skipped {
		b.answerCallback(cb.ID, "")
		return
	}

	game, err := b.gameService.GetByID(ctx, gameID)
	if err != nil {
		slog.Error("get game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	b.editGameMessage(cb.Message.Chat.ID, cb.Message.MessageID, game, participations)
	b.answerCallback(cb.ID, "")
}

func (b *Bot) editGameMessage(chatID int64, messageID int, game *models.Game, participations []*models.GameParticipation) {
	msgText := FormatGameMessage(game, participations, b.loc)
	keyboard := gameKeyboard(game.ID)

	edit := tgbotapi.NewEditMessageText(chatID, messageID, msgText)
	edit.ReplyMarkup = &keyboard

	if _, err := b.api.Send(edit); err != nil {
		slog.Error("edit game message", "err", err, "game_id", game.ID)
	}
}

func (b *Bot) reply(chatID int64, replyToID int, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyToMessageID = replyToID
	if _, err := b.api.Send(msg); err != nil {
		slog.Error("send reply", "err", err)
	}
}

func (b *Bot) answerCallback(callbackID, text string) {
	answer := tgbotapi.NewCallback(callbackID, text)
	if _, err := b.api.Request(answer); err != nil {
		slog.Debug("answer callback", "err", err)
	}
}

func gameKeyboard(gameID int64) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("I'm in", fmt.Sprintf("join:%d", gameID)),
			tgbotapi.NewInlineKeyboardButtonData("I'll skip", fmt.Sprintf("skip:%d", gameID)),
		),
	)
}

func (b *Bot) isGroupAdmin(userID int64) (bool, error) {
	admins, err := b.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
		ChatConfig: tgbotapi.ChatConfig{ChatID: b.cfg.GroupChatID},
	})
	if err != nil {
		return false, err
	}
	for _, admin := range admins {
		if admin.User.ID == userID {
			return true, nil
		}
	}
	return false, nil
}

func (b *Bot) isBotMentioned(msg *tgbotapi.Message) bool {
	for _, entity := range msg.Entities {
		if entity.Type == "mention" {
			mention := msg.Text[entity.Offset : entity.Offset+entity.Length]
			if mention == "@"+b.api.Self.UserName {
				return true
			}
		}
	}
	return false
}

func stripBotMention(text, botUsername string, entities []tgbotapi.MessageEntity) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "@"+botUsername, ""))
}

func parseAdminCommand(text string, loc *time.Location) (time.Time, string, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 2 {
		return time.Time{}, "", fmt.Errorf("expected 2 lines")
	}

	gameDate, err := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(lines[0]), loc)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("parse date: %w", err)
	}

	courtsLine := strings.TrimPrefix(strings.TrimSpace(lines[1]), "courts:")
	courts := strings.ReplaceAll(strings.TrimSpace(courtsLine), " ", "")
	if courts == "" {
		return time.Time{}, "", fmt.Errorf("empty courts")
	}

	return gameDate, courts, nil
}

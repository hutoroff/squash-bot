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
	isGroupMention := b.isKnownGroupMention(msg)

	if !isPrivate && !isGroupMention {
		return
	}

	text := msg.Text

	if isGroupMention {
		// Group mention: target group is determined by where the bot was @mentioned.
		text = stripBotMention(text, b.api.Self.UserName, msg.Entities)
		isAdmin, err := b.isAdminInGroup(msg.From.ID, msg.Chat.ID)
		if err != nil {
			slog.Error("check admin status", "err", err, "user_id", msg.From.ID)
			b.reply(msg.Chat.ID, msg.MessageID, "Failed to verify permissions")
			return
		}
		if !isAdmin {
			b.reply(msg.Chat.ID, msg.MessageID, "Only group administrators can create games")
			return
		}
		gameDate, courts, err := parseAdminCommand(text, b.loc)
		if err != nil {
			b.reply(msg.Chat.ID, msg.MessageID, "Invalid format. Use:\n2024-03-15 18:00\ncourts: 2,3,4")
			return
		}
		b.createAndAnnounceGame(ctx, msg.Chat.ID, msg.MessageID, msg.Chat.ID, gameDate, courts)
		return
	}

	// Private message: discover which configured groups this user admins.
	adminGroupIDs := b.adminGroups(msg.From.ID)
	if len(adminGroupIDs) == 0 {
		b.reply(msg.Chat.ID, msg.MessageID, "Only group administrators can create games")
		return
	}

	gameDate, courts, err := parseAdminCommand(text, b.loc)
	if err != nil {
		b.reply(msg.Chat.ID, msg.MessageID, "Invalid format. Use:\n2024-03-15 18:00\ncourts: 2,3,4")
		return
	}

	if len(adminGroupIDs) == 1 {
		b.createAndAnnounceGame(ctx, msg.Chat.ID, msg.MessageID, adminGroupIDs[0], gameDate, courts)
		return
	}

	// Admin manages multiple groups — ask which one to post to.
	// The key is (chatID, messageID): message IDs are unique only within a single
	// chat, so using messageID alone would let two admins in different private chats
	// collide and overwrite each other's pending game.
	key := pendingGameKey{chatID: msg.Chat.ID, messageID: msg.MessageID}
	b.pendingGames.Store(key, &pendingGame{
		gameDate:    gameDate,
		courts:      courts,
		replyChatID: msg.Chat.ID,
		replyMsgID:  msg.MessageID,
	})
	keyboard := b.buildGroupSelectionKeyboard(adminGroupIDs, key)
	selMsg := tgbotapi.NewMessage(msg.Chat.ID, "Which group should I post the game announcement in?")
	selMsg.ReplyMarkup = keyboard
	if _, err := b.api.Send(selMsg); err != nil {
		slog.Error("send group selection keyboard", "err", err)
		b.pendingGames.Delete(key)
	}
}

func (b *Bot) handleGroupSelection(ctx context.Context, cb *tgbotapi.CallbackQuery, key pendingGameKey, groupID int64) {
	// Verify the callback originates from the same chat that created the request.
	// A mismatch would indicate tampered callback data; drop it silently.
	if cb.Message.Chat.ID != key.chatID {
		slog.Warn("handleGroupSelection: origin chat mismatch",
			"expected", key.chatID, "got", cb.Message.Chat.ID)
		b.answerCallback(cb.ID, "")
		return
	}

	raw, ok := b.pendingGames.LoadAndDelete(key)
	if !ok {
		b.answerCallback(cb.ID, "Session expired, please send the game details again")
		return
	}
	pg := raw.(*pendingGame)

	// Re-verify admin status at callback time to prevent replay attacks.
	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, "You are not an admin in that group")
		return
	}

	b.answerCallback(cb.ID, "")

	// Clear the selection keyboard.
	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	editSel := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, "Creating game...")
	editSel.ReplyMarkup = &emptyKeyboard
	b.api.Send(editSel) //nolint:errcheck — best-effort UI update

	b.createAndAnnounceGame(ctx, pg.replyChatID, pg.replyMsgID, groupID, pg.gameDate, pg.courts)
}

func (b *Bot) createAndAnnounceGame(ctx context.Context, replyChatID int64, replyMsgID int, groupID int64, gameDate time.Time, courts string) {
	game, err := b.gameService.CreateGame(ctx, groupID, gameDate, courts)
	if err != nil {
		slog.Error("create game", "err", err)
		b.reply(replyChatID, replyMsgID, "Failed to create game")
		return
	}

	msgText := FormatGameMessage(game, nil, nil, b.loc)
	keyboard := gameKeyboard(game.ID)

	announcement := tgbotapi.NewMessage(groupID, msgText)
	announcement.ReplyMarkup = keyboard

	sent, err := b.api.Send(announcement)
	if err != nil {
		slog.Error("send game message", "err", err)
		b.reply(replyChatID, replyMsgID, "Game created but failed to send announcement")
		return
	}

	pin := tgbotapi.PinChatMessageConfig{
		ChatID:              groupID,
		MessageID:           sent.MessageID,
		DisableNotification: true,
	}
	if _, err := b.api.Request(pin); err != nil {
		slog.Error("pin message", "err", err)
	}

	if err := b.gameService.UpdateMessageID(ctx, game.ID, int64(sent.MessageID)); err != nil {
		slog.Error("update message_id", "err", err)
	}

	slog.Info("Game created", "date", gameDate.Format(time.DateOnly), "courts", courts, "message_id", sent.MessageID, "group_id", groupID)
	b.reply(replyChatID, replyMsgID, "Game created and pinned ✓")
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

	action, rawID := parts[0], parts[1]

	if action == "select_group" {
		// Format: select_group:<originChatID>:<originMessageID>:<groupID>
		// Both chatID and messageID are needed because Telegram message IDs are
		// unique only within a chat, not globally.
		subparts := strings.SplitN(rawID, ":", 3)
		if len(subparts) != 3 {
			slog.Debug("invalid select_group callback format", "data", cb.Data)
			b.answerCallback(cb.ID, "")
			return
		}
		originChatID, err := strconv.ParseInt(subparts[0], 10, 64)
		if err != nil {
			slog.Debug("invalid chat_id in select_group callback", "data", cb.Data)
			b.answerCallback(cb.ID, "")
			return
		}
		originMsgID, err := strconv.ParseInt(subparts[1], 10, 64)
		if err != nil {
			slog.Debug("invalid message_id in select_group callback", "data", cb.Data)
			b.answerCallback(cb.ID, "")
			return
		}
		groupID, err := strconv.ParseInt(subparts[2], 10, 64)
		if err != nil {
			slog.Debug("invalid group_id in select_group callback", "data", cb.Data)
			b.answerCallback(cb.ID, "")
			return
		}
		b.handleGroupSelection(ctx, cb, pendingGameKey{chatID: originChatID, messageID: int(originMsgID)}, groupID)
		return
	}

	gameID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		slog.Debug("invalid game_id in callback", "data", cb.Data)
		b.answerCallback(cb.ID, "")
		return
	}

	switch action {
	case "join":
		b.handleJoin(ctx, cb, gameID)
	case "skip":
		b.handleSkip(ctx, cb, gameID)
	case "guest_add":
		b.handleGuestAdd(ctx, cb, gameID)
	case "guest_remove":
		b.handleGuestRemove(ctx, cb, gameID)
	default:
		slog.Debug("unknown callback action", "action", action)
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

	guests, err := b.partService.GetGuests(ctx, gameID)
	if err != nil {
		slog.Error("get guests", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	b.editGameMessage(cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests)
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

	guests, err := b.partService.GetGuests(ctx, gameID)
	if err != nil {
		slog.Error("get guests", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	b.editGameMessage(cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests)
	b.answerCallback(cb.ID, "")
}

func (b *Bot) handleGuestAdd(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	u := cb.From
	participations, guests, err := b.partService.AddGuest(ctx, gameID, u.ID, u.UserName, u.FirstName, u.LastName)
	if err != nil {
		slog.Error("add guest", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	game, err := b.gameService.GetByID(ctx, gameID)
	if err != nil {
		slog.Error("get game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	b.editGameMessage(cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests)
	b.answerCallback(cb.ID, "")
}

func (b *Bot) handleGuestRemove(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	removed, participations, guests, err := b.partService.RemoveGuest(ctx, gameID, cb.From.ID)
	if err != nil {
		slog.Error("remove guest", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	if !removed {
		b.answerCallback(cb.ID, "You haven't invited any guests")
		return
	}

	game, err := b.gameService.GetByID(ctx, gameID)
	if err != nil {
		slog.Error("get game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	b.editGameMessage(cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests)
	b.answerCallback(cb.ID, "")
}

func (b *Bot) editGameMessage(chatID int64, messageID int, game *models.Game, participations []*models.GameParticipation, guests []*models.GuestParticipation) {
	msgText := FormatGameMessage(game, participations, guests, b.loc)
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

// gameKeyboard builds the inline keyboard for a game announcement.
// Row 1: "I'm in" / "I'll skip" — register or remove yourself.
// Row 2: "+1" / "-1" — add or remove a guest you are bringing.
// Only the player who added a guest can remove it via "-1".
func gameKeyboard(gameID int64) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("I'm in", fmt.Sprintf("join:%d", gameID)),
			tgbotapi.NewInlineKeyboardButtonData("I'll skip", fmt.Sprintf("skip:%d", gameID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("+1", fmt.Sprintf("guest_add:%d", gameID)),
			tgbotapi.NewInlineKeyboardButtonData("-1", fmt.Sprintf("guest_remove:%d", gameID)),
		),
	)
}

// adminGroups returns the IDs of all known groups where userID is an admin.
// Groups that cannot be reached are logged and skipped so that one inaccessible
// group does not disable the entire DM flow.
func (b *Bot) adminGroups(userID int64) []int64 {
	groups, err := b.groupRepo.GetAll(context.Background())
	if err != nil {
		slog.Warn("adminGroups: failed to query groups", "err", err)
		return nil
	}
	var result []int64
	for _, g := range groups {
		ok, err := b.isAdminInGroup(userID, g.ChatID)
		if err != nil {
			slog.Warn("adminGroups: skipping inaccessible group", "group_id", g.ChatID, "err", err)
			continue
		}
		if ok {
			result = append(result, g.ChatID)
		}
	}
	return result
}

// isAdminInGroup reports whether userID is an administrator of the given group.
func (b *Bot) isAdminInGroup(userID, groupID int64) (bool, error) {
	admins, err := b.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
		ChatConfig: tgbotapi.ChatConfig{ChatID: groupID},
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

// isKnownGroupMention reports whether the message is a bot @mention in a known group.
func (b *Bot) isKnownGroupMention(msg *tgbotapi.Message) bool {
	if msg.Chat.Type != "group" && msg.Chat.Type != "supergroup" {
		return false
	}
	if !b.isBotMentioned(msg) {
		return false
	}
	groups, err := b.groupRepo.GetAll(context.Background())
	if err != nil {
		return false
	}
	for _, g := range groups {
		if msg.Chat.ID == g.ChatID {
			return true
		}
	}
	return false
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

// buildGroupSelectionKeyboard creates one button per group labelled with its Telegram title.
// origin is embedded in each button's callback data so the handler can look up
// the correct pendingGame entry even when multiple admins have concurrent dialogs.
func (b *Bot) buildGroupSelectionKeyboard(groupIDs []int64, origin pendingGameKey) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, gid := range groupIDs {
		title := fmt.Sprintf("Group %d", gid)
		chatInfo, err := b.api.GetChat(tgbotapi.ChatInfoConfig{
			ChatConfig: tgbotapi.ChatConfig{ChatID: gid},
		})
		if err == nil && chatInfo.Title != "" {
			title = chatInfo.Title
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(title,
				fmt.Sprintf("select_group:%d:%d:%d", origin.chatID, origin.messageID, gid)),
		))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
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

package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

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

	// In private chats, slash commands take priority and cancel any pending
	// courts-edit state (so an admin who clicked "Edit Courts" and then sends
	// /help doesn't have the command text stored as court data).
	// Group @mentions are handled below with their own per-command routing.
	if isPrivate && strings.HasPrefix(msg.Text, "/") {
		b.pendingCourtsEdit.Delete(msg.Chat.ID)
		b.handleCommand(ctx, msg)
		return
	}

	// In private chats, check for a pending courts-edit.
	if isPrivate {
		if raw, ok := b.pendingCourtsEdit.LoadAndDelete(msg.Chat.ID); ok {
			b.processCourtsEdit(ctx, msg, raw.(int64))
			return
		}
	}

	text := msg.Text

	if isGroupMention {
		// Group mention: target group is determined by where the bot was @mentioned.
		text = stripBotMention(text, b.api.Self.UserName, msg.Entities)

		if strings.HasPrefix(strings.TrimSpace(text), "/") {
			// Slash command in a group @mention context.
			// Only /help and /start are served from the group.
			// /new_game is allowed when it carries game details in the body — treat it
			// identically to a plain game-creation @mention for the current group.
			// All other management commands belong in a private chat.
			cmdText := strings.TrimSpace(text)
			firstWord := strings.Fields(cmdText)[0]
			if idx := strings.Index(firstWord, "@"); idx >= 0 {
				firstWord = firstWord[:idx]
			}
			switch strings.ToLower(firstWord) {
			case "/help", "/start":
				stripped := *msg
				stripped.Text = cmdText
				b.handleCommand(ctx, &stripped)
				return
			case "/new_game":
				lines := strings.SplitN(cmdText, "\n", 2)
				if len(lines) < 2 || strings.TrimSpace(lines[1]) == "" {
					b.reply(msg.Chat.ID, msg.MessageID,
						"Send game details after the command:\n/new\\_game\nYYYY-MM-DD HH:MM\ncourts: 2,3,4")
					return
				}
				text = strings.TrimSpace(lines[1])
				// text is now the game-creation body; fall through to the
				// admin check and createAndAnnounceGame call below.
			default:
				b.reply(msg.Chat.ID, msg.MessageID,
					"Management commands work in private messages. Start a chat with me and use /help.")
				return
			}
		}

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
			b.reply(msg.Chat.ID, msg.MessageID, "Invalid format. Use:\nYYYY-MM-DD HH:MM\ncourts: 2,3,4")
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
		b.reply(msg.Chat.ID, msg.MessageID, "Invalid format. Use:\nYYYY-MM-DD HH:MM\ncourts: 2,3,4")
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
	game, err := b.client.CreateGame(ctx, groupID, gameDate, courts)
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

	if err := b.client.UpdateMessageID(ctx, game.ID, int64(sent.MessageID)); err != nil {
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

	if action == "trigger" {
		b.handleTrigger(ctx, cb, rawID)
		return
	}

	// manage_kick and manage_kick_guest carry two IDs: <gameID>:<targetID>.
	// Handle them before the single-ID parse below.
	if action == "manage_kick" || action == "manage_kick_guest" {
		subparts := strings.SplitN(rawID, ":", 2)
		if len(subparts) != 2 {
			slog.Debug("invalid two-id callback format", "data", cb.Data)
			b.answerCallback(cb.ID, "")
			return
		}
		gid, err := strconv.ParseInt(subparts[0], 10, 64)
		if err != nil {
			slog.Debug("invalid game_id in two-id callback", "data", cb.Data)
			b.answerCallback(cb.ID, "")
			return
		}
		targetID, err := strconv.ParseInt(subparts[1], 10, 64)
		if err != nil {
			slog.Debug("invalid target_id in two-id callback", "data", cb.Data)
			b.answerCallback(cb.ID, "")
			return
		}
		switch action {
		case "manage_kick":
			b.handleManageKickPlayer(ctx, cb, gid, targetID)
		case "manage_kick_guest":
			b.handleManageKickGuest(ctx, cb, gid, targetID)
		}
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
	case "manage":
		b.handleManage(ctx, cb, gameID)
	case "manage_players":
		b.handleManageShowPlayers(ctx, cb, gameID)
	case "manage_guests":
		b.handleManageShowGuests(ctx, cb, gameID)
	case "manage_courts":
		b.handleManageEditCourts(ctx, cb, gameID)
	case "manage_close":
		b.handleManageClose(ctx, cb)
	default:
		slog.Debug("unknown callback action", "action", action)
		b.answerCallback(cb.ID, "")
	}
}

func (b *Bot) handleJoin(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	participations, err := b.client.Join(ctx, gameID, cb.From.ID, cb.From.UserName, cb.From.FirstName, cb.From.LastName)
	if err != nil {
		slog.Error("join game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	game, err := b.client.GetGameByID(ctx, gameID)
	if err != nil {
		slog.Error("get game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	guests, err := b.client.GetGuests(ctx, gameID)
	if err != nil {
		slog.Error("get guests", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	b.editGameMessage(cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests)
	b.answerCallback(cb.ID, "")
}

func (b *Bot) handleSkip(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	participations, skipped, err := b.client.Skip(ctx, gameID, cb.From.ID, cb.From.UserName, cb.From.FirstName, cb.From.LastName)
	if err != nil {
		slog.Error("skip game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	if !skipped {
		b.answerCallback(cb.ID, "")
		return
	}

	game, err := b.client.GetGameByID(ctx, gameID)
	if err != nil {
		slog.Error("get game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	guests, err := b.client.GetGuests(ctx, gameID)
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
	// Capacity enforcement is done atomically inside AddGuest (DB advisory lock +
	// transaction), so there is no TOCTOU race even under concurrent clicks.
	added, participations, guests, err := b.client.AddGuest(ctx, gameID, u.ID, u.UserName, u.FirstName, u.LastName)
	if err != nil {
		slog.Error("add guest", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}
	if !added {
		b.answerCallback(cb.ID, "Game is already at full capacity")
		return
	}

	game, err := b.client.GetGameByID(ctx, gameID)
	if err != nil {
		slog.Error("get game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	b.editGameMessage(cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests)
	b.answerCallback(cb.ID, "")
}

func (b *Bot) handleGuestRemove(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	removed, participations, guests, err := b.client.RemoveGuest(ctx, gameID, cb.From.ID)
	if err != nil {
		slog.Error("remove guest", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Something went wrong, please try again")
		return
	}

	if !removed {
		b.answerCallback(cb.ID, "You haven't invited any guests")
		return
	}

	game, err := b.client.GetGameByID(ctx, gameID)
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
	groups, err := b.client.GetGroups(context.Background())
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
	groups, err := b.client.GetGroups(context.Background())
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
	// Telegram entity offsets are UTF-16 code unit indices, not UTF-8 byte indices.
	// Convert the message text to UTF-16 once and slice from there, then decode back
	// to a Go string. This handles emoji and other non-BMP characters that appear
	// before the @mention correctly.
	utf16Text := utf16.Encode([]rune(msg.Text))
	for _, entity := range msg.Entities {
		start, end := entity.Offset, entity.Offset+entity.Length
		if start < 0 || end > len(utf16Text) {
			continue
		}
		raw := string(utf16.Decode(utf16Text[start:end]))
		switch entity.Type {
		case "mention":
			// "@botname" mention entity.
			if raw == "@"+b.api.Self.UserName {
				return true
			}
		case "bot_command":
			// "/command@botname" — only treat as a mention when addressed to this bot.
			// Telegram sends the full "/command@botname" text inside the entity.
			if atIdx := strings.Index(raw, "@"); atIdx >= 0 {
				if strings.EqualFold(raw[atIdx+1:], b.api.Self.UserName) {
					return true
				}
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

// checkManageAdmin fetches the game and verifies that cb.From is still an admin
// of the game's group chat. Answers the callback and returns (nil, false) on any
// failure so callers can simply do `if game, ok := ...; !ok { return }`.
func (b *Bot) checkManageAdmin(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) (*models.Game, bool) {
	game, err := b.client.GetGameByID(ctx, gameID)
	if err != nil {
		slog.Error("checkManageAdmin: get game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, "Game not found")
		return nil, false
	}

	isAdmin, err := b.isAdminInGroup(cb.From.ID, game.ChatID)
	if err != nil {
		slog.Error("checkManageAdmin: check admin", "err", err, "user_id", cb.From.ID, "chat_id", game.ChatID)
		b.answerCallback(cb.ID, "Failed to verify permissions")
		return nil, false
	}
	if !isAdmin {
		b.answerCallback(cb.ID, "You no longer have admin access to this group")
		return nil, false
	}

	return game, true
}

// handleManage shows the management keyboard for a specific game.
func (b *Bot) handleManage(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	game, ok := b.checkManageAdmin(ctx, cb, gameID)
	if !ok {
		return
	}
	b.answerCallback(cb.ID, "")
	b.renderManageScreen(ctx, cb, game)
}

// renderManageScreen edits the callback message to show the management view for the given game.
// The callback must be answered before calling this.
func (b *Bot) renderManageScreen(ctx context.Context, cb *tgbotapi.CallbackQuery, game *models.Game) {
	participations, err := b.client.GetParticipations(ctx, game.ID)
	if err != nil {
		slog.Error("renderManageScreen: get participations", "err", err)
		return
	}
	guests, err := b.client.GetGuests(ctx, game.ID)
	if err != nil {
		slog.Error("renderManageScreen: get guests", "err", err)
		return
	}

	registered := 0
	for _, p := range participations {
		if p.Status == models.StatusRegistered {
			registered++
		}
	}

	localDate := game.GameDate.In(b.loc)
	text := fmt.Sprintf("*Manage game:*\n📅 %s · %s\n🎾 Courts: %s\nPlayers: %d/%d, Guests: %d",
		formatGameDate(localDate), localDate.Format("15:04"),
		escapeMarkdown(game.Courts), registered, game.CourtsCount*2, len(guests))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Kick Player", fmt.Sprintf("manage_players:%d", game.ID)),
			tgbotapi.NewInlineKeyboardButtonData("Kick Guest", fmt.Sprintf("manage_guests:%d", game.ID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Edit Courts", fmt.Sprintf("manage_courts:%d", game.ID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✕ Close", fmt.Sprintf("manage_close:%d", game.ID)),
		),
	)

	edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, text)
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = &keyboard
	b.api.Send(edit) //nolint:errcheck
}

// handleManageShowPlayers lists registered players as kick buttons.
func (b *Bot) handleManageShowPlayers(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	if _, ok := b.checkManageAdmin(ctx, cb, gameID); !ok {
		return
	}

	participations, err := b.client.GetParticipations(ctx, gameID)
	if err != nil {
		slog.Error("handleManageShowPlayers: get participations", "err", err)
		b.answerCallback(cb.ID, "Something went wrong")
		return
	}

	var registered []*models.GameParticipation
	for _, p := range participations {
		if p.Status == models.StatusRegistered {
			registered = append(registered, p)
		}
	}

	if len(registered) == 0 {
		b.answerCallback(cb.ID, "No registered players to kick")
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, p := range registered {
		label := fmt.Sprintf("Kick %s", playerDisplayName(p.Player))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label,
				fmt.Sprintf("manage_kick:%d:%d", gameID, p.Player.TelegramID)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("← Back", fmt.Sprintf("manage:%d", gameID)),
	))

	edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, "Select a player to kick:")
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	edit.ReplyMarkup = &keyboard
	b.api.Send(edit) //nolint:errcheck
	b.answerCallback(cb.ID, "")
}

// handleManageKickPlayer removes a player from the game and updates the group message.
func (b *Bot) handleManageKickPlayer(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID, telegramID int64) {
	game, ok := b.checkManageAdmin(ctx, cb, gameID)
	if !ok {
		return
	}

	participations, guests, removed, err := b.client.KickPlayer(ctx, gameID, telegramID)
	if err != nil {
		slog.Error("handleManageKickPlayer: kick", "err", err)
		b.answerCallback(cb.ID, "Something went wrong")
		return
	}
	if !removed {
		b.answerCallback(cb.ID, "Player not found in this game")
		return
	}

	slog.Info("Admin kicked player", "admin", cb.From.ID, "target_telegram_id", telegramID, "game_id", gameID)

	// Update the group announcement.
	if game.MessageID != nil {
		b.editGameMessage(game.ChatID, int(*game.MessageID), game, participations, guests)
	}

	b.answerCallback(cb.ID, "Player kicked ✓")
	b.renderManageScreen(ctx, cb, game)
}

// handleManageShowGuests lists guests as kick buttons.
func (b *Bot) handleManageShowGuests(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	if _, ok := b.checkManageAdmin(ctx, cb, gameID); !ok {
		return
	}

	guests, err := b.client.GetGuests(ctx, gameID)
	if err != nil {
		slog.Error("handleManageShowGuests: get guests", "err", err)
		b.answerCallback(cb.ID, "Something went wrong")
		return
	}

	if len(guests) == 0 {
		b.answerCallback(cb.ID, "No guests to kick")
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, g := range guests {
		label := fmt.Sprintf("Kick +1 (by %s)", playerDisplayName(g.InvitedBy))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label,
				fmt.Sprintf("manage_kick_guest:%d:%d", gameID, g.ID)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("← Back", fmt.Sprintf("manage:%d", gameID)),
	))

	edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, "Select a guest to kick:")
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	edit.ReplyMarkup = &keyboard
	b.api.Send(edit) //nolint:errcheck
	b.answerCallback(cb.ID, "")
}

// handleManageKickGuest removes a specific guest and updates the group message.
func (b *Bot) handleManageKickGuest(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID, guestID int64) {
	game, ok := b.checkManageAdmin(ctx, cb, gameID)
	if !ok {
		return
	}

	participations, guests, removed, err := b.client.KickGuestByID(ctx, gameID, guestID)
	if err != nil {
		slog.Error("handleManageKickGuest: kick", "err", err)
		b.answerCallback(cb.ID, "Something went wrong")
		return
	}
	if !removed {
		b.answerCallback(cb.ID, "Guest not found")
		return
	}

	slog.Info("Admin kicked guest", "admin", cb.From.ID, "guest_id", guestID, "game_id", gameID)

	// Update the group announcement.
	if game.MessageID != nil {
		b.editGameMessage(game.ChatID, int(*game.MessageID), game, participations, guests)
	}

	b.answerCallback(cb.ID, "Guest kicked ✓")
	b.renderManageScreen(ctx, cb, game)
}

// handleTrigger calls the management service to run a scheduled event on demand.
// Only users listed in serviceAdminIDs are allowed.
func (b *Bot) handleTrigger(ctx context.Context, cb *tgbotapi.CallbackQuery, event string) {
	if !b.serviceAdminIDs[cb.From.ID] {
		b.answerCallback(cb.ID, "Not authorized")
		return
	}

	switch event {
	case "day_before", "day_after", "weekly_reminder":
		// valid events
	default:
		slog.Debug("handleTrigger: unknown event", "event", event)
		b.answerCallback(cb.ID, "Unknown event")
		return
	}

	// The trigger endpoint returns 202 immediately (job runs async on the
	// management service), so this call should be fast. Only send a success
	// callback and remove the keyboard after a confirmed successful response;
	// on error, answer with a failure notice and leave the buttons intact so
	// the admin can retry.
	if err := b.client.TriggerScheduledEvent(ctx, event); err != nil {
		slog.Error("handleTrigger: request failed", "event", event, "err", err)
		b.answerCallback(cb.ID, "Failed to trigger — check service health")
		return
	}

	slog.Info("Manual trigger", "event", event, "user_id", cb.From.ID)
	b.answerCallback(cb.ID, "Triggered ✓")

	// Remove the keyboard so the same message cannot be used to fire the job
	// again. This prevents accidental duplicate runs (especially relevant for
	// weekly_reminder which sends DMs). A fresh /trigger shows a new menu.
	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	edit := tgbotapi.NewEditMessageReplyMarkup(cb.Message.Chat.ID, cb.Message.MessageID, emptyKeyboard)
	b.api.Send(edit) //nolint:errcheck
}

// handleManageClose restores the games-list view in the callback message so the
// admin can continue managing other games without re-running /games.
func (b *Bot) handleManageClose(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	b.answerCallback(cb.ID, "")

	// Shared fallback: remove the keyboard and leave the message text as-is.
	fallback := func() {
		emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
		edit := tgbotapi.NewEditMessageReplyMarkup(cb.Message.Chat.ID, cb.Message.MessageID, emptyKeyboard)
		b.api.Send(edit) //nolint:errcheck
	}

	adminGroupIDs := b.adminGroups(cb.From.ID)
	if len(adminGroupIDs) == 0 {
		fallback()
		return
	}

	games, err := b.client.GetUpcomingGamesByChatIDs(ctx, adminGroupIDs)
	if err != nil {
		slog.Error("handleManageClose: get games", "err", err)
		fallback()
		return
	}

	groups, err := b.client.GetGroups(ctx)
	if err != nil {
		slog.Error("handleManageClose: get groups", "err", err)
		fallback()
		return
	}

	if len(games) == 0 {
		emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, "No upcoming games in your groups.")
		edit.ReplyMarkup = &emptyKeyboard
		b.api.Send(edit) //nolint:errcheck
		return
	}

	text, keyboard := formatGamesListMessage(games, groups, b.loc)
	edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, text)
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = &keyboard
	b.api.Send(edit) //nolint:errcheck
}

// handleManageEditCourts stores a pending courts-edit and prompts the admin to type the new value.
func (b *Bot) handleManageEditCourts(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	if _, ok := b.checkManageAdmin(ctx, cb, gameID); !ok {
		return
	}

	b.pendingCourtsEdit.Store(cb.Message.Chat.ID, gameID)
	b.answerCallback(cb.ID, "")

	prompt := tgbotapi.NewMessage(cb.Message.Chat.ID, "Send the new courts (e.g.: 2,3,4):")
	b.api.Send(prompt) //nolint:errcheck
}

func stripBotMention(text, botUsername string, entities []tgbotapi.MessageEntity) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "@"+botUsername, ""))
}

const maxCourtsLen = 100

func parseAdminCommand(text string, loc *time.Location) (time.Time, string, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 2 {
		return time.Time{}, "", fmt.Errorf("expected 2 lines")
	}

	gameDate, err := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(lines[0]), loc)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("parse date: %w", err)
	}
	if !gameDate.After(time.Now().In(loc)) {
		return time.Time{}, "", fmt.Errorf("game date must be in the future")
	}

	courtsLine := strings.TrimPrefix(strings.TrimSpace(lines[1]), "courts:")
	courts := strings.ReplaceAll(strings.TrimSpace(courtsLine), " ", "")
	if courts == "" {
		return time.Time{}, "", fmt.Errorf("empty courts")
	}
	if len(courts) > maxCourtsLen {
		return time.Time{}, "", fmt.Errorf("courts string too long (max %d chars)", maxCourtsLen)
	}

	return gameDate, courts, nil
}

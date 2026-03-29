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
	"github.com/vkhutorov/squash_bot/internal/i18n"
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

	// In private chats, slash commands take priority and cancel any pending state.
	if isPrivate && strings.HasPrefix(msg.Text, "/") {
		b.pendingCourtsEdit.Delete(msg.Chat.ID)
		b.pendingNewGameWizard.Delete(msg.Chat.ID)
		b.handleCommand(ctx, msg)
		return
	}

	// In private chats, route to whichever state machine is active.
	if isPrivate {
		if raw, ok := b.pendingCourtsEdit.LoadAndDelete(msg.Chat.ID); ok {
			b.processCourtsEdit(ctx, msg, raw.(int64))
			return
		}
		if raw, ok := b.pendingNewGameWizard.Load(msg.Chat.ID); ok {
			b.processNewGameWizard(ctx, msg, raw.(*newGameWizard))
			return
		}
		// Non-command, non-wizard private messages are ignored.
		return
	}

	// Group @mention: only /help and /start are served; everything else
	// is redirected to private chat.
	lz := b.userLocalizer(msg.From.LanguageCode)
	text := stripBotMention(msg.Text, b.api.Self.UserName, msg.Entities)
	text = strings.TrimSpace(text)

	if strings.HasPrefix(text, "/") {
		cmdText := text
		firstWord := strings.Fields(cmdText)[0]
		if idx := strings.Index(firstWord, "@"); idx >= 0 {
			firstWord = firstWord[:idx]
		}
		switch strings.ToLower(firstWord) {
		case "/help", "/start":
			stripped := *msg
			stripped.Text = cmdText
			b.handleCommand(ctx, &stripped)
		default:
			b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgManagementPrivateOnly))
		}
		return
	}

	// Non-command group @mention.
	b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgManagementPrivateOnly))
}

func (b *Bot) handleGroupSelection(ctx context.Context, cb *tgbotapi.CallbackQuery, key pendingGameKey, groupID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

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
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	pg := raw.(*pendingGame)

	// Re-verify admin status at callback time to prevent replay attacks.
	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNotAdminInGroup))
		return
	}

	b.answerCallback(cb.ID, "")

	// Clear the selection keyboard.
	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	editSel := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgCreatingGame))
	editSel.ReplyMarkup = &emptyKeyboard
	b.api.Send(editSel) //nolint:errcheck — best-effort UI update

	b.createAndAnnounceGame(ctx, pg.replyChatID, pg.replyMsgID, groupID, pg.gameDate, pg.courts, lz)
}

func (b *Bot) createAndAnnounceGame(ctx context.Context, replyChatID int64, replyMsgID int, groupID int64, gameDate time.Time, courts string, userLz *i18n.Localizer) {
	game, err := b.client.CreateGame(ctx, groupID, gameDate, courts)
	if err != nil {
		slog.Error("create game", "err", err)
		b.reply(replyChatID, replyMsgID, userLz.T(i18n.MsgFailedCreateGame))
		return
	}

	// Use the group's language for the public announcement.
	groupLz := b.groupLocalizer(ctx, groupID)
	msgText := FormatGameMessage(game, nil, nil, b.loc, time.Now(), groupLz)
	keyboard := gameKeyboard(game.ID, groupLz)

	announcement := tgbotapi.NewMessage(groupID, msgText)
	announcement.ReplyMarkup = keyboard

	sent, err := b.api.Send(announcement)
	if err != nil {
		slog.Error("send game message", "err", err)
		b.reply(replyChatID, replyMsgID, userLz.T(i18n.MsgGameCreatedFailedAnnounce))
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
	b.reply(replyChatID, replyMsgID, userLz.T(i18n.MsgGameCreatedPinned))
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

	if action == "ng_date" {
		b.handleNewGameDate(ctx, cb, rawID)
		return
	}

	// set_lang_group:<groupID> — show language selection for that group
	if action == "set_lang_group" {
		groupID, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil {
			slog.Debug("invalid group_id in set_lang_group callback", "data", cb.Data)
			b.answerCallback(cb.ID, "")
			return
		}
		b.handleSetLangGroup(ctx, cb, groupID)
		return
	}

	// set_lang:<lang>:<groupID> — apply the chosen language to the group
	if action == "set_lang" {
		subparts := strings.SplitN(rawID, ":", 2)
		if len(subparts) != 2 {
			slog.Debug("invalid set_lang callback format", "data", cb.Data)
			b.answerCallback(cb.ID, "")
			return
		}
		groupID, err := strconv.ParseInt(subparts[1], 10, 64)
		if err != nil {
			slog.Debug("invalid group_id in set_lang callback", "data", cb.Data)
			b.answerCallback(cb.ID, "")
			return
		}
		b.handleSetLang(ctx, cb, subparts[0], groupID)
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
	b.editGameMessage(cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests, groupLz)
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
	b.editGameMessage(cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests, groupLz)
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
	b.editGameMessage(cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests, groupLz)
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
	b.editGameMessage(cb.Message.Chat.ID, cb.Message.MessageID, game, participations, guests, groupLz)
	b.answerCallback(cb.ID, "")
}

func (b *Bot) editGameMessage(chatID int64, messageID int, game *models.Game, participations []*models.GameParticipation, guests []*models.GuestParticipation, lz *i18n.Localizer) {
	msgText := FormatGameMessage(game, participations, guests, b.loc, time.Now(), lz)
	keyboard := gameKeyboard(game.ID, lz)

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
// Row 1: join / skip — register or remove yourself.
// Row 2: +1 / -1 — add or remove a guest.
func gameKeyboard(gameID int64, lz *i18n.Localizer) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnImIn), fmt.Sprintf("join:%d", gameID)),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnIllSkip), fmt.Sprintf("skip:%d", gameID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnPlusOne), fmt.Sprintf("guest_add:%d", gameID)),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnMinusOne), fmt.Sprintf("guest_remove:%d", gameID)),
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
func (b *Bot) checkManageAdmin(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64, lz *i18n.Localizer) (*models.Game, bool) {
	game, err := b.client.GetGameByID(ctx, gameID)
	if err != nil {
		slog.Error("checkManageAdmin: get game", "err", err, "game_id", gameID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgGameNotFound))
		return nil, false
	}

	isAdmin, err := b.isAdminInGroup(cb.From.ID, game.ChatID)
	if err != nil {
		slog.Error("checkManageAdmin: check admin", "err", err, "user_id", cb.From.ID, "chat_id", game.ChatID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgFailedVerifyPermissions))
		return nil, false
	}
	if !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgLostAdminAccess))
		return nil, false
	}

	return game, true
}

// handleManage shows the management keyboard for a specific game.
func (b *Bot) handleManage(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	game, ok := b.checkManageAdmin(ctx, cb, gameID, lz)
	if !ok {
		return
	}
	b.answerCallback(cb.ID, "")
	b.renderManageScreen(ctx, cb, game, lz)
}

// renderManageScreen edits the callback message to show the management view for the given game.
// The callback must be answered before calling this.
func (b *Bot) renderManageScreen(ctx context.Context, cb *tgbotapi.CallbackQuery, game *models.Game, lz *i18n.Localizer) {
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
	text := lz.Tf(i18n.MsgManageGameHeader,
		lz.FormatGameDate(localDate), localDate.Format("15:04"),
		escapeMarkdown(game.Courts), registered, game.CourtsCount*2, len(guests))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnKickPlayer), fmt.Sprintf("manage_players:%d", game.ID)),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnKickGuest), fmt.Sprintf("manage_guests:%d", game.ID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnEditCourts), fmt.Sprintf("manage_courts:%d", game.ID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnClose), fmt.Sprintf("manage_close:%d", game.ID)),
		),
	)

	edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, text)
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = &keyboard
	b.api.Send(edit) //nolint:errcheck
}

// handleManageShowPlayers lists registered players as kick buttons.
func (b *Bot) handleManageShowPlayers(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	if _, ok := b.checkManageAdmin(ctx, cb, gameID, lz); !ok {
		return
	}

	participations, err := b.client.GetParticipations(ctx, gameID)
	if err != nil {
		slog.Error("handleManageShowPlayers: get participations", "err", err)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	var registered []*models.GameParticipation
	for _, p := range participations {
		if p.Status == models.StatusRegistered {
			registered = append(registered, p)
		}
	}

	if len(registered) == 0 {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNoPlayersToKick))
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, p := range registered {
		label := lz.Tf(i18n.MsgKickPlayerLabel, playerDisplayName(p.Player))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label,
				fmt.Sprintf("manage_kick:%d:%d", gameID, p.Player.TelegramID)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnBack), fmt.Sprintf("manage:%d", gameID)),
	))

	edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgSelectPlayerToKick))
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	edit.ReplyMarkup = &keyboard
	b.api.Send(edit) //nolint:errcheck
	b.answerCallback(cb.ID, "")
}

// handleManageKickPlayer removes a player from the game and updates the group message.
func (b *Bot) handleManageKickPlayer(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID, telegramID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	game, ok := b.checkManageAdmin(ctx, cb, gameID, lz)
	if !ok {
		return
	}

	participations, guests, removed, err := b.client.KickPlayer(ctx, gameID, telegramID)
	if err != nil {
		slog.Error("handleManageKickPlayer: kick", "err", err)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}
	if !removed {
		b.answerCallback(cb.ID, lz.T(i18n.MsgKickPlayerNotFound))
		return
	}

	slog.Info("Admin kicked player", "admin", cb.From.ID, "target_telegram_id", telegramID, "game_id", gameID)

	// Update the group announcement using the group's language.
	if game.MessageID != nil {
		groupLz := b.groupLocalizer(ctx, game.ChatID)
		b.editGameMessage(game.ChatID, int(*game.MessageID), game, participations, guests, groupLz)
	}

	b.answerCallback(cb.ID, lz.T(i18n.MsgPlayerKicked))
	b.renderManageScreen(ctx, cb, game, lz)
}

// handleManageShowGuests lists guests as kick buttons.
func (b *Bot) handleManageShowGuests(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	if _, ok := b.checkManageAdmin(ctx, cb, gameID, lz); !ok {
		return
	}

	guests, err := b.client.GetGuests(ctx, gameID)
	if err != nil {
		slog.Error("handleManageShowGuests: get guests", "err", err)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	if len(guests) == 0 {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNoGuestsToKick))
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, g := range guests {
		label := lz.Tf(i18n.MsgKickGuestLabel, playerDisplayName(g.InvitedBy))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label,
				fmt.Sprintf("manage_kick_guest:%d:%d", gameID, g.ID)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnBack), fmt.Sprintf("manage:%d", gameID)),
	))

	edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgSelectGuestToKick))
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	edit.ReplyMarkup = &keyboard
	b.api.Send(edit) //nolint:errcheck
	b.answerCallback(cb.ID, "")
}

// handleManageKickGuest removes a specific guest and updates the group message.
func (b *Bot) handleManageKickGuest(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID, guestID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	game, ok := b.checkManageAdmin(ctx, cb, gameID, lz)
	if !ok {
		return
	}

	participations, guests, removed, err := b.client.KickGuestByID(ctx, gameID, guestID)
	if err != nil {
		slog.Error("handleManageKickGuest: kick", "err", err)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}
	if !removed {
		b.answerCallback(cb.ID, lz.T(i18n.MsgGuestNotFound))
		return
	}

	slog.Info("Admin kicked guest", "admin", cb.From.ID, "guest_id", guestID, "game_id", gameID)

	// Update the group announcement using the group's language.
	if game.MessageID != nil {
		groupLz := b.groupLocalizer(ctx, game.ChatID)
		b.editGameMessage(game.ChatID, int(*game.MessageID), game, participations, guests, groupLz)
	}

	b.answerCallback(cb.ID, lz.T(i18n.MsgGuestKicked))
	b.renderManageScreen(ctx, cb, game, lz)
}

// handleTrigger calls the management service to run a scheduled event on demand.
// Only users listed in serviceAdminIDs are allowed.
func (b *Bot) handleTrigger(ctx context.Context, cb *tgbotapi.CallbackQuery, event string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	if !b.serviceAdminIDs[cb.From.ID] {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNotAuthorized))
		return
	}

	switch event {
	case "day_before", "day_after", "weekly_reminder":
		// valid events
	default:
		slog.Debug("handleTrigger: unknown event", "event", event)
		b.answerCallback(cb.ID, lz.T(i18n.MsgUnknownEvent))
		return
	}

	// The trigger endpoint returns 202 immediately (job runs async on the
	// management service), so this call should be fast. Only send a success
	// callback and remove the keyboard after a confirmed successful response;
	// on error, answer with a failure notice and leave the buttons intact so
	// the admin can retry.
	if err := b.client.TriggerScheduledEvent(ctx, event); err != nil {
		slog.Error("handleTrigger: request failed", "event", event, "err", err)
		b.answerCallback(cb.ID, lz.T(i18n.MsgFailedTrigger))
		return
	}

	slog.Info("Manual trigger", "event", event, "user_id", cb.From.ID)
	b.answerCallback(cb.ID, lz.T(i18n.MsgTriggered))

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
	lz := b.userLocalizer(cb.From.LanguageCode)
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
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgNoUpcomingGames))
		edit.ReplyMarkup = &emptyKeyboard
		b.api.Send(edit) //nolint:errcheck
		return
	}

	text, keyboard := formatGamesListMessage(games, groups, b.loc, lz)
	edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, text)
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = &keyboard
	b.api.Send(edit) //nolint:errcheck
}

// handleManageEditCourts stores a pending courts-edit and prompts the admin to type the new value.
func (b *Bot) handleManageEditCourts(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	if _, ok := b.checkManageAdmin(ctx, cb, gameID, lz); !ok {
		return
	}

	b.pendingCourtsEdit.Store(cb.Message.Chat.ID, gameID)
	b.answerCallback(cb.ID, "")

	prompt := tgbotapi.NewMessage(cb.Message.Chat.ID, lz.T(i18n.MsgSendNewCourts))
	b.api.Send(prompt) //nolint:errcheck
}

// handleSetLangGroup shows the language selection keyboard for a specific group.
func (b *Bot) handleSetLangGroup(ctx context.Context, cb *tgbotapi.CallbackQuery, groupID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminSetLanguage))
		return
	}

	b.answerCallback(cb.ID, "")
	b.renderLanguageKeyboard(cb.Message.Chat.ID, cb.Message.MessageID, groupID, lz)
}

// handleSetLang applies the chosen language to the group.
func (b *Bot) handleSetLang(ctx context.Context, cb *tgbotapi.CallbackQuery, lang string, groupID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminSetLanguage))
		return
	}

	if err := b.client.SetGroupLanguage(ctx, groupID, lang); err != nil {
		slog.Error("handleSetLang: set language", "err", err, "group_id", groupID, "lang", lang)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	slog.Info("Group language updated", "group_id", groupID, "lang", lang, "by_user", cb.From.ID)
	b.answerCallback(cb.ID, lz.T(i18n.MsgLanguageSet))

	// Remove the keyboard from the language-selection message.
	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	edit := tgbotapi.NewEditMessageReplyMarkup(cb.Message.Chat.ID, cb.Message.MessageID, emptyKeyboard)
	b.api.Send(edit) //nolint:errcheck
}

// renderLanguageKeyboard edits (or sends) a message with language selection buttons for groupID.
func (b *Bot) renderLanguageKeyboard(chatID int64, messageID int, groupID int64, lz *i18n.Localizer) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnLangEn), fmt.Sprintf("set_lang:en:%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnLangDe), fmt.Sprintf("set_lang:de:%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnLangRu), fmt.Sprintf("set_lang:ru:%d", groupID)),
		),
	)

	if messageID != 0 {
		edit := tgbotapi.NewEditMessageText(chatID, messageID, lz.T(i18n.MsgSelectLanguage))
		edit.ReplyMarkup = &keyboard
		b.api.Send(edit) //nolint:errcheck
	} else {
		msg := tgbotapi.NewMessage(chatID, lz.T(i18n.MsgSelectLanguage))
		msg.ReplyMarkup = keyboard
		b.api.Send(msg) //nolint:errcheck
	}
}

// handleNewGameDate handles the ng_date:<YYYY-MM-DD> callback from the date picker.
// It stores the selected date in the wizard state and prompts for time.
func (b *Bot) handleNewGameDate(ctx context.Context, cb *tgbotapi.CallbackQuery, dateStr string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	date, err := time.ParseInLocation("2006-01-02", dateStr, b.loc)
	if err != nil {
		slog.Debug("handleNewGameDate: invalid date in callback", "data", dateStr)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	// Re-verify admin eligibility at callback time.
	adminGroupIDs := b.adminGroups(cb.From.ID)
	if len(adminGroupIDs) == 0 {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminCreateGames))
		return
	}

	b.pendingNewGameWizard.Store(cb.Message.Chat.ID, &newGameWizard{
		gameDate: date,
		step:     wizardStepTime,
	})
	b.answerCallback(cb.ID, "")

	localDate := date.In(b.loc)
	prompt := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID,
		lz.Tf(i18n.MsgNewGameEnterTime, lz.FormatGameDate(localDate)))
	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	prompt.ReplyMarkup = &emptyKeyboard
	b.api.Send(prompt) //nolint:errcheck
}

// processNewGameWizard routes an incoming private message to the correct wizard step.
func (b *Bot) processNewGameWizard(ctx context.Context, msg *tgbotapi.Message, wizard *newGameWizard) {
	lz := b.userLocalizer(msg.From.LanguageCode)
	switch wizard.step {
	case wizardStepTime:
		b.processNewGameWizardTime(ctx, msg, wizard, lz)
	case wizardStepCourts:
		b.processNewGameWizardCourts(ctx, msg, wizard, lz)
	}
}

// processNewGameWizardTime parses the user's time input and advances to courts step.
func (b *Bot) processNewGameWizardTime(ctx context.Context, msg *tgbotapi.Message, wizard *newGameWizard, lz *i18n.Localizer) {
	t, err := time.Parse("15:04", strings.TrimSpace(msg.Text))
	if err != nil {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgNewGameInvalidTime))
		return // keep wizard state so the user can retry
	}

	d := wizard.gameDate.In(b.loc)
	gameDate := time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), 0, 0, b.loc)
	if !gameDate.After(time.Now()) {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgNewGameTimePast))
		return // keep wizard state so the user can retry
	}

	wizard.gameDate = gameDate
	wizard.step = wizardStepCourts
	b.pendingNewGameWizard.Store(msg.Chat.ID, wizard)

	localDate := gameDate.In(b.loc)
	b.reply(msg.Chat.ID, msg.MessageID,
		lz.Tf(i18n.MsgNewGameEnterCourts, lz.FormatGameDate(localDate), localDate.Format("15:04")))
}

// processNewGameWizardCourts validates the courts input and creates the game.
func (b *Bot) processNewGameWizardCourts(ctx context.Context, msg *tgbotapi.Message, wizard *newGameWizard, lz *i18n.Localizer) {
	courts := normalizeCourts(strings.TrimSpace(msg.Text))
	if courts == "" {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgInvalidCourtsFormat))
		return // keep wizard state so the user can retry
	}
	if len(courts) > maxCourtsLen {
		b.reply(msg.Chat.ID, msg.MessageID, lz.Tf(i18n.MsgCourtsStringTooLong, maxCourtsLen))
		return
	}

	b.pendingNewGameWizard.Delete(msg.Chat.ID)

	adminGroupIDs := b.adminGroups(msg.From.ID)
	if len(adminGroupIDs) == 0 {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgOnlyAdminCreateGames))
		return
	}

	if len(adminGroupIDs) == 1 {
		b.createAndAnnounceGame(ctx, msg.Chat.ID, msg.MessageID, adminGroupIDs[0], wizard.gameDate, courts, lz)
		return
	}

	// Admin manages multiple groups — ask which one to post to.
	key := pendingGameKey{chatID: msg.Chat.ID, messageID: msg.MessageID}
	b.pendingGames.Store(key, &pendingGame{
		gameDate:    wizard.gameDate,
		courts:      courts,
		replyChatID: msg.Chat.ID,
		replyMsgID:  msg.MessageID,
	})
	keyboard := b.buildGroupSelectionKeyboard(adminGroupIDs, key)
	selMsg := tgbotapi.NewMessage(msg.Chat.ID, lz.T(i18n.MsgWhichGroup))
	selMsg.ReplyMarkup = keyboard
	if _, err := b.api.Send(selMsg); err != nil {
		slog.Error("send group selection keyboard", "err", err)
		b.pendingGames.Delete(key)
	}
}

// normalizeCourts converts any common delimiter (space, semicolon, slash) to
// commas and removes empty parts, returning a canonical "2,3,4" string.
func normalizeCourts(s string) string {
	for _, sep := range []string{" ", ";", "/", "|"} {
		s = strings.ReplaceAll(s, sep, ",")
	}
	var parts []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ",")
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

	trimmedLine := strings.TrimSpace(lines[1])
	if !strings.HasPrefix(trimmedLine, "courts:") {
		return time.Time{}, "", fmt.Errorf("second line must start with 'courts:'")
	}
	courts := strings.ReplaceAll(strings.TrimSpace(strings.TrimPrefix(trimmedLine, "courts:")), " ", "")
	if courts == "" {
		return time.Time{}, "", fmt.Errorf("empty courts")
	}
	if len(courts) > maxCourtsLen {
		return time.Time{}, "", fmt.Errorf("courts string too long (max %d chars)", maxCourtsLen)
	}
	for _, p := range strings.Split(courts, ",") {
		if p == "" {
			return time.Time{}, "", fmt.Errorf("invalid courts: empty part (use format like 2,3,4)")
		}
	}

	return gameDate, courts, nil
}

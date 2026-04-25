package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/gameformat"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

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
		label := lz.Tf(i18n.MsgKickPlayerLabel, gameformat.PlayerDisplayName(p.Player))
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

	_, _, removed, err := b.client.KickPlayer(ctx, gameID, telegramID)
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

	b.answerCallback(cb.ID, lz.T(i18n.MsgPlayerKicked))
	b.scheduleGameMessageEdit(game.ID)
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
		label := lz.Tf(i18n.MsgKickGuestLabel, gameformat.PlayerDisplayName(g.InvitedBy))
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

	_, _, removed, err := b.client.KickGuestByID(ctx, gameID, guestID)
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

	b.answerCallback(cb.ID, lz.T(i18n.MsgGuestKicked))
	b.scheduleGameMessageEdit(game.ID)
	b.renderManageScreen(ctx, cb, game, lz)
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

	text, keyboard := formatGamesListMessage(games, groups, lz)
	edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, text)
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = &keyboard
	b.api.Send(edit) //nolint:errcheck
}

// handleManageEditCourts either shows an inline court-toggle keyboard (when the game
// has a venue with configured courts) or falls back to prompting for free-text input.
func (b *Bot) handleManageEditCourts(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	game, ok := b.checkManageAdmin(ctx, cb, gameID, lz)
	if !ok {
		return
	}

	// If the game has a venue with courts, show the inline toggle picker.
	if game.Venue != nil && game.Venue.Courts != "" {
		courts := splitCSV(game.Venue.Courts)
		// Pre-select courts that are already set on the game.
		selected := make(map[string]bool)
		for _, c := range splitCSV(game.Courts) {
			selected[c] = true
		}
		state := &manageCourtsToggleState{
			gameID:         gameID,
			venueCourts:    courts,
			selectedCourts: selected,
		}
		// Clear the free-text state so an earlier pendingCourtsEdit for a different
		// game cannot steal the next text message from this chat.
		b.pendingCourtsEdit.Delete(cb.Message.Chat.ID)
		b.pendingManageCourtsToggle.Store(cb.Message.Chat.ID, state)
		b.answerCallback(cb.ID, "")
		b.renderManageCourtsKeyboard(cb.Message.Chat.ID, cb.Message.MessageID, state, lz)
		return
	}

	// No venue — fall back to free-text input.
	// Clear any active toggle session so its buttons can no longer modify state.
	b.pendingManageCourtsToggle.Delete(cb.Message.Chat.ID)
	b.pendingCourtsEdit.Store(cb.Message.Chat.ID, gameID)
	b.answerCallback(cb.ID, "")

	prompt := tgbotapi.NewMessage(cb.Message.Chat.ID, lz.T(i18n.MsgSendNewCourts))
	b.api.Send(prompt) //nolint:errcheck
}

// renderManageCourtsKeyboard renders the inline toggle keyboard for the courts-update flow.
func (b *Bot) renderManageCourtsKeyboard(chatID int64, messageID int, state *manageCourtsToggleState, lz *i18n.Localizer) {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, court := range state.venueCourts {
		label := court
		if state.selectedCourts[court] {
			label = "✓ " + court
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("manage_court_toggle:%d:%s", state.gameID, court)),
		))
	}

	selected := manageCourtsSelectedString(state)
	confirmLabel := lz.Tf(i18n.MsgNewGameConfirmCourts, selected)
	if selected == "" {
		confirmLabel = lz.T(i18n.MsgNewGameSelectCourts)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(confirmLabel, fmt.Sprintf("manage_court_confirm:%d", state.gameID)),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editText(chatID, messageID, lz.T(i18n.MsgNewGameSelectCourts), &keyboard)
}

// manageCourtsSelectedString returns a comma-separated string of selected courts
// from state.venueCourts in their original order.
func manageCourtsSelectedString(state *manageCourtsToggleState) string {
	var parts []string
	for _, c := range state.venueCourts {
		if state.selectedCourts[c] {
			parts = append(parts, c)
		}
	}
	return strings.Join(parts, ",")
}

// handleManageCourtsToggle toggles a court in the manage-courts inline picker.
// rawID is "<gameID>:<court>".
func (b *Bot) handleManageCourtsToggle(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	subparts := strings.SplitN(rawID, ":", 2)
	if len(subparts) != 2 {
		slog.Debug("invalid rawID in manage_court_toggle", "data", cb.Data)
		b.answerCallback(cb.ID, "")
		return
	}
	gameID, err := strconv.ParseInt(subparts[0], 10, 64)
	if err != nil {
		slog.Debug("invalid game_id in manage_court_toggle", "data", cb.Data)
		b.answerCallback(cb.ID, "")
		return
	}
	court := subparts[1]

	raw, ok := b.pendingManageCourtsToggle.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	state := raw.(*manageCourtsToggleState)

	// Reject presses from an older message whose session has already been replaced.
	if state.gameID != gameID {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}

	if state.selectedCourts == nil {
		state.selectedCourts = make(map[string]bool)
	}
	state.selectedCourts[court] = !state.selectedCourts[court]
	b.pendingManageCourtsToggle.Store(cb.Message.Chat.ID, state)
	b.answerCallback(cb.ID, "")

	b.renderManageCourtsKeyboard(cb.Message.Chat.ID, cb.Message.MessageID, state, lz)
}

// handleManageCourtsConfirm confirms the court selection and updates the game.
func (b *Bot) handleManageCourtsConfirm(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	raw, ok := b.pendingManageCourtsToggle.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	state := raw.(*manageCourtsToggleState)

	// Guard: the confirm callback's gameID must match the stored state to prevent
	// replaying a stale callback from a previous session.
	if state.gameID != gameID {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}

	courts := manageCourtsSelectedString(state)
	if courts == "" {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNewGameNoCourtsSelected))
		return
	}

	// Re-verify admin status before persisting changes.
	game, ok2 := b.checkManageAdmin(ctx, cb, gameID, lz)
	if !ok2 {
		return // checkManageAdmin already answered the callback
	}

	b.pendingManageCourtsToggle.Delete(cb.Message.Chat.ID)
	b.answerCallback(cb.ID, "")

	if err := b.client.UpdateCourts(ctx, gameID, game.ChatID, courts, actorDisplayFrom(cb.From), cb.From.ID); err != nil {
		slog.Error("handleManageCourtsConfirm: update courts", "err", err, "game_id", gameID)
		b.sendText(cb.Message.Chat.ID, lz.T(i18n.MsgFailedUpdateCourts), nil)
		return
	}

	slog.Info("Courts updated via toggle", "game_id", gameID, "courts", courts)

	b.scheduleGameMessageEdit(gameID)
	b.sendText(cb.Message.Chat.ID, lz.Tf(i18n.MsgCourtsUpdated, courts), nil)
}

// processCourtsEdit handles the admin's text response after clicking "Edit Courts".
func (b *Bot) processCourtsEdit(ctx context.Context, msg *tgbotapi.Message, gameID int64) {
	lz := b.userLocalizer(msg.From.LanguageCode)
	courts := strings.TrimSpace(msg.Text)
	if courts == "" {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgInvalidCourtsFormat))
		return
	}

	// Validate: must be non-empty comma-separated values within length limit.
	if len(courts) > maxCourtsLen {
		b.reply(msg.Chat.ID, msg.MessageID, lz.Tf(i18n.MsgCourtsStringTooLong, maxCourtsLen))
		return
	}
	parts := strings.Split(courts, ",")
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgInvalidCourtsFormat))
			return
		}
	}

	// Re-fetch the game to get the chat ID needed for the admin check.
	game, err := b.client.GetGameByID(ctx, gameID)
	if err != nil {
		slog.Error("processCourtsEdit: get game", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgGameNotFoundPeriod))
		return
	}

	// Re-verify admin status before persisting changes.
	isAdmin, err := b.isAdminInGroup(msg.From.ID, game.ChatID)
	if err != nil {
		slog.Error("processCourtsEdit: check admin", "err", err, "user_id", msg.From.ID, "chat_id", game.ChatID)
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgFailedVerifyPermissionsPeriod))
		return
	}
	if !isAdmin {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgLostAdminAccessPeriod))
		return
	}

	if err := b.client.UpdateCourts(ctx, gameID, game.ChatID, courts, actorDisplayFrom(msg.From), msg.From.ID); err != nil {
		slog.Error("processCourtsEdit: update courts", "err", err, "game_id", gameID)
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgFailedUpdateCourts))
		return
	}

	slog.Info("Courts updated", "game_id", gameID, "courts", courts)

	b.scheduleGameMessageEdit(gameID)
	b.reply(msg.Chat.ID, msg.MessageID, lz.Tf(i18n.MsgCourtsUpdated, courts))
}

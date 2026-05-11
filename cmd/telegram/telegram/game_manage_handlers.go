package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/cmd/telegram/client"
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

	var rows [][]tgbotapi.InlineKeyboardButton
	if game.MessageID == nil {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnPublishGame), fmt.Sprintf("publish_game:%d", game.ID)),
		))
	}
	rows = append(rows,
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
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)

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

	_, _, removed, err := b.client.KickPlayer(ctx, gameID, telegramID, game.ChatID, cb.From.ID, actorDisplayFrom(cb.From))
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

	_, _, removed, err := b.client.KickGuestByID(ctx, gameID, guestID, game.ChatID, cb.From.ID, actorDisplayFrom(cb.From))
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

// handleManageEditCourts either shows a mode chooser (auto-book vs manual) when the
// game's venue has auto-booking ready, shows the inline court-toggle keyboard for a
// venue with courts, or falls back to free-text input when there is no venue.
func (b *Bot) handleManageEditCourts(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	game, ok := b.checkManageAdmin(ctx, cb, gameID, lz)
	if !ok {
		return
	}

	// If the game has a venue, check whether auto-booking is available.
	if game.Venue != nil && game.Venue.AutoBookingEnabled {
		readiness, err := b.client.GetVenueBookingReadiness(ctx, game.Venue.ID, game.ChatID)
		if err != nil {
			slog.Warn("handleManageEditCourts: booking readiness check failed", "err", err, "venue_id", game.Venue.ID)
			// Fall through to manual picker on error — don't block the admin.
		} else if readiness.Ready && readiness.MaxCourts > 0 {
			b.pendingManageCourtsToggle.Delete(cb.Message.Chat.ID)
			b.pendingCourtsEdit.Delete(cb.Message.Chat.ID)
			b.pendingManageBookCount.Store(cb.Message.Chat.ID, &manageBookCountState{
				gameID:  gameID,
				groupID: game.ChatID,
				max:     readiness.MaxCourts,
			})
			b.answerCallback(cb.ID, "")
			b.renderCourtsModePicker(cb.Message.Chat.ID, cb.Message.MessageID, gameID, readiness.MaxCourts, lz)
			return
		}
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

// renderCourtsModePicker edits the message to show Auto-book vs Manual selection.
func (b *Bot) renderCourtsModePicker(chatID int64, messageID int, gameID int64, maxCourts int, lz *i18n.Localizer) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				lz.T(i18n.BtnEditCourtsAutoBook),
				fmt.Sprintf("manage_courts_mode:%d:auto", gameID),
			),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				lz.T(i18n.BtnEditCourtsManual),
				fmt.Sprintf("manage_courts_mode:%d:manual", gameID),
			),
		),
	)
	b.editText(chatID, messageID, lz.T(i18n.MsgEditCourtsChooseMode), &keyboard)
}

// handleManageCourtsMode handles the manage_courts_mode:<gameID>:<mode> callback.
// mode is "auto" or "manual".
func (b *Bot) handleManageCourtsMode(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	sub := strings.SplitN(rawID, ":", 2)
	if len(sub) != 2 {
		b.answerCallback(cb.ID, "")
		return
	}
	gameID, err := strconv.ParseInt(sub[0], 10, 64)
	if err != nil {
		b.answerCallback(cb.ID, "")
		return
	}
	mode := sub[1]

	game, ok := b.checkManageAdmin(ctx, cb, gameID, lz)
	if !ok {
		return
	}

	if mode == "auto" {
		raw, exists := b.pendingManageBookCount.Load(cb.Message.Chat.ID)
		if !exists {
			b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
			return
		}
		state := raw.(*manageBookCountState)
		if state.gameID != gameID {
			b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
			return
		}
		b.answerCallback(cb.ID, "")
		b.renderBookCountKeyboard(cb.Message.Chat.ID, cb.Message.MessageID, state, lz)
		return
	}

	// Manual mode: show toggle keyboard or free-text.
	b.pendingManageBookCount.Delete(cb.Message.Chat.ID)
	if game.Venue != nil && game.Venue.Courts != "" {
		courts := splitCSV(game.Venue.Courts)
		selected := make(map[string]bool)
		for _, c := range splitCSV(game.Courts) {
			selected[c] = true
		}
		toggleState := &manageCourtsToggleState{
			gameID:         gameID,
			venueCourts:    courts,
			selectedCourts: selected,
		}
		b.pendingCourtsEdit.Delete(cb.Message.Chat.ID)
		b.pendingManageCourtsToggle.Store(cb.Message.Chat.ID, toggleState)
		b.answerCallback(cb.ID, "")
		b.renderManageCourtsKeyboard(cb.Message.Chat.ID, cb.Message.MessageID, toggleState, lz)
		return
	}
	b.pendingManageCourtsToggle.Delete(cb.Message.Chat.ID)
	b.pendingCourtsEdit.Store(cb.Message.Chat.ID, gameID)
	b.answerCallback(cb.ID, "")
	prompt := tgbotapi.NewMessage(cb.Message.Chat.ID, lz.T(i18n.MsgSendNewCourts))
	b.api.Send(prompt) //nolint:errcheck
}

// renderBookCountKeyboard shows inline buttons 1…max for choosing how many courts to book.
func (b *Bot) renderBookCountKeyboard(chatID int64, messageID int, state *manageBookCountState, lz *i18n.Localizer) {
	var rows [][]tgbotapi.InlineKeyboardButton
	for n := 1; n <= state.max; n++ {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("%d", n),
				fmt.Sprintf("manage_book:%d:%d", state.gameID, n),
			),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(
			lz.T(i18n.BtnBookCancel),
			fmt.Sprintf("manage_book_cancel:%d", state.gameID),
		),
	))
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editText(chatID, messageID, lz.T(i18n.MsgBookCountPrompt), &keyboard)
}

// handleManageBook handles manage_book:<gameID>:<count> — triggers auto-booking.
func (b *Bot) handleManageBook(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	sub := strings.SplitN(rawID, ":", 2)
	if len(sub) != 2 {
		b.answerCallback(cb.ID, "")
		return
	}
	gameID, err1 := strconv.ParseInt(sub[0], 10, 64)
	count, err2 := strconv.ParseInt(sub[1], 10, 64)
	if err1 != nil || err2 != nil || count <= 0 {
		b.answerCallback(cb.ID, "")
		return
	}

	raw, exists := b.pendingManageBookCount.Load(cb.Message.Chat.ID)
	if !exists {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	state := raw.(*manageBookCountState)
	if state.gameID != gameID {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}

	isAdmin, err := b.isAdminInGroup(cb.From.ID, state.groupID)
	if err != nil {
		slog.Error("handleManageBook: check admin", "err", err, "user_id", cb.From.ID, "group_id", state.groupID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgFailedVerifyPermissions))
		return
	}
	if !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgLostAdminAccess))
		return
	}

	b.pendingManageBookCount.Delete(cb.Message.Chat.ID)
	b.answerCallback(cb.ID, "")

	result, err := b.client.BookGameCourts(ctx, gameID, state.groupID, cb.From.ID, actorDisplayFrom(cb.From), int(count))
	if err != nil {
		slog.Error("handleManageBook: book courts", "err", err, "game_id", gameID)
		b.sendText(cb.Message.Chat.ID, lz.T(i18n.MsgSomethingWentWrong), nil)
		return
	}

	slog.Info("Auto-booked courts on demand", "game_id", gameID, "requested", result.Requested,
		"booked", result.BookedCount, "labels", result.BookedLabels)

	b.scheduleGameMessageEdit(gameID)

	switch {
	case result.BookedCount == 0:
		b.sendText(cb.Message.Chat.ID, lz.T(i18n.MsgBookNoneBooked), nil)
	case result.BookedCount < result.Requested:
		b.sendText(cb.Message.Chat.ID,
			lz.Tf(i18n.MsgBookPartial, result.BookedCount, result.Requested, strings.Join(result.BookedLabels, ", ")), nil)
	default:
		b.sendText(cb.Message.Chat.ID,
			lz.Tf(i18n.MsgBookSuccess, result.BookedCount, strings.Join(result.BookedLabels, ", ")), nil)
	}
}

// handleManageBookCancel cancels the count-picker and returns to the manage screen.
func (b *Bot) handleManageBookCancel(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	b.pendingManageBookCount.Delete(cb.Message.Chat.ID)
	b.answerCallback(cb.ID, lz.T(i18n.MsgBookCanceled))
	game, ok := b.checkManageAdmin(ctx, cb, gameID, lz)
	if !ok {
		return
	}
	b.renderManageScreen(ctx, cb, game, lz)
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
// When courts being removed have active Eversports bookings, shows a cancel-or-back prompt first.
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

	// Pre-flight: check for active bookings on courts that are being removed.
	removedCourts := courtsBeingRemoved(game.Courts, courts)
	if len(removedCourts) > 0 {
		bookings, err := b.client.ListActiveCourtBookings(ctx, gameID, removedCourts)
		if err != nil {
			slog.Error("handleManageCourtsConfirm: list active bookings", "err", err, "game_id", gameID)
			b.answerCallback(cb.ID, "")
			b.sendText(cb.Message.Chat.ID, lz.T(i18n.MsgSomethingWentWrong), nil)
			return
		}
		if len(bookings) > 0 {
			// Collect unique booked labels for the prompt.
			seen := make(map[string]bool)
			var bookedLabels []string
			for _, bk := range bookings {
				if !seen[bk.CourtLabel] {
					seen[bk.CourtLabel] = true
					bookedLabels = append(bookedLabels, bk.CourtLabel)
				}
			}
			promptState := &manageCourtsCancelPromptState{
				gameID:       gameID,
				groupID:      game.ChatID,
				newCourts:    courts,
				bookedLabels: bookedLabels,
				venueCourts:  state.venueCourts,
			}
			b.pendingManageCourtsCancelPrompt.Store(cb.Message.Chat.ID, promptState)
			b.answerCallback(cb.ID, "")
			b.renderCourtCancelPrompt(cb.Message.Chat.ID, cb.Message.MessageID, promptState, lz)
			return
		}
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

// courtsBeingRemoved returns court labels present in currentCSV but not in newCSV (multiset diff).
func courtsBeingRemoved(currentCSV, newCSV string) []string {
	current := splitCSV(currentCSV)
	incoming := splitCSV(newCSV)
	incomingCount := make(map[string]int, len(incoming))
	for _, c := range incoming {
		incomingCount[c]++
	}
	var removed []string
	for _, c := range current {
		if incomingCount[c] > 0 {
			incomingCount[c]--
		} else {
			removed = append(removed, c)
		}
	}
	return removed
}

// renderCourtCancelPrompt edits the message to show the cancellation confirmation prompt.
func (b *Bot) renderCourtCancelPrompt(chatID int64, messageID int, state *manageCourtsCancelPromptState, lz *i18n.Localizer) {
	labels := strings.Join(state.bookedLabels, ", ")
	var text string
	if len(state.bookedLabels) == 1 {
		text = lz.Tf(i18n.MsgCourtCancelPromptSingle, labels)
	} else {
		text = lz.Tf(i18n.MsgCourtCancelPromptMulti, labels)
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				lz.T(i18n.BtnCourtCancelConfirm),
				fmt.Sprintf("manage_courts_cancel_confirm:%d", state.gameID),
			),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				lz.T(i18n.BtnCourtCancelAbort),
				fmt.Sprintf("manage_courts_cancel_abort:%d", state.gameID),
			),
		),
	)
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = &keyboard
	b.api.Send(edit) //nolint:errcheck
}

// handleManageCourtsCancelConfirm cancels active bookings for removed courts then updates courts.
func (b *Bot) handleManageCourtsCancelConfirm(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	raw, ok := b.pendingManageCourtsCancelPrompt.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	state := raw.(*manageCourtsCancelPromptState)
	if state.gameID != gameID {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}

	isAdmin, err := b.isAdminInGroup(cb.From.ID, state.groupID)
	if err != nil {
		slog.Error("handleManageCourtsCancelConfirm: check admin", "err", err, "user_id", cb.From.ID, "group_id", state.groupID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgFailedVerifyPermissions))
		return
	}
	if !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgLostAdminAccess))
		return
	}

	b.pendingManageCourtsCancelPrompt.Delete(cb.Message.Chat.ID)
	b.pendingManageCourtsToggle.Delete(cb.Message.Chat.ID)
	b.answerCallback(cb.ID, "")

	canceledLabels, failed, err := b.client.UpdateCourtsAndCancelBookings(ctx, gameID, state.groupID, state.newCourts, actorDisplayFrom(cb.From), cb.From.ID)
	if err != nil {
		slog.Error("handleManageCourtsCancelConfirm: update+cancel", "err", err, "game_id", gameID)
		b.sendText(cb.Message.Chat.ID, lz.T(i18n.MsgFailedUpdateCourts), nil)
		return
	}

	slog.Info("Courts updated with booking cancellation", "game_id", gameID, "courts", state.newCourts,
		"canceled", canceledLabels, "failed", len(failed))

	b.scheduleGameMessageEdit(gameID)

	if len(failed) > 0 {
		var failedLabels []string
		for _, f := range failed {
			failedLabels = append(failedLabels, f.Court)
		}
		b.sendText(cb.Message.Chat.ID, lz.Tf(i18n.MsgCourtCancelPartial, state.newCourts, strings.Join(failedLabels, ", ")), nil)
		return
	}
	b.sendText(cb.Message.Chat.ID, lz.Tf(i18n.MsgCourtCancelSuccess, state.newCourts), nil)
}

// handleManageCourtsCancelAbort discards the cancellation prompt and returns to the court-toggle picker.
func (b *Bot) handleManageCourtsCancelAbort(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	raw, ok := b.pendingManageCourtsCancelPrompt.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	state := raw.(*manageCourtsCancelPromptState)
	if state.gameID != gameID {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}

	b.pendingManageCourtsCancelPrompt.Delete(cb.Message.Chat.ID)
	b.answerCallback(cb.ID, "")

	// Restore toggle state so the admin can adjust the selection.
	selected := make(map[string]bool)
	for _, c := range splitCSV(state.newCourts) {
		selected[c] = true
	}
	toggleState := &manageCourtsToggleState{
		gameID:         gameID,
		venueCourts:    state.venueCourts,
		selectedCourts: selected,
	}
	b.pendingManageCourtsToggle.Store(cb.Message.Chat.ID, toggleState)
	b.renderManageCourtsKeyboard(cb.Message.Chat.ID, cb.Message.MessageID, toggleState, lz)
}

// handleManagePublish publishes an unpublished game from the manage menu.
func (b *Bot) handleManagePublish(ctx context.Context, cb *tgbotapi.CallbackQuery, gameID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	game, ok := b.checkManageAdmin(ctx, cb, gameID, lz)
	if !ok {
		return
	}

	_, err := b.client.PublishGame(ctx, gameID, cb.From.ID, actorDisplayFrom(cb.From))
	if err != nil {
		if errors.Is(err, client.ErrAlreadyPublished) {
			b.answerCallback(cb.ID, lz.T(i18n.MsgPublishAlreadyDone))
		} else {
			slog.Error("handleManagePublish: publish failed", "err", err, "game_id", gameID)
			b.answerCallback(cb.ID, lz.T(i18n.MsgPublishFailed))
		}
		b.renderManageScreen(ctx, cb, game, lz)
		return
	}

	b.answerCallback(cb.ID, lz.T(i18n.MsgGamePublished))

	// Re-fetch the game so the updated MessageID removes the Publish button on re-render.
	updated, err := b.client.GetGameByID(ctx, gameID)
	if err != nil {
		slog.Error("handleManagePublish: re-fetch game", "err", err, "game_id", gameID)
		updated = game
	}
	b.renderManageScreen(ctx, cb, updated, lz)
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

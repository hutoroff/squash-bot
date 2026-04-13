package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/vkhutorov/squash_bot/internal/i18n"
)

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

	// Use Load (not LoadAndDelete) so the pending state survives transient
	// failures (admin check error, no venues) and the group-picker keyboard
	// remains usable for a retry.
	raw, ok := b.pendingGames.Load(key)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	pg := raw.(*pendingGame)

	// Re-verify admin status at callback time to prevent replay attacks.
	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNotAdminInGroup))
		return // pendingGames intact — keyboard still usable
	}

	// Venue is required for each group. Check before consuming the pending state.
	venues, err := b.client.GetVenuesByGroup(ctx, groupID)
	if err != nil {
		slog.Error("handleGroupSelection: fetch venues", "err", err)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return // pendingGames intact — keyboard still usable
	}
	if len(venues) == 0 {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNewGameNoVenuesConfigured))
		return // pendingGames intact — keyboard still usable
	}

	// All checks passed — consume the pending state and proceed.
	b.pendingGames.Delete(key)
	b.answerCallback(cb.ID, "")

	if len(venues) == 1 {
		// Auto-select the only venue.
		venueID := venues[0].ID
		pg.venueID = &venueID
		emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
		editSel := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgCreatingGame))
		editSel.ReplyMarkup = &emptyKeyboard
		b.api.Send(editSel) //nolint:errcheck
		b.createAndAnnounceGame(ctx, pg.replyChatID, pg.replyMsgID, groupID, pg.gameDate, pg.courts, pg.venueID, lz)
		return
	}

	// Multiple venues — show venue picker; state saved keyed by private chat ID.
	b.pendingGroupVenuePick.Store(cb.Message.Chat.ID, &groupVenuePickState{
		groupID:     groupID,
		gameDate:    pg.gameDate,
		courts:      pg.courts,
		replyChatID: pg.replyChatID,
		replyMsgID:  pg.replyMsgID,
	})
	localDate := pg.gameDate.In(b.loc)
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, v := range venues {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(v.Name, fmt.Sprintf("ng_gvenue:%d", v.ID)),
		))
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	editSel := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID,
		lz.Tf(i18n.MsgNewGameSelectVenue, lz.FormatGameDate(localDate)))
	editSel.ReplyMarkup = &keyboard
	b.api.Send(editSel) //nolint:errcheck
}

// handleNewGameGroupVenue handles ng_gvenue:<venueID> callbacks.
// It is used by multi-group admins who have already selected a group and are
// now choosing a venue from that group's venue list.
func (b *Bot) handleNewGameGroupVenue(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	raw, ok := b.pendingGroupVenuePick.LoadAndDelete(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	state := raw.(*groupVenuePickState)

	venueID, err := parseInt64(rawID)
	if err != nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	// Verify the venue belongs to the selected group (prevents callback forgery).
	venue, err := b.client.GetVenueByID(ctx, venueID)
	if err != nil || venue.GroupID != state.groupID {
		b.answerCallback(cb.ID, lz.T(i18n.MsgVenueNotFound))
		return
	}

	b.answerCallback(cb.ID, "")

	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	editSel := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgCreatingGame))
	editSel.ReplyMarkup = &emptyKeyboard
	b.api.Send(editSel) //nolint:errcheck

	b.createAndAnnounceGame(ctx, state.replyChatID, state.replyMsgID, state.groupID, state.gameDate, state.courts, &venueID, lz)
}

func (b *Bot) createAndAnnounceGame(ctx context.Context, replyChatID int64, replyMsgID int, groupID int64, gameDate time.Time, courts string, venueID *int64, userLz *i18n.Localizer) {
	game, err := b.client.CreateGame(ctx, groupID, gameDate, courts, venueID)
	if err != nil {
		slog.Error("create game", "err", err)
		b.reply(replyChatID, replyMsgID, userLz.T(i18n.MsgFailedCreateGame))
		return
	}

	// Use the group's language and timezone for the public announcement.
	groupLz := b.groupLocalizer(ctx, groupID)
	groupLoc := b.groupLocation(ctx, groupID)
	msgText := FormatGameMessage(game, nil, nil, groupLoc, time.Now(), groupLz)
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

// buildDateSelectionKeyboard returns an inline keyboard with the next 14 days (2 per row).
// loc determines which timezone is used to compute "today"; use the group's timezone when known.
func (b *Bot) buildDateSelectionKeyboard(lz *i18n.Localizer, loc *time.Location) tgbotapi.InlineKeyboardMarkup {
	today := time.Now().In(loc)
	var rows [][]tgbotapi.InlineKeyboardButton
	for i := 0; i < 14; i += 2 {
		var row []tgbotapi.InlineKeyboardButton
		for j := 0; j < 2; j++ {
			date := today.AddDate(0, 0, i+j)
			label := lz.ShortWeekday(date.Weekday()) + " " + date.Format("02.01")
			dateStr := date.Format("2006-01-02")
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(label, "ng_date:"+dateStr))
		}
		rows = append(rows, row)
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// handleNewGameDate handles the ng_date:<YYYY-MM-DD> callback from the date picker.
// It stores the selected date in the wizard state and prompts for time or venue.
func (b *Bot) handleNewGameDate(ctx context.Context, cb *tgbotapi.CallbackQuery, dateStr string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	// Re-verify admin eligibility at callback time.
	adminGroupIDs := b.adminGroups(cb.From.ID)
	if len(adminGroupIDs) == 0 {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminCreateGames))
		return
	}

	// For single-group admins, venue selection is mandatory and can be auto-skipped
	// when exactly one venue exists. Multi-group admins use manual time entry because
	// the target group (and therefore its venues) is unknown until courts are entered.
	if len(adminGroupIDs) == 1 {
		venues, err := b.client.GetVenuesByGroup(ctx, adminGroupIDs[0])
		if err != nil {
			slog.Error("handleNewGameDate: fetch venues", "err", err)
			b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
			return
		}
		if len(venues) == 0 {
			b.answerCallback(cb.ID, lz.T(i18n.MsgNewGameNoVenuesConfigured))
			return
		}
		// Parse date in the group's timezone so the calendar date is correct.
		loc := b.groupLocation(ctx, adminGroupIDs[0])
		date, err := time.ParseInLocation("2006-01-02", dateStr, loc)
		if err != nil {
			slog.Debug("handleNewGameDate: invalid date in callback", "data", dateStr)
			b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
			return
		}
		if len(venues) == 1 {
			// Auto-select the only venue — skip the picker entirely.
			v := venues[0]
			venueID := v.ID
			wizard := &newGameWizard{
				gameDate:          date,
				dateStr:           dateStr,
				loc:               loc,
				step:              wizardStepCourtPick,
				venueID:           &venueID,
				venueCourts:       splitCSV(v.Courts),
				selectedCourts:    make(map[string]bool),
				timeSlots:         splitCSV(v.TimeSlots),
				preferredGameTime: v.PreferredGameTime,
			}
			b.pendingNewGameWizard.Store(cb.Message.Chat.ID, wizard)
			b.answerCallback(cb.ID, "")
			b.renderCourtPickKeyboard(cb.Message.Chat.ID, cb.Message.MessageID, wizard, lz)
			return
		}
		// Multiple venues — show picker without a manual fallback option.
		b.pendingNewGameWizard.Store(cb.Message.Chat.ID, &newGameWizard{
			gameDate: date,
			dateStr:  dateStr,
			loc:      loc,
			step:     wizardStepVenue,
		})
		b.answerCallback(cb.ID, "")
		localDate := date.In(loc)
		var rows [][]tgbotapi.InlineKeyboardButton
		for _, v := range venues {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(v.Name, fmt.Sprintf("ng_venue:%d", v.ID)),
			))
		}
		keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
		prompt := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID,
			lz.Tf(i18n.MsgNewGameSelectVenue, lz.FormatGameDate(localDate)))
		prompt.ReplyMarkup = &keyboard
		b.api.Send(prompt) //nolint:errcheck
		return
	}

	// Multiple groups — group timezone is unknown until the admin picks a group.
	// Parse with the bot's fallback timezone for now; handleNewGameGroup will
	// re-parse dateStr once the group (and its timezone) is known.
	date, err := time.ParseInLocation("2006-01-02", dateStr, b.loc)
	if err != nil {
		slog.Debug("handleNewGameDate: invalid date in callback", "data", dateStr)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	// Multiple groups → ask which group to create the game in first.
	b.pendingNewGameWizard.Store(cb.Message.Chat.ID, &newGameWizard{
		gameDate: date,
		dateStr:  dateStr,
		step:     wizardStepGroup,
	})
	b.answerCallback(cb.ID, "")

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, gid := range adminGroupIDs {
		title := fmt.Sprintf("Group %d", gid)
		chatInfo, err := b.api.GetChat(tgbotapi.ChatInfoConfig{
			ChatConfig: tgbotapi.ChatConfig{ChatID: gid},
		})
		if err == nil && chatInfo.Title != "" {
			title = chatInfo.Title
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(title, fmt.Sprintf("ng_group:%d", gid)),
		))
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	prompt := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID,
		lz.T(i18n.MsgWhichGroup))
	prompt.ReplyMarkup = &keyboard
	b.api.Send(prompt) //nolint:errcheck
}

// handleNewGameGroup handles ng_group:<groupID> callbacks.
// It is used by multi-group admins who have selected a date and are now picking
// which group the game should be posted in.
func (b *Bot) handleNewGameGroup(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	raw, ok := b.pendingNewGameWizard.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wizard := raw.(*newGameWizard)

	// Reject out-of-order callbacks — only valid when the wizard is waiting for group input.
	if wizard.step != wizardStepGroup {
		b.answerCallback(cb.ID, "")
		return
	}

	groupID, err := parseInt64(rawID)
	if err != nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	// Re-verify admin status at callback time to prevent replay attacks.
	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNotAdminInGroup))
		return // wizard intact — keyboard still usable
	}

	// Venue is required for each group. Fail if none configured for the selected group.
	venues, err := b.client.GetVenuesByGroup(ctx, groupID)
	if err != nil {
		slog.Error("handleNewGameGroup: fetch venues", "err", err)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return // wizard intact — keyboard still usable
	}
	if len(venues) == 0 {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNewGameNoVenuesConfigured))
		return // wizard intact — keyboard still usable (admin can pick a different group)
	}

	wizard.groupID = groupID

	// Now that the group is known, load its timezone and re-parse the date so the
	// calendar date is correct for that group's local time, regardless of what
	// timezone the date picker was built with.
	loc := b.groupLocation(ctx, groupID)
	wizard.loc = loc
	if wizard.dateStr != "" {
		if reparsed, err := time.ParseInLocation("2006-01-02", wizard.dateStr, loc); err == nil {
			wizard.gameDate = reparsed
		}
	}

	b.answerCallback(cb.ID, "")

	if len(venues) == 1 {
		// Auto-select the only venue — skip the picker entirely.
		v := venues[0]
		venueID := v.ID
		wizard.venueID = &venueID
		wizard.venueCourts = splitCSV(v.Courts)
		wizard.selectedCourts = make(map[string]bool)
		wizard.timeSlots = splitCSV(v.TimeSlots)
		wizard.preferredGameTime = v.PreferredGameTime
		wizard.step = wizardStepCourtPick
		b.pendingNewGameWizard.Store(cb.Message.Chat.ID, wizard)
		b.renderCourtPickKeyboard(cb.Message.Chat.ID, cb.Message.MessageID, wizard, lz)
		return
	}

	// Multiple venues — show venue picker.
	// Clear any stale venue-specific state so a forged ng_venue callback cannot
	// reuse data from a previously visited group.
	wizard.step = wizardStepVenue
	wizard.venueID = nil
	wizard.venueCourts = nil
	wizard.selectedCourts = nil
	wizard.timeSlots = nil
	b.pendingNewGameWizard.Store(cb.Message.Chat.ID, wizard)
	localDate := wizard.gameDate.In(loc)
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, v := range venues {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(v.Name, fmt.Sprintf("ng_venue:%d", v.ID)),
		))
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	prompt := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID,
		lz.Tf(i18n.MsgNewGameSelectVenue, lz.FormatGameDate(localDate)))
	prompt.ReplyMarkup = &keyboard
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
	case wizardStepGroup, wizardStepVenue, wizardStepCourtPick:
		// These steps use inline keyboard callbacks; text input is ignored.
	}
}

// processNewGameWizardTime parses the user's time input and advances to courts step.
func (b *Bot) processNewGameWizardTime(ctx context.Context, msg *tgbotapi.Message, wizard *newGameWizard, lz *i18n.Localizer) {
	t, err := time.Parse("15:04", strings.TrimSpace(msg.Text))
	if err != nil {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgNewGameInvalidTime))
		return // keep wizard state so the user can retry
	}

	loc := b.wizardLoc(wizard)
	d := wizard.gameDate.In(loc)
	gameDate := time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), 0, 0, loc)
	if !gameDate.After(time.Now()) {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgNewGameTimePast))
		return // keep wizard state so the user can retry
	}

	wizard.gameDate = gameDate

	// If courts were already chosen in the court-toggle step (venue-backed path),
	// skip the free-text courts prompt and create the game immediately — same
	// logic as handleNewGameTimeSlot.
	courts := selectedCourtsString(wizard)
	if courts != "" {
		if wizard.groupID != 0 {
			isAdmin, err := b.isAdminInGroup(msg.From.ID, wizard.groupID)
			if err != nil || !isAdmin {
				b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgNotAdminInGroup))
				return // wizard intact — user can retry
			}
			b.pendingNewGameWizard.Delete(msg.Chat.ID)
			b.createAndAnnounceGame(ctx, msg.Chat.ID, msg.MessageID, wizard.groupID, gameDate, courts, wizard.venueID, lz)
			return
		}
		b.pendingNewGameWizard.Delete(msg.Chat.ID)
		adminGroupIDs := b.adminGroups(msg.From.ID)
		if len(adminGroupIDs) == 0 {
			b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgOnlyAdminCreateGames))
			return
		}
		if len(adminGroupIDs) == 1 {
			b.createAndAnnounceGame(ctx, msg.Chat.ID, msg.MessageID, adminGroupIDs[0], gameDate, courts, wizard.venueID, lz)
			return
		}
		// Multiple groups, no pre-selection.
		key := pendingGameKey{chatID: msg.Chat.ID, messageID: msg.MessageID}
		b.pendingGames.Store(key, &pendingGame{
			gameDate:    gameDate,
			courts:      courts,
			venueID:     wizard.venueID,
			replyChatID: msg.Chat.ID,
			replyMsgID:  msg.MessageID,
		})
		keyboard := b.buildGroupSelectionKeyboard(adminGroupIDs, key)
		selMsg := tgbotapi.NewMessage(msg.Chat.ID, lz.T(i18n.MsgWhichGroup))
		selMsg.ReplyMarkup = keyboard
		if _, err := b.api.Send(selMsg); err != nil {
			slog.Error("processNewGameWizardTime: send group selection", "err", err)
			b.pendingGames.Delete(key)
		}
		return
	}

	// No courts pre-selected (no-venue path) — ask the admin to type them.
	wizard.step = wizardStepCourts
	b.pendingNewGameWizard.Store(msg.Chat.ID, wizard)

	localDate := gameDate.In(b.wizardLoc(wizard))
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

	// If the group was already chosen during the wizard (multi-group admin flow),
	// re-verify admin status before consuming the wizard state and creating the game.
	// Group membership is dynamic, so the admin could have been removed since the
	// group was selected.
	if wizard.groupID != 0 {
		isAdmin, err := b.isAdminInGroup(msg.From.ID, wizard.groupID)
		if err != nil || !isAdmin {
			b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgNotAdminInGroup))
			return // wizard intact — user can retry courts input
		}
		b.pendingNewGameWizard.Delete(msg.Chat.ID)
		b.createAndAnnounceGame(ctx, msg.Chat.ID, msg.MessageID, wizard.groupID, wizard.gameDate, courts, wizard.venueID, lz)
		return
	}

	b.pendingNewGameWizard.Delete(msg.Chat.ID)

	adminGroupIDs := b.adminGroups(msg.From.ID)
	if len(adminGroupIDs) == 0 {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgOnlyAdminCreateGames))
		return
	}

	if len(adminGroupIDs) == 1 {
		b.createAndAnnounceGame(ctx, msg.Chat.ID, msg.MessageID, adminGroupIDs[0], wizard.gameDate, courts, wizard.venueID, lz)
		return
	}

	// Admin manages multiple groups with no pre-selection — ask which one to post to.
	key := pendingGameKey{chatID: msg.Chat.ID, messageID: msg.MessageID}
	b.pendingGames.Store(key, &pendingGame{
		gameDate:    wizard.gameDate,
		courts:      courts,
		venueID:     wizard.venueID,
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

// handleNewGameVenue handles ng_venue:<venueID> or ng_venue:none callbacks.
func (b *Bot) handleNewGameVenue(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	raw, ok := b.pendingNewGameWizard.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wizard := raw.(*newGameWizard)

	// Reject out-of-order callbacks — only valid when the wizard is waiting for venue input.
	if wizard.step != wizardStepVenue {
		b.answerCallback(cb.ID, "")
		return
	}

	venueID, err := parseInt64(rawID)
	if err != nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	venue, err := b.client.GetVenueByID(ctx, venueID)
	if err != nil {
		slog.Error("handleNewGameVenue: get venue", "err", err)
		b.answerCallback(cb.ID, lz.T(i18n.MsgVenueNotFound))
		return
	}

	// If a group was pre-selected (multi-group admin flow), verify the venue belongs to it.
	if wizard.groupID != 0 && venue.GroupID != wizard.groupID {
		b.answerCallback(cb.ID, lz.T(i18n.MsgVenueNotFound))
		return
	}

	wizard.venueID = &venueID
	wizard.venueCourts = splitCSV(venue.Courts)
	wizard.selectedCourts = make(map[string]bool)
	wizard.timeSlots = splitCSV(venue.TimeSlots)
	wizard.preferredGameTime = venue.PreferredGameTime
	wizard.step = wizardStepCourtPick
	b.pendingNewGameWizard.Store(cb.Message.Chat.ID, wizard)
	b.answerCallback(cb.ID, "")

	b.renderCourtPickKeyboard(cb.Message.Chat.ID, cb.Message.MessageID, wizard, lz)
}

func (b *Bot) renderCourtPickKeyboard(chatID int64, messageID int, wizard *newGameWizard, lz *i18n.Localizer) {
	venueID := int64(0)
	if wizard.venueID != nil {
		venueID = *wizard.venueID
	}
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, court := range wizard.venueCourts {
		label := court
		if wizard.selectedCourts[court] {
			label = "✓ " + court
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("ng_court_toggle:%d:%s", venueID, court)),
		))
	}

	// Confirm button shows selected courts or a placeholder.
	selected := selectedCourtsString(wizard)
	confirmLabel := lz.Tf(i18n.MsgNewGameConfirmCourts, selected)
	if selected == "" {
		confirmLabel = lz.T(i18n.MsgNewGameSelectCourts)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(confirmLabel, fmt.Sprintf("ng_court_confirm:%d", venueID)),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editText(chatID, messageID, lz.T(i18n.MsgNewGameSelectCourts), &keyboard)
}

// handleNewGameCourtToggle toggles a court in the new-game wizard court picker.
// rawID is "<venueID>:<court>".
func (b *Bot) handleNewGameCourtToggle(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	subparts := strings.SplitN(rawID, ":", 2)
	if len(subparts) != 2 {
		slog.Debug("invalid rawID in ng_court_toggle", "data", cb.Data)
		b.answerCallback(cb.ID, "")
		return
	}
	venueID, err := strconv.ParseInt(subparts[0], 10, 64)
	if err != nil {
		slog.Debug("invalid venue_id in ng_court_toggle", "data", cb.Data)
		b.answerCallback(cb.ID, "")
		return
	}
	court := subparts[1]

	raw, ok := b.pendingNewGameWizard.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wizard := raw.(*newGameWizard)

	// Reject presses from an older message whose venue/session has been replaced.
	wizardVenueID := int64(0)
	if wizard.venueID != nil {
		wizardVenueID = *wizard.venueID
	}
	if wizardVenueID != venueID {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}

	if wizard.selectedCourts == nil {
		wizard.selectedCourts = make(map[string]bool)
	}
	wizard.selectedCourts[court] = !wizard.selectedCourts[court]
	b.pendingNewGameWizard.Store(cb.Message.Chat.ID, wizard)
	b.answerCallback(cb.ID, "")

	b.renderCourtPickKeyboard(cb.Message.Chat.ID, cb.Message.MessageID, wizard, lz)
}

// handleNewGameCourtConfirm confirms the court selection in the new-game wizard.
// rawID is "<venueID>" — validated against the current wizard session.
func (b *Bot) handleNewGameCourtConfirm(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	venueID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		slog.Debug("invalid venue_id in ng_court_confirm", "data", cb.Data)
		b.answerCallback(cb.ID, "")
		return
	}

	raw, ok := b.pendingNewGameWizard.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wizard := raw.(*newGameWizard)

	// Reject confirms from an older message whose venue/session has been replaced.
	wizardVenueID := int64(0)
	if wizard.venueID != nil {
		wizardVenueID = *wizard.venueID
	}
	if wizardVenueID != venueID {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}

	courts := selectedCourtsString(wizard)
	if courts == "" {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNewGameNoCourtsSelected))
		return
	}

	b.answerCallback(cb.ID, "")

	// Advance to time step.
	wizard.step = wizardStepTime
	b.pendingNewGameWizard.Store(cb.Message.Chat.ID, wizard)

	localDate := wizard.gameDate.In(b.loc)
	if len(wizard.timeSlots) > 0 {
		// Show venue time slots as buttons.
		b.renderTimeSlotKeyboard(cb.Message.Chat.ID, cb.Message.MessageID, wizard, lz)
	} else {
		// No time slots — fall back to free-text time input.
		prompt := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID,
			lz.Tf(i18n.MsgNewGameEnterTime, lz.FormatGameDate(localDate)))
		emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
		prompt.ReplyMarkup = &emptyKeyboard
		b.api.Send(prompt) //nolint:errcheck
	}
}

func (b *Bot) renderTimeSlotKeyboard(chatID int64, messageID int, wizard *newGameWizard, lz *i18n.Localizer) {
	localDate := wizard.gameDate.In(b.loc)
	text := lz.Tf(i18n.MsgNewGameSelectTime, lz.FormatGameDate(localDate), selectedCourtsString(wizard))

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, slot := range wizard.timeSlots {
		label := slot
		if slot == wizard.preferredGameTime {
			label = "⭐ " + slot
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("ng_timeslot:%s", slot)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.MsgNewGameCustomTime), "ng_time_custom:_"),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editText(chatID, messageID, text, &keyboard)
}

func (b *Bot) handleNewGameTimeSlot(ctx context.Context, cb *tgbotapi.CallbackQuery, timeSlot string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	raw, ok := b.pendingNewGameWizard.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wizard := raw.(*newGameWizard)

	t, err := time.Parse("15:04", strings.TrimSpace(timeSlot))
	if err != nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNewGameInvalidTime))
		return
	}

	loc := b.wizardLoc(wizard)
	d := wizard.gameDate.In(loc)
	gameDate := time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), 0, 0, loc)
	if !gameDate.After(time.Now()) {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNewGameTimePast))
		return
	}

	wizard.gameDate = gameDate
	courts := selectedCourtsString(wizard)

	// If the group was already chosen during the wizard (multi-group admin flow),
	// re-verify admin status before consuming the wizard state and creating the game.
	// Group membership is dynamic, so the admin could have been removed since the
	// group was selected. Check here, while the wizard/keyboard are still intact,
	// so a failure can surface as a toast without leaving the UI in a broken state.
	if wizard.groupID != 0 {
		isAdmin, err := b.isAdminInGroup(cb.From.ID, wizard.groupID)
		if err != nil || !isAdmin {
			b.answerCallback(cb.ID, lz.T(i18n.MsgNotAdminInGroup))
			return // wizard intact — keyboard still usable
		}
		b.pendingNewGameWizard.Delete(cb.Message.Chat.ID)
		b.answerCallback(cb.ID, "")
		emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgCreatingGame))
		edit.ReplyMarkup = &emptyKeyboard
		b.api.Send(edit) //nolint:errcheck
		b.createAndAnnounceGame(ctx, cb.Message.Chat.ID, cb.Message.MessageID, wizard.groupID, gameDate, courts, wizard.venueID, lz)
		return
	}

	b.pendingNewGameWizard.Delete(cb.Message.Chat.ID)
	b.answerCallback(cb.ID, "")

	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgCreatingGame))
	edit.ReplyMarkup = &emptyKeyboard
	b.api.Send(edit) //nolint:errcheck

	adminGroupIDs := b.adminGroups(cb.From.ID)
	if len(adminGroupIDs) == 0 {
		b.sendText(cb.Message.Chat.ID, lz.T(i18n.MsgOnlyAdminCreateGames), nil)
		return
	}

	if len(adminGroupIDs) == 1 {
		b.createAndAnnounceGame(ctx, cb.Message.Chat.ID, cb.Message.MessageID, adminGroupIDs[0], gameDate, courts, wizard.venueID, lz)
		return
	}

	// Multiple groups with no pre-selection — ask which one.
	key := pendingGameKey{chatID: cb.Message.Chat.ID, messageID: cb.Message.MessageID}
	b.pendingGames.Store(key, &pendingGame{
		gameDate:    gameDate,
		courts:      courts,
		venueID:     wizard.venueID,
		replyChatID: cb.Message.Chat.ID,
		replyMsgID:  cb.Message.MessageID,
	})
	keyboard := b.buildGroupSelectionKeyboard(adminGroupIDs, key)
	selMsg := tgbotapi.NewMessage(cb.Message.Chat.ID, lz.T(i18n.MsgWhichGroup))
	selMsg.ReplyMarkup = keyboard
	if _, err := b.api.Send(selMsg); err != nil {
		slog.Error("handleNewGameTimeSlot: send group selection", "err", err)
		b.pendingGames.Delete(key)
	}
}

func (b *Bot) handleNewGameTimeCustom(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	raw, ok := b.pendingNewGameWizard.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wizard := raw.(*newGameWizard)

	// Keep wizard at wizardStepTime but clear the time slot keyboard → admin types time.
	b.answerCallback(cb.ID, "")

	localDate := wizard.gameDate.In(b.wizardLoc(wizard))
	prompt := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID,
		lz.Tf(i18n.MsgNewGameEnterTime, lz.FormatGameDate(localDate)))
	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	prompt.ReplyMarkup = &emptyKeyboard
	b.api.Send(prompt) //nolint:errcheck
}

// selectedCourtsString returns a comma-separated string of selected courts
// from the wizard's venueCourts list in their original order.
func selectedCourtsString(wizard *newGameWizard) string {
	var parts []string
	for _, c := range wizard.venueCourts {
		if wizard.selectedCourts[c] {
			parts = append(parts, c)
		}
	}
	return strings.Join(parts, ",")
}

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
	"github.com/vkhutorov/squash_bot/internal/models"
)

// gameDaysDisplayOrder is the order weekdays appear in the game-day picker keyboard.
var gameDaysDisplayOrder = []time.Weekday{
	time.Monday, time.Tuesday, time.Wednesday,
	time.Thursday, time.Friday, time.Saturday,
	time.Sunday,
}

// ── /venues command ───────────────────────────────────────────────────────────

func (b *Bot) handleCommandVenues(ctx context.Context, msg *tgbotapi.Message, lz *i18n.Localizer) {
	adminGroupIDs := b.adminGroups(msg.From.ID)
	if len(adminGroupIDs) == 0 {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgOnlyAdminCanUse))
		return
	}

	if len(adminGroupIDs) == 1 {
		b.sendVenueList(ctx, msg.Chat.ID, 0, adminGroupIDs[0], lz)
		return
	}

	// Multiple groups — let admin pick a group first (reuse the same group-picker pattern).
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
			tgbotapi.NewInlineKeyboardButtonData(title, fmt.Sprintf("venue_list:%d", gid)),
		))
	}
	out := tgbotapi.NewMessage(msg.Chat.ID, lz.T(i18n.MsgSelectGroupForVenues))
	out.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	if _, err := b.api.Send(out); err != nil {
		slog.Error("handleCommandVenues: send group picker", "err", err)
	}
}

// sendVenueList sends (or edits) the venue list for a group.
// If messageID != 0 the existing message is edited; otherwise a new message is sent.
func (b *Bot) sendVenueList(ctx context.Context, chatID int64, messageID int, groupID int64, lz *i18n.Localizer) {
	venues, err := b.client.GetVenuesByGroup(ctx, groupID)
	if err != nil {
		slog.Error("sendVenueList: get venues", "err", err, "group_id", groupID)
		if messageID != 0 {
			b.editText(chatID, messageID, lz.T(i18n.MsgSomethingWentWrong), nil)
		} else {
			b.sendText(chatID, lz.T(i18n.MsgSomethingWentWrong), nil)
		}
		return
	}

	// Fetch group title for the header.
	groupTitle := fmt.Sprintf("Group %d", groupID)
	if info, err := b.api.GetChat(tgbotapi.ChatInfoConfig{ChatConfig: tgbotapi.ChatConfig{ChatID: groupID}}); err == nil && info.Title != "" {
		groupTitle = info.Title
	}

	text := lz.Tf(i18n.MsgVenueList, escapeMarkdown(groupTitle))
	var rows [][]tgbotapi.InlineKeyboardButton

	for _, v := range venues {
		label := v.Name
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("venue_edit:%d", v.ID)),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnVenueDelete), fmt.Sprintf("venue_delete:%d:%d", v.ID, groupID)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnVenueAdd), fmt.Sprintf("venue_add:%d", groupID)),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)

	if len(venues) == 0 {
		text += lz.T(i18n.MsgVenueNoVenues)
	}

	if messageID != 0 {
		b.editText(chatID, messageID, text, &keyboard)
	} else {
		b.sendText(chatID, text, &keyboard)
	}
}

// ── Venue list callback ───────────────────────────────────────────────────────

func (b *Bot) handleVenueList(ctx context.Context, cb *tgbotapi.CallbackQuery, groupID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminCanUse))
		return
	}

	b.answerCallback(cb.ID, "")
	b.sendVenueList(ctx, cb.Message.Chat.ID, cb.Message.MessageID, groupID, lz)
}

// ── Add venue wizard ──────────────────────────────────────────────────────────

func (b *Bot) handleVenueAdd(ctx context.Context, cb *tgbotapi.CallbackQuery, groupID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminCanUse))
		return
	}

	b.pendingVenueWizard.Store(cb.Message.Chat.ID, &venueWizard{
		groupID: groupID,
		step:    venueStepName,
	})
	b.answerCallback(cb.ID, "")

	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgVenueAskName))
	edit.ReplyMarkup = &emptyKeyboard
	b.api.Send(edit) //nolint:errcheck
}

// processVenueWizard handles text input during venue creation.
func (b *Bot) processVenueWizard(ctx context.Context, msg *tgbotapi.Message, wiz *venueWizard) {
	lz := b.userLocalizer(msg.From.LanguageCode)
	text := strings.TrimSpace(msg.Text)

	switch wiz.step {
	case venueStepName:
		if text == "" {
			b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgInvalidFormat))
			return
		}
		wiz.name = text
		wiz.step = venueStepCourts
		b.pendingVenueWizard.Store(msg.Chat.ID, wiz)
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgVenueAskCourts))

	case venueStepCourts:
		courts := normalizeCourts(text)
		if courts == "" {
			b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgInvalidCourtsFormat))
			return
		}
		wiz.courts = courts
		wiz.step = venueStepTimeSlots
		b.pendingVenueWizard.Store(msg.Chat.ID, wiz)
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgVenueAskTimeSlots))

	case venueStepTimeSlots:
		timeSlots := ""
		if text != lz.T(i18n.MsgVenueSkipAddress) && text != "-" {
			timeSlots = normalizeTimeSlots(text)
			if !validateTimeSlots(timeSlots) {
				b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgVenueInvalidTimeSlots))
				return // keep wizard state so the user can retry
			}
		}
		wiz.timeSlots = timeSlots
		if timeSlots != "" {
			// Have time slots — ask for preferred time.
			wiz.step = venueStepPreferredTime
			b.pendingVenueWizard.Store(msg.Chat.ID, wiz)
			keyboard := renderPreferredTimeKeyboard(splitCSV(timeSlots), lz)
			b.sendText(msg.Chat.ID, lz.T(i18n.MsgVenueAskPreferredTime), &keyboard)
		} else {
			wiz.step = venueStepAddress
			b.pendingVenueWizard.Store(msg.Chat.ID, wiz)
			b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgVenueAskAddress))
		}

	case venueStepAddress:
		if text != lz.T(i18n.MsgVenueSkipAddress) && text != "-" {
			wiz.address = text
		}
		wiz.step = venueStepGameDays
		b.pendingVenueWizard.Store(msg.Chat.ID, wiz)
		// Send game-days toggle keyboard.
		keyboard := renderGameDaysKeyboard(wiz.gameDays, lz)
		b.sendText(msg.Chat.ID, lz.T(i18n.MsgVenueAskGameDays), &keyboard)

	case venueStepGracePeriod:
		gracePeriod := 0
		if text != "-" && text != "" {
			n, err := strconv.Atoi(text)
			if err != nil || n <= 0 {
				b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgInvalidFormat))
				return
			}
			gracePeriod = n
		}
		wiz.gracePeriod = gracePeriod
		wiz.step = venueStepAutoBookingCourts
		b.pendingVenueWizard.Store(msg.Chat.ID, wiz)
		b.reply(msg.Chat.ID, msg.MessageID, lz.Tf(i18n.MsgVenueAskAutoBookingCourts, wiz.courts))

	case venueStepAutoBookingCourts:
		autoBookingCourts := ""
		if text != "-" && text != "" {
			normalized := normalizeCourts(text)
			courtSet := makeCourtSet(wiz.courts)
			for _, part := range splitCSV(normalized) {
				if !courtSet[part] {
					b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgVenueInvalidAutoBookingCourts))
					return
				}
			}
			autoBookingCourts = normalized
		}
		wiz.autoBookingCourts = autoBookingCourts
		b.pendingVenueWizard.Delete(msg.Chat.ID)

		gameDaysStr := gameDaysToString(wiz.gameDays)
		venue, err := b.client.CreateVenue(ctx, wiz.groupID, wiz.name, wiz.courts, wiz.timeSlots, wiz.address, wiz.gracePeriod, gameDaysStr, 0, wiz.preferredGameTime, wiz.autoBookingCourts)
		if err != nil {
			slog.Error("processVenueWizard: create venue", "err", err)
			b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgSomethingWentWrong))
			return
		}

		slog.Info("Venue created", "venue_id", venue.ID, "name", venue.Name, "group_id", wiz.groupID)
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgVenueCreated))
		b.sendVenueList(ctx, msg.Chat.ID, 0, wiz.groupID, lz)
	}
}

// ── Edit venue ────────────────────────────────────────────────────────────────

func (b *Bot) handleVenueEditMenu(ctx context.Context, cb *tgbotapi.CallbackQuery, venueID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	venue, err := b.client.GetVenueByID(ctx, venueID)
	if err != nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgVenueNotFound))
		return
	}

	isAdmin, err := b.isAdminInGroup(cb.From.ID, venue.GroupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminCanUse))
		return
	}

	b.answerCallback(cb.ID, "")
	b.renderVenueEditMenu(cb.Message.Chat.ID, cb.Message.MessageID, venue, lz)
}

func (b *Bot) renderVenueEditMenu(chatID int64, messageID int, venue *models.Venue, lz *i18n.Localizer) {
	timeSlots := venue.TimeSlots
	if timeSlots == "" {
		timeSlots = "—"
	}
	address := venue.Address
	if address == "" {
		address = "—"
	}
	preferredTime := venue.PreferredGameTime
	if preferredTime == "" {
		preferredTime = "—"
	}
	autoBookingCourts := venue.AutoBookingCourts
	if autoBookingCourts == "" {
		autoBookingCourts = "—"
	}
	text := lz.Tf(i18n.MsgVenueEditMenu,
		escapeMarkdown(venue.Name),
		escapeMarkdown(venue.Courts),
		escapeMarkdown(timeSlots),
		escapeMarkdown(address),
	) + "\n" + lz.Tf(i18n.MsgVenuePreferredTimeLine, escapeMarkdown(preferredTime)) +
		"\n" + lz.Tf(i18n.MsgVenueAutoBookingCourtsLine, escapeMarkdown(autoBookingCourts))

	var rows [][]tgbotapi.InlineKeyboardButton
	rows = append(rows,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnVenueEditName), fmt.Sprintf("venue_edit_name:%d:%d", venue.ID, venue.GroupID)),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnVenueEditCourts), fmt.Sprintf("venue_edit_courts:%d:%d", venue.ID, venue.GroupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnVenueEditTimeSlots), fmt.Sprintf("venue_edit_slots:%d:%d", venue.ID, venue.GroupID)),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnVenueEditAddress), fmt.Sprintf("venue_edit_addr:%d:%d", venue.ID, venue.GroupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnVenueEditGameDays), fmt.Sprintf("venue_edit_gamedays:%d:%d", venue.ID, venue.GroupID)),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnVenueEditGracePeriod), fmt.Sprintf("venue_edit_graceperiod:%d:%d", venue.ID, venue.GroupID)),
		),
	)
	// Only show "Preferred Time" button when time slots are configured.
	if venue.TimeSlots != "" {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnVenueEditPreferredTime), fmt.Sprintf("venue_edit_preferred_time:%d:%d", venue.ID, venue.GroupID)),
		))
	}
	// Only show "Auto-booking Courts" button when courts are configured.
	if venue.Courts != "" {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnVenueEditAutoBookingCourts), fmt.Sprintf("venue_edit_auto_booking_courts:%d:%d", venue.ID, venue.GroupID)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnBack), fmt.Sprintf("venue_list:%d", venue.GroupID)),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editText(chatID, messageID, text, &keyboard)
}

func (b *Bot) handleVenueStartEdit(ctx context.Context, cb *tgbotapi.CallbackQuery, venueID, groupID int64, field venueEditField) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminCanUse))
		return
	}

	// Verify that the venue actually belongs to groupID — guards against forged callback data
	// where an admin could inject a venueID from another group.
	venue, err := b.client.GetVenueByID(ctx, venueID)
	if err != nil || venue.GroupID != groupID {
		b.answerCallback(cb.ID, lz.T(i18n.MsgVenueNotFound))
		return
	}

	b.answerCallback(cb.ID, "")

	// Game days editing uses a toggle keyboard (no free text state needed).
	if field == venueEditFieldGameDays {
		selectedDays := gameDaysFromString(venue.GameDays)
		b.pendingVenueGameDaysEdit.Store(cb.Message.Chat.ID, &venueGameDaysEditState{
			venueID:      venueID,
			groupID:      groupID,
			selectedDays: selectedDays,
			msgID:        cb.Message.MessageID,
		})
		keyboard := renderGameDaysKeyboard(selectedDays, lz)
		b.editText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgVenueAskGameDays), &keyboard)
		return
	}

	// Preferred time editing uses an inline keyboard of time slots.
	if field == venueEditFieldPreferredTime {
		slots := splitCSV(venue.TimeSlots)
		if len(slots) == 0 {
			b.answerCallback(cb.ID, lz.T(i18n.MsgVenueNoPreferredTime))
			return
		}
		b.pendingVenuePreferredTimeEdit.Store(cb.Message.Chat.ID, &venuePreferredTimeEditState{
			venueID:   venueID,
			groupID:   groupID,
			timeSlots: slots,
		})
		keyboard := renderPreferredTimeEditKeyboard(venueID, slots, lz)
		b.editText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgVenueAskPreferredTime), &keyboard)
		return
	}

	b.pendingVenueEdit.Store(cb.Message.Chat.ID, &venueEditState{
		venueID: venueID,
		groupID: groupID,
		field:   field,
	})

	var prompt string
	switch field {
	case venueEditFieldName:
		prompt = lz.T(i18n.MsgVenueAskName)
	case venueEditFieldCourts:
		prompt = lz.T(i18n.MsgVenueAskCourts)
	case venueEditFieldTimeSlots:
		prompt = lz.T(i18n.MsgVenueAskTimeSlots)
	case venueEditFieldAddress:
		prompt = lz.T(i18n.MsgVenueAskAddress)
	case venueEditFieldGracePeriod:
		prompt = lz.T(i18n.MsgVenueAskGracePeriod)
	case venueEditFieldAutoBookingCourts:
		prompt = lz.Tf(i18n.MsgVenueAskAutoBookingCourts, venue.Courts)
	}
	b.sendText(cb.Message.Chat.ID, prompt, nil)
}

// processVenueEdit handles text input during a single-field venue edit.
func (b *Bot) processVenueEdit(ctx context.Context, msg *tgbotapi.Message, state *venueEditState) {
	lz := b.userLocalizer(msg.From.LanguageCode)
	text := strings.TrimSpace(msg.Text)

	// Fetch current venue to apply partial update.
	venue, err := b.client.GetVenueByID(ctx, state.venueID)
	if err != nil {
		slog.Error("processVenueEdit: get venue", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgVenueNotFound))
		b.pendingVenueEdit.Delete(msg.Chat.ID)
		return
	}

	switch state.field {
	case venueEditFieldName:
		if text == "" {
			b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgInvalidFormat))
			return
		}
		venue.Name = text
	case venueEditFieldCourts:
		courts := normalizeCourts(text)
		if courts == "" {
			b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgInvalidCourtsFormat))
			return
		}
		venue.Courts = courts
	case venueEditFieldTimeSlots:
		if text == "-" || text == lz.T(i18n.MsgVenueSkipAddress) {
			venue.TimeSlots = ""
		} else {
			slots := normalizeTimeSlots(text)
			if !validateTimeSlots(slots) {
				b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgVenueInvalidTimeSlots))
				return
			}
			venue.TimeSlots = slots
		}
	case venueEditFieldAddress:
		if text == "-" || text == lz.T(i18n.MsgVenueSkipAddress) {
			venue.Address = ""
		} else {
			venue.Address = text
		}
	case venueEditFieldGracePeriod:
		if text == "-" || text == "" {
			venue.GracePeriodHours = 24
		} else {
			n, err := strconv.Atoi(text)
			if err != nil || n <= 0 {
				b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgInvalidFormat))
				return
			}
			venue.GracePeriodHours = n
		}
	case venueEditFieldAutoBookingCourts:
		if text == "-" || text == "" {
			venue.AutoBookingCourts = ""
		} else {
			normalized := normalizeCourts(text)
			courtSet := makeCourtSet(venue.Courts)
			for _, part := range splitCSV(normalized) {
				if !courtSet[part] {
					b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgVenueInvalidAutoBookingCourts))
					return
				}
			}
			venue.AutoBookingCourts = normalized
		}
	}

	b.pendingVenueEdit.Delete(msg.Chat.ID)

	// When time slots change, revalidate preferred game time; clear if no longer in the new list.
	if state.field == venueEditFieldTimeSlots && venue.PreferredGameTime != "" {
		found := false
		for _, s := range splitCSV(venue.TimeSlots) {
			if s == venue.PreferredGameTime {
				found = true
				break
			}
		}
		if !found {
			venue.PreferredGameTime = ""
		}
	}

	// When courts change, drop any auto-booking courts no longer present in the new list.
	if state.field == venueEditFieldCourts && venue.AutoBookingCourts != "" {
		newCourtSet := makeCourtSet(venue.Courts)
		var valid []string
		for _, c := range splitCSV(venue.AutoBookingCourts) {
			if newCourtSet[c] {
				valid = append(valid, c)
			}
		}
		venue.AutoBookingCourts = strings.Join(valid, ",")
	}

	updated, err := b.client.UpdateVenue(ctx, venue.ID, venue.GroupID, venue.Name, venue.Courts, venue.TimeSlots, venue.Address, venue.GracePeriodHours, venue.GameDays, venue.BookingOpensDays, venue.PreferredGameTime, venue.AutoBookingCourts)
	if err != nil {
		slog.Error("processVenueEdit: update venue", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	slog.Info("Venue updated", "venue_id", updated.ID, "field", state.field)
	b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgVenueUpdated))
	b.sendVenueList(ctx, msg.Chat.ID, 0, updated.GroupID, lz)
}

// ── Delete venue ──────────────────────────────────────────────────────────────

func (b *Bot) handleVenueDeleteConfirm(ctx context.Context, cb *tgbotapi.CallbackQuery, venueID, groupID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	venue, err := b.client.GetVenueByID(ctx, venueID)
	if err != nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgVenueNotFound))
		return
	}

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminCanUse))
		return
	}

	// Verify the venue belongs to groupID — guards against forged callback data
	// that could reveal another group's venue name in the confirmation prompt.
	if venue.GroupID != groupID {
		b.answerCallback(cb.ID, lz.T(i18n.MsgVenueNotFound))
		return
	}

	b.answerCallback(cb.ID, "")

	text := lz.Tf(i18n.MsgVenueConfirmDelete, escapeMarkdown(venue.Name))
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnVenueConfirmDelete), fmt.Sprintf("venue_delete_ok:%d:%d", venueID, groupID)),
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnBack), fmt.Sprintf("venue_list:%d", groupID)),
		),
	)
	b.editText(cb.Message.Chat.ID, cb.Message.MessageID, text, &keyboard)
}

func (b *Bot) handleVenueDelete(ctx context.Context, cb *tgbotapi.CallbackQuery, venueID, groupID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminCanUse))
		return
	}

	if err := b.client.DeleteVenue(ctx, venueID, groupID); err != nil {
		slog.Error("handleVenueDelete: delete", "err", err, "venue_id", venueID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	slog.Info("Venue deleted", "venue_id", venueID, "group_id", groupID, "by_user", cb.From.ID)
	b.answerCallback(cb.ID, lz.T(i18n.MsgVenueDeleted))
	b.sendVenueList(ctx, cb.Message.Chat.ID, cb.Message.MessageID, groupID, lz)
}

// ── Game days toggle callbacks ────────────────────────────────────────────────

// handleVenueDayToggle handles venue_day_toggle:<day> callbacks.
// Works for both the venue wizard (venueStepGameDays) and the edit game-days flow.
func (b *Bot) handleVenueDayToggle(ctx context.Context, cb *tgbotapi.CallbackQuery, rawDay string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	day, err := strconv.Atoi(rawDay)
	if err != nil || day < 0 || day > 6 {
		b.answerCallback(cb.ID, "")
		return
	}
	b.answerCallback(cb.ID, "")

	// Check wizard first.
	if raw, ok := b.pendingVenueWizard.Load(cb.Message.Chat.ID); ok {
		wiz := raw.(*venueWizard)
		if wiz.step == venueStepGameDays {
			wiz.gameDays = toggleDay(wiz.gameDays, day)
			b.pendingVenueWizard.Store(cb.Message.Chat.ID, wiz)
			keyboard := renderGameDaysKeyboard(wiz.gameDays, lz)
			b.editText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgVenueAskGameDays), &keyboard)
			return
		}
	}

	// Check edit state.
	if raw, ok := b.pendingVenueGameDaysEdit.Load(cb.Message.Chat.ID); ok {
		state := raw.(*venueGameDaysEditState)
		state.selectedDays = toggleDay(state.selectedDays, day)
		b.pendingVenueGameDaysEdit.Store(cb.Message.Chat.ID, state)
		keyboard := renderGameDaysKeyboard(state.selectedDays, lz)
		b.editText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgVenueAskGameDays), &keyboard)
	}
}

// handleVenueDayConfirm handles venue_day_confirm:_ callbacks.
func (b *Bot) handleVenueDayConfirm(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	lz := b.userLocalizer(cb.From.LanguageCode)
	b.answerCallback(cb.ID, "")

	// Check wizard first.
	if raw, ok := b.pendingVenueWizard.Load(cb.Message.Chat.ID); ok {
		wiz := raw.(*venueWizard)
		if wiz.step == venueStepGameDays {
			wiz.step = venueStepGracePeriod
			b.pendingVenueWizard.Store(cb.Message.Chat.ID, wiz)
			emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
			edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgVenueAskGracePeriod))
			edit.ReplyMarkup = &emptyKeyboard
			b.api.Send(edit) //nolint:errcheck
			return
		}
	}

	// Check edit state.
	if raw, ok := b.pendingVenueGameDaysEdit.Load(cb.Message.Chat.ID); ok {
		state := raw.(*venueGameDaysEditState)
		b.pendingVenueGameDaysEdit.Delete(cb.Message.Chat.ID)

		venue, err := b.client.GetVenueByID(ctx, state.venueID)
		if err != nil {
			slog.Error("handleVenueDayConfirm: get venue", "err", err)
			b.sendText(cb.Message.Chat.ID, lz.T(i18n.MsgSomethingWentWrong), nil)
			return
		}

		venue.GameDays = gameDaysToString(state.selectedDays)
		updated, err := b.client.UpdateVenue(ctx, venue.ID, venue.GroupID, venue.Name, venue.Courts, venue.TimeSlots, venue.Address, venue.GracePeriodHours, venue.GameDays, venue.BookingOpensDays, venue.PreferredGameTime, venue.AutoBookingCourts)
		if err != nil {
			slog.Error("handleVenueDayConfirm: update venue", "err", err)
			b.sendText(cb.Message.Chat.ID, lz.T(i18n.MsgSomethingWentWrong), nil)
			return
		}

		slog.Info("Venue game days updated", "venue_id", updated.ID, "game_days", updated.GameDays)
		b.sendText(cb.Message.Chat.ID, lz.T(i18n.MsgVenueUpdated), nil)
		b.sendVenueList(ctx, cb.Message.Chat.ID, 0, state.groupID, lz)
	}
}

// ── Preferred game time callbacks ─────────────────────────────────────────────

// renderPreferredTimeKeyboard builds an inline keyboard for selecting a preferred game time.
// Each slot gets its own button row, plus a "No preference" button at the bottom.
func renderPreferredTimeKeyboard(slots []string, lz *i18n.Localizer) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, slot := range slots {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(slot, fmt.Sprintf("venue_wiz_ptime:%s", slot)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnVenueClearPreferredTime), "venue_wiz_ptime:_skip"),
	))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// renderPreferredTimeEditKeyboard builds an inline keyboard for editing an existing venue's preferred time.
func renderPreferredTimeEditKeyboard(venueID int64, slots []string, lz *i18n.Localizer) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, slot := range slots {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(slot, fmt.Sprintf("venue_ptime_set:%d:%s", venueID, slot)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnVenueClearPreferredTime), fmt.Sprintf("venue_ptime_set:%d:_clear", venueID)),
	))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// handleVenueWizPreferredTimePick handles venue_wiz_ptime:<slot> callbacks during venue creation wizard.
func (b *Bot) handleVenueWizPreferredTimePick(ctx context.Context, cb *tgbotapi.CallbackQuery, slot string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	raw, ok := b.pendingVenueWizard.Load(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	wiz := raw.(*venueWizard)
	if wiz.step != venueStepPreferredTime {
		b.answerCallback(cb.ID, "")
		return
	}

	if slot != "_skip" {
		wiz.preferredGameTime = slot
	}
	wiz.step = venueStepAddress
	b.pendingVenueWizard.Store(cb.Message.Chat.ID, wiz)
	b.answerCallback(cb.ID, "")

	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, lz.T(i18n.MsgVenueAskAddress))
	edit.ReplyMarkup = &emptyKeyboard
	b.api.Send(edit) //nolint:errcheck
}

// handleVenuePtimeSet handles venue_ptime_set:<venueID>:<slot> callbacks for existing venue editing.
func (b *Bot) handleVenuePtimeSet(ctx context.Context, cb *tgbotapi.CallbackQuery, venueID int64, slot string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	raw, ok := b.pendingVenuePreferredTimeEdit.LoadAndDelete(cb.Message.Chat.ID)
	if !ok {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}
	state := raw.(*venuePreferredTimeEditState)

	if state.venueID != venueID {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSessionExpired))
		return
	}

	venue, err := b.client.GetVenueByID(ctx, state.venueID)
	if err != nil {
		slog.Error("handleVenuePtimeSet: get venue", "err", err)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	preferredTime := ""
	if slot != "_clear" {
		preferredTime = slot
	}
	venue.PreferredGameTime = preferredTime

	updated, err := b.client.UpdateVenue(ctx, venue.ID, venue.GroupID, venue.Name, venue.Courts, venue.TimeSlots, venue.Address, venue.GracePeriodHours, venue.GameDays, venue.BookingOpensDays, venue.PreferredGameTime, venue.AutoBookingCourts)
	if err != nil {
		slog.Error("handleVenuePtimeSet: update venue", "err", err)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	slog.Info("Venue preferred time updated", "venue_id", updated.ID, "preferred_game_time", updated.PreferredGameTime)
	b.answerCallback(cb.ID, lz.T(i18n.MsgVenueUpdated))
	b.renderVenueEditMenu(cb.Message.Chat.ID, cb.Message.MessageID, updated, lz)
}

// renderGameDaysKeyboard builds a toggle keyboard for weekday selection.
// selectedDays is a slice of time.Weekday int values (0=Sun..6=Sat).
func renderGameDaysKeyboard(selectedDays []int, lz *i18n.Localizer) tgbotapi.InlineKeyboardMarkup {
	selected := make(map[int]bool)
	for _, d := range selectedDays {
		selected[d] = true
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	var row []tgbotapi.InlineKeyboardButton
	for i, wd := range gameDaysDisplayOrder {
		label := lz.ShortWeekday(wd)
		if selected[int(wd)] {
			label = "✓ " + label
		}
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("venue_day_toggle:%d", int(wd))))
		if len(row) == 3 || i == len(gameDaysDisplayOrder)-1 {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(row...))
			row = nil
		}
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.MsgVenueConfirmDays), "venue_day_confirm:_"),
	))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// toggleDay toggles a day int in/out of a slice (returns new slice).
func toggleDay(days []int, day int) []int {
	for i, d := range days {
		if d == day {
			return append(days[:i], days[i+1:]...)
		}
	}
	return append(days, day)
}

// gameDaysToString converts a slice of weekday ints to a comma-separated string.
func gameDaysToString(days []int) string {
	if len(days) == 0 {
		return ""
	}
	parts := make([]string, len(days))
	for i, d := range days {
		parts[i] = strconv.Itoa(d)
	}
	return strings.Join(parts, ",")
}

// gameDaysFromString parses a comma-separated weekday int string back to a slice.
func gameDaysFromString(s string) []int {
	if s == "" {
		return nil
	}
	var days []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if d, err := strconv.Atoi(p); err == nil && d >= 0 && d <= 6 {
			days = append(days, d)
		}
	}
	return days
}

// ── New game wizard: venue, courts, timeslot callbacks ────────────────────────

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

	d := wizard.gameDate.In(b.loc)
	gameDate := time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), 0, 0, b.loc)
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

	localDate := wizard.gameDate.In(b.loc)
	prompt := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID,
		lz.Tf(i18n.MsgNewGameEnterTime, lz.FormatGameDate(localDate)))
	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	prompt.ReplyMarkup = &emptyKeyboard
	b.api.Send(prompt) //nolint:errcheck
}

// ── Helpers ───────────────────────────────────────────────────────────────────

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

// makeCourtSet builds a lookup set from a comma-separated courts string.
func makeCourtSet(courts string) map[string]bool {
	set := make(map[string]bool)
	for _, c := range splitCSV(courts) {
		set[c] = true
	}
	return set
}

// splitCSV splits a comma-separated string into a trimmed slice, omitting empty parts.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var parts []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// normalizeTimeSlots cleans up a user-entered time slots string (same delimiters as courts).
func normalizeTimeSlots(s string) string {
	return normalizeCourts(s) // same delimiter normalization logic works
}

// validateTimeSlots returns true if every comma-separated part of s parses as a valid HH:MM time.
// An empty string is considered valid (no slots).
func validateTimeSlots(s string) bool {
	if s == "" {
		return true
	}
	for _, slot := range strings.Split(s, ",") {
		if _, err := time.Parse("15:04", strings.TrimSpace(slot)); err != nil {
			return false
		}
	}
	return true
}

// parseInt64 is a small helper to parse int64 from string.
func parseInt64(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// editText edits an existing message's text and optional keyboard.
func (b *Bot) editText(chatID int64, messageID int, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = keyboard
	b.api.Send(edit) //nolint:errcheck
}

// sendText sends a new message with optional keyboard.
func (b *Bot) sendText(chatID int64, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if keyboard != nil {
		msg.ReplyMarkup = *keyboard
	}
	b.api.Send(msg) //nolint:errcheck
}

package telegram

import (
	"context"
	"fmt"
	"log/slog"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/i18n"
)

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
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnSetTimezone), fmt.Sprintf("set_tz_pick:%d", groupID)),
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

// handleSetTzPick shows the timezone selection keyboard for a specific group.
func (b *Bot) handleSetTzPick(ctx context.Context, cb *tgbotapi.CallbackQuery, groupID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminSetLanguage))
		return
	}

	b.answerCallback(cb.ID, "")
	b.renderTimezoneKeyboard(cb.Message.Chat.ID, cb.Message.MessageID, groupID, lz)
}

// handleSetTz applies the chosen timezone to the group.
func (b *Bot) handleSetTz(ctx context.Context, cb *tgbotapi.CallbackQuery, tz string, groupID int64) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminSetLanguage))
		return
	}

	if err := b.client.SetGroupTimezone(ctx, groupID, tz); err != nil {
		slog.Error("handleSetTz: set timezone", "err", err, "group_id", groupID, "tz", tz)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	slog.Info("Group timezone updated", "group_id", groupID, "tz", tz, "by_user", cb.From.ID)
	b.answerCallback(cb.ID, lz.T(i18n.MsgTimezoneSet))

	// Remove the keyboard from the timezone-selection message.
	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	edit := tgbotapi.NewEditMessageReplyMarkup(cb.Message.Chat.ID, cb.Message.MessageID, emptyKeyboard)
	b.api.Send(edit) //nolint:errcheck
}

// renderTimezoneKeyboard edits a message with a curated timezone selection keyboard.
func (b *Bot) renderTimezoneKeyboard(chatID int64, messageID int, groupID int64, lz *i18n.Localizer) {
	// Curated list of common IANA timezones, displayed 2 per row.
	tzPairs := [][2]string{
		{"UTC", "UTC"},
		{"Europe/London", "London"},
		{"Europe/Berlin", "Berlin"},
		{"Europe/Paris", "Paris"},
		{"Europe/Moscow", "Moscow"},
		{"America/New_York", "New York"},
		{"America/Chicago", "Chicago"},
		{"America/Denver", "Denver"},
		{"America/Los_Angeles", "Los Angeles"},
		{"America/Sao_Paulo", "São Paulo"},
		{"Asia/Dubai", "Dubai"},
		{"Asia/Kolkata", "Kolkata"},
		{"Asia/Bangkok", "Bangkok"},
		{"Asia/Singapore", "Singapore"},
		{"Asia/Tokyo", "Tokyo"},
		{"Asia/Seoul", "Seoul"},
		{"Australia/Sydney", "Sydney"},
		{"Pacific/Auckland", "Auckland"},
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := 0; i < len(tzPairs); i += 2 {
		row := tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tzPairs[i][1], fmt.Sprintf("set_tz:%d:%s", groupID, tzPairs[i][0])),
		)
		if i+1 < len(tzPairs) {
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(tzPairs[i+1][1], fmt.Sprintf("set_tz:%d:%s", groupID, tzPairs[i+1][0])))
		}
		rows = append(rows, row)
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	edit := tgbotapi.NewEditMessageText(chatID, messageID, lz.T(i18n.MsgSelectTimezone))
	edit.ReplyMarkup = &keyboard
	b.api.Send(edit) //nolint:errcheck
}

// handleTrigger calls the management service to run a scheduled event on demand.
// Only users listed in serviceAdminIDs are allowed.
func (b *Bot) handleTrigger(ctx context.Context, cb *tgbotapi.CallbackQuery, event string) {
	lz := b.userLocalizer(cb.From.LanguageCode)

	if !b.serviceAdminIDs[cb.From.ID] {
		b.answerCallback(cb.ID, lz.T(i18n.MsgNotAuthorized))
		return
	}

	if !isValidTriggerEvent(event) {
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

// isValidTriggerEvent reports whether event is a recognised scheduler event name
// that may be triggered manually via the /trigger command.
func isValidTriggerEvent(event string) bool {
	switch event {
	case "cancellation_reminder", "day_after_cleanup", "booking_reminder", "auto_booking":
		return true
	default:
		return false
	}
}

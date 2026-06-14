package telegram

import (
	"context"
	"fmt"
	"log/slog"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/i18n"
)

// ── Per-user language settings ────────────────────────────────────────────────

// handleToggleResultsOptOut flips the results/leaderboard opt-out for the user.
func (b *Bot) handleToggleResultsOptOut(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	lz := b.userLocalizer(ctx, cb.From)

	current, err := b.client.GetUserResultsOptOut(ctx, cb.From.ID)
	if err != nil {
		slog.Error("handleToggleResultsOptOut: get opt-out", "err", err, "user_id", cb.From.ID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	newOptOut := !current
	if err := b.client.SetUserResultsOptOut(ctx, cb.From.ID, newOptOut); err != nil {
		slog.Error("handleToggleResultsOptOut: set opt-out", "err", err, "user_id", cb.From.ID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	if newOptOut {
		b.answerCallback(cb.ID, lz.T(i18n.MsgResultsOptOutEnabled))
	} else {
		b.answerCallback(cb.ID, lz.T(i18n.MsgResultsOptOutDisabled))
	}

	// Build the keyboard directly from the known new state to avoid a round-trip.
	optOutBtnKey := i18n.BtnSettingsResultsOptOutOn
	if newOptOut {
		optOutBtnKey = i18n.BtnSettingsResultsOptOutOff
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnSettingsLanguage), "settings_lang:_"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(optOutBtnKey), "settings_results_optout:_"),
		),
	)
	edit := tgbotapi.NewEditMessageReplyMarkup(cb.Message.Chat.ID, cb.Message.MessageID, keyboard)
	b.api.Send(edit) //nolint:errcheck
}

// handleSettingsLang shows the DM language picker for the user.
func (b *Bot) handleSettingsLang(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	lz := b.userLocalizer(ctx, cb.From)
	b.answerCallback(cb.ID, "")
	b.renderUserLanguageKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, cb.From, lz)
}

// handleSetUserLang applies the chosen language as the user's DM preference.
func (b *Bot) handleSetUserLang(ctx context.Context, cb *tgbotapi.CallbackQuery, lang string) {
	lz := b.userLocalizer(ctx, cb.From)
	switch lang {
	case "en", "de", "ru":
		// valid
	default:
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	if err := b.client.SetUserDMLanguage(ctx, cb.From.ID, lang); err != nil {
		slog.Error("handleSetUserLang: set language", "err", err, "user_id", cb.From.ID, "lang", lang)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	// Update cache immediately so subsequent messages reflect the new language.
	b.userLangCache.Store(cb.From.ID, userLangPref{lang: i18n.Lang(lang), hasOverride: true})

	newLz := i18n.New(i18n.Lang(lang))
	b.answerCallback(cb.ID, newLz.T(i18n.MsgDMLanguageSet))
	b.renderUserLanguageKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, cb.From, newLz)
}

// renderUserLanguageKeyboard edits (or sends) the DM language picker with the current language marked ✓.
func (b *Bot) renderUserLanguageKeyboard(ctx context.Context, chatID int64, messageID int, u *tgbotapi.User, lz *i18n.Localizer) {
	current := b.resolveUserLang(ctx, u)

	markCurrent := func(langCode i18n.Lang, label string) string {
		if current == langCode {
			return label + " ✓"
		}
		return label
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(markCurrent(i18n.En, lz.T(i18n.BtnLangEn)), "set_user_lang:en"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(markCurrent(i18n.De, lz.T(i18n.BtnLangDe)), "set_user_lang:de"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(markCurrent(i18n.Ru, lz.T(i18n.BtnLangRu)), "set_user_lang:ru"),
		),
	)

	if messageID != 0 {
		edit := tgbotapi.NewEditMessageText(chatID, messageID, lz.T(i18n.MsgSelectDMLanguage))
		edit.ReplyMarkup = &keyboard
		b.api.Send(edit) //nolint:errcheck
	} else {
		msg := tgbotapi.NewMessage(chatID, lz.T(i18n.MsgSelectDMLanguage))
		msg.ReplyMarkup = keyboard
		b.api.Send(msg) //nolint:errcheck
	}
}

// handleSetLangGroup shows the language selection keyboard for a specific group.
func (b *Bot) handleSetLangGroup(ctx context.Context, cb *tgbotapi.CallbackQuery, groupID int64) {
	lz := b.userLocalizer(ctx, cb.From)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminSetLanguage))
		return
	}

	b.answerCallback(cb.ID, "")
	b.renderLanguageKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, groupID, lz)
}

// handleSetLang applies the chosen language to the group.
func (b *Bot) handleSetLang(ctx context.Context, cb *tgbotapi.CallbackQuery, lang string, groupID int64) {
	lz := b.userLocalizer(ctx, cb.From)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminSetLanguage))
		return
	}

	if err := b.client.SetGroupLanguage(ctx, groupID, lang, cb.From.ID, actorDisplayFrom(cb.From)); err != nil {
		slog.Error("handleSetLang: set language", "err", err, "group_id", groupID, "lang", lang)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	slog.Info("Group language updated", "group_id", groupID, "lang", lang, "by_user", cb.From.ID)
	b.answerCallback(cb.ID, lz.T(i18n.MsgLanguageSet))
	b.renderGroupConfigKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, groupID, lz)
}

// renderLanguageKeyboard edits (or sends) a message with language selection buttons for groupID.
func (b *Bot) renderLanguageKeyboard(ctx context.Context, chatID int64, messageID int, groupID int64, lz *i18n.Localizer) {
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
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnBack), fmt.Sprintf("group_cfg:%d", groupID)),
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

// renderGroupConfigKeyboard edits (or sends) the 3-button group config menu.
func (b *Bot) renderGroupConfigKeyboard(ctx context.Context, chatID int64, messageID int, groupID int64, lz *i18n.Localizer) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnGroupLanguage), fmt.Sprintf("set_lang_group:%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnGroupTimezone), fmt.Sprintf("set_tz_pick:%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnGroupChangelog), fmt.Sprintf("changelog_cfg:%d", groupID)),
		),
	)

	if messageID != 0 {
		edit := tgbotapi.NewEditMessageText(chatID, messageID, lz.T(i18n.MsgGroupConfigTitle))
		edit.ReplyMarkup = &keyboard
		b.api.Send(edit) //nolint:errcheck
	} else {
		msg := tgbotapi.NewMessage(chatID, lz.T(i18n.MsgGroupConfigTitle))
		msg.ReplyMarkup = keyboard
		b.api.Send(msg) //nolint:errcheck
	}
}

// handleGroupConfig shows the group config menu for the given group.
func (b *Bot) handleGroupConfig(ctx context.Context, cb *tgbotapi.CallbackQuery, groupID int64) {
	lz := b.userLocalizer(ctx, cb.From)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminSetLanguage))
		return
	}

	b.answerCallback(cb.ID, "")
	b.renderGroupConfigKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, groupID, lz)
}

// handleToggleChangelog toggles the changelog_enabled setting for the group.
func (b *Bot) handleToggleChangelog(ctx context.Context, cb *tgbotapi.CallbackQuery, groupID int64) {
	lz := b.userLocalizer(ctx, cb.From)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminSetLanguage))
		return
	}

	group, err := b.client.GetGroupByID(ctx, groupID)
	if err != nil {
		slog.Error("handleToggleChangelog: get group", "err", err, "group_id", groupID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}
	if group == nil {
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	newEnabled := !group.ChangelogEnabled
	if err := b.client.SetGroupChangelog(ctx, groupID, newEnabled, cb.From.ID, actorDisplayFrom(cb.From)); err != nil {
		slog.Error("handleToggleChangelog: set changelog", "err", err, "group_id", groupID)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	slog.Info("Group changelog toggle", "group_id", groupID, "enabled", newEnabled, "by_user", cb.From.ID)
	if newEnabled {
		b.answerCallback(cb.ID, lz.T(i18n.MsgChangelogEnabled))
	} else {
		b.answerCallback(cb.ID, lz.T(i18n.MsgChangelogDisabled))
	}
	b.renderChangelogKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, groupID, lz)
}

// handleSetTzPick shows the timezone selection keyboard for a specific group.
func (b *Bot) handleSetTzPick(ctx context.Context, cb *tgbotapi.CallbackQuery, groupID int64) {
	lz := b.userLocalizer(ctx, cb.From)

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
	lz := b.userLocalizer(ctx, cb.From)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminSetLanguage))
		return
	}

	if err := b.client.SetGroupTimezone(ctx, groupID, tz, cb.From.ID, actorDisplayFrom(cb.From)); err != nil {
		slog.Error("handleSetTz: set timezone", "err", err, "group_id", groupID, "tz", tz)
		b.answerCallback(cb.ID, lz.T(i18n.MsgSomethingWentWrong))
		return
	}

	slog.Info("Group timezone updated", "group_id", groupID, "tz", tz, "by_user", cb.From.ID)
	b.answerCallback(cb.ID, lz.T(i18n.MsgTimezoneSet))
	b.renderGroupConfigKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, groupID, lz)
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
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnBack), fmt.Sprintf("group_cfg:%d", groupID)),
	))
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	edit := tgbotapi.NewEditMessageText(chatID, messageID, lz.T(i18n.MsgSelectTimezone))
	edit.ReplyMarkup = &keyboard
	b.api.Send(edit) //nolint:errcheck
}

// renderChangelogKeyboard edits (or sends) the changelog ON/OFF sub-screen.
func (b *Bot) renderChangelogKeyboard(ctx context.Context, chatID int64, messageID int, groupID int64, lz *i18n.Localizer) {
	changelogBtnKey := i18n.BtnChangelogOff
	if group, err := b.client.GetGroupByID(ctx, groupID); err == nil && group != nil && group.ChangelogEnabled {
		changelogBtnKey = i18n.BtnChangelogOn
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(changelogBtnKey), fmt.Sprintf("toggle_changelog:%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnBack), fmt.Sprintf("group_cfg:%d", groupID)),
		),
	)

	if messageID != 0 {
		edit := tgbotapi.NewEditMessageText(chatID, messageID, lz.T(i18n.MsgChangelogConfigTitle))
		edit.ReplyMarkup = &keyboard
		b.api.Send(edit) //nolint:errcheck
	} else {
		msg := tgbotapi.NewMessage(chatID, lz.T(i18n.MsgChangelogConfigTitle))
		msg.ReplyMarkup = keyboard
		b.api.Send(msg) //nolint:errcheck
	}
}

// handleChangelogConfig shows the changelog sub-screen for the given group.
func (b *Bot) handleChangelogConfig(ctx context.Context, cb *tgbotapi.CallbackQuery, groupID int64) {
	lz := b.userLocalizer(ctx, cb.From)

	isAdmin, err := b.isAdminInGroup(cb.From.ID, groupID)
	if err != nil || !isAdmin {
		b.answerCallback(cb.ID, lz.T(i18n.MsgOnlyAdminSetLanguage))
		return
	}

	b.answerCallback(cb.ID, "")
	b.renderChangelogKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, groupID, lz)
}

// handleTrigger calls the management service to run a scheduled event on demand.
// Only users listed in serviceAdminIDs are allowed.
func (b *Bot) handleTrigger(ctx context.Context, cb *tgbotapi.CallbackQuery, event string) {
	lz := b.userLocalizer(ctx, cb.From)

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

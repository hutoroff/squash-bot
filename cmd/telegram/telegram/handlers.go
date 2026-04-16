package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf16"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/gameformat"
	"github.com/hutoroff/squash-bot/internal/i18n"
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
		b.pendingManageCourtsToggle.Delete(msg.Chat.ID)
		b.pendingNewGameWizard.Delete(msg.Chat.ID)
		b.pendingVenueWizard.Delete(msg.Chat.ID)
		b.pendingVenueEdit.Delete(msg.Chat.ID)
		b.pendingVenueGameDaysEdit.Delete(msg.Chat.ID)
		b.pendingVenuePreferredTimeEdit.Delete(msg.Chat.ID)
		b.pendingGroupVenuePick.Delete(msg.Chat.ID)
		b.pendingVenueCredAdd.Delete(msg.Chat.ID)
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
		if raw, ok := b.pendingVenueWizard.Load(msg.Chat.ID); ok {
			b.processVenueWizard(ctx, msg, raw.(*venueWizard))
			return
		}
		if raw, ok := b.pendingVenueEdit.Load(msg.Chat.ID); ok {
			b.processVenueEdit(ctx, msg, raw.(*venueEditState))
			return
		}
		if raw, ok := b.pendingVenueCredAdd.Load(msg.Chat.ID); ok {
			b.processVenueCredWizard(ctx, msg, raw.(*venueCredWizard))
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
	handler, ok := b.callbackRouter[action]
	if !ok {
		slog.Debug("unknown callback action", "action", action)
		b.answerCallback(cb.ID, "")
		return
	}
	handler(ctx, cb, rawID)
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
	return gameformat.GameKeyboard(gameID, lz)
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

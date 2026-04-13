package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

// handleCommand dispatches /command messages from private chats.
func (b *Bot) handleCommand(ctx context.Context, msg *tgbotapi.Message) {
	// Extract command name: first word, lowercased, stripping @botname suffix.
	raw := strings.Fields(msg.Text)[0]
	if idx := strings.Index(raw, "@"); idx >= 0 {
		raw = raw[:idx]
	}
	cmd := strings.ToLower(raw)

	lz := b.userLocalizer(msg.From.LanguageCode)

	switch cmd {
	case "/start", "/help":
		b.handleCommandHelp(ctx, msg, lz)
	case "/mygame":
		b.handleCommandMyGame(ctx, msg, lz)
	case "/games":
		b.handleCommandGames(ctx, msg, lz)
	case "/newgame":
		b.handleCommandNewGame(ctx, msg, lz)
	case "/language":
		b.handleCommandLanguage(ctx, msg, lz)
	case "/venues":
		b.handleCommandVenues(ctx, msg, lz)
	case "/trigger":
		b.handleCommandTrigger(ctx, msg, lz)
	default:
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgUnknownCommand))
	}
}

func (b *Bot) handleCommandHelp(ctx context.Context, msg *tgbotapi.Message, lz *i18n.Localizer) {
	adminGroupIDs := b.adminGroups(msg.From.ID)
	isAdmin := len(adminGroupIDs) > 0

	var sb strings.Builder
	sb.WriteString(lz.T(i18n.MsgAvailableCommands))
	sb.WriteString(lz.T(i18n.MsgCmdMyGame))
	sb.WriteString(lz.T(i18n.MsgCmdHelp))

	if isAdmin {
		sb.WriteString(lz.T(i18n.MsgAdminCommands))
		sb.WriteString(lz.T(i18n.MsgCmdNewGame))
		sb.WriteString(lz.T(i18n.MsgCmdGames))
		sb.WriteString(lz.T(i18n.MsgCmdVenues))
		sb.WriteString(lz.T(i18n.MsgCmdLanguage))
	}

	if b.serviceAdminIDs[msg.From.ID] {
		sb.WriteString(lz.T(i18n.MsgServiceAdminCommands))
		sb.WriteString(lz.T(i18n.MsgCmdTrigger))
	}

	out := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
	out.ParseMode = "Markdown"
	if _, err := b.api.Send(out); err != nil {
		slog.Error("handleCommandHelp: send", "err", err)
	}
}

func (b *Bot) handleCommandMyGame(ctx context.Context, msg *tgbotapi.Message, lz *i18n.Localizer) {
	game, err := b.client.GetNextGameForTelegramUser(ctx, msg.From.ID)
	if err != nil {
		slog.Error("handleCommandMyGame: get next game", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgFailedFetchGame))
		return
	}

	if game == nil {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgNoUpcomingRegistered))
		return
	}

	participations, err := b.client.GetParticipations(ctx, game.ID)
	if err != nil {
		slog.Error("handleCommandMyGame: get participations", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgFailedFetchDetails))
		return
	}
	guests, err := b.client.GetGuests(ctx, game.ID)
	if err != nil {
		slog.Error("handleCommandMyGame: get guests", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgFailedFetchDetails))
		return
	}

	loc := b.groupLocation(ctx, game.ChatID)
	text := lz.T(i18n.MsgYourNextGame) + FormatGameMessage(game, participations, guests, loc, time.Now(), lz)

	out := tgbotapi.NewMessage(msg.Chat.ID, text)

	// Add a URL button linking to the original group message if available.
	if game.MessageID != nil {
		if link := superGroupMessageLink(game.ChatID, *game.MessageID); link != "" {
			keyboard := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonURL(lz.T(i18n.BtnViewInGroup), link),
				),
			)
			out.ReplyMarkup = keyboard
		}
	}

	if _, err := b.api.Send(out); err != nil {
		slog.Error("handleCommandMyGame: send", "err", err)
	}
}

func (b *Bot) handleCommandGames(ctx context.Context, msg *tgbotapi.Message, lz *i18n.Localizer) {
	adminGroupIDs := b.adminGroups(msg.From.ID)
	if len(adminGroupIDs) == 0 {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgOnlyAdminCanUse))
		return
	}

	games, err := b.client.GetUpcomingGamesByChatIDs(ctx, adminGroupIDs)
	if err != nil {
		slog.Error("handleCommandGames: get games", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgFailedFetchGames))
		return
	}

	groups, err := b.client.GetGroups(ctx)
	if err != nil {
		slog.Error("handleCommandGames: get groups", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgFailedFetchGroupInfo))
		return
	}

	if len(games) == 0 {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgNoUpcomingGames))
		return
	}

	text, keyboard := formatGamesListMessage(games, groups, lz)
	out := tgbotapi.NewMessage(msg.Chat.ID, text)
	out.ReplyMarkup = keyboard
	out.ParseMode = "Markdown"
	if _, err := b.api.Send(out); err != nil {
		slog.Error("handleCommandGames: send", "err", err)
	}
}

func (b *Bot) handleCommandNewGame(ctx context.Context, msg *tgbotapi.Message, lz *i18n.Localizer) {
	adminGroupIDs := b.adminGroups(msg.From.ID)
	if len(adminGroupIDs) == 0 {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgOnlyAdminCreateGames))
		return
	}

	// For single-group admins, ensure at least one venue is configured before
	// showing the date picker — gives immediate feedback instead of failing mid-flow.
	// Also resolve the group timezone so the date picker shows the correct "today".
	loc := b.loc
	if len(adminGroupIDs) == 1 {
		venues, err := b.client.GetVenuesByGroup(ctx, adminGroupIDs[0])
		if err != nil {
			slog.Error("handleCommandNewGame: fetch venues", "err", err)
			b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgSomethingWentWrong))
			return
		}
		if len(venues) == 0 {
			b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgNewGameNoVenuesConfigured))
			return
		}
		loc = b.groupLocation(ctx, adminGroupIDs[0])
	}

	keyboard := b.buildDateSelectionKeyboard(lz, loc)
	out := tgbotapi.NewMessage(msg.Chat.ID, lz.T(i18n.MsgNewGameSelectDate))
	out.ReplyMarkup = keyboard
	if _, err := b.api.Send(out); err != nil {
		slog.Error("handleCommandNewGame: send date keyboard", "err", err)
	}
}

// handleCommandLanguage lets a group admin set the language for one of their groups.
func (b *Bot) handleCommandLanguage(ctx context.Context, msg *tgbotapi.Message, lz *i18n.Localizer) {
	adminGroupIDs := b.adminGroups(msg.From.ID)
	if len(adminGroupIDs) == 0 {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgOnlyAdminSetLanguage))
		return
	}

	if len(adminGroupIDs) == 1 {
		// Show language selection directly.
		b.renderLanguageKeyboard(msg.Chat.ID, 0, adminGroupIDs[0], lz)
		return
	}

	// Multiple groups — first pick the group.
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
			tgbotapi.NewInlineKeyboardButtonData(title, fmt.Sprintf("set_lang_group:%d", gid)),
		))
	}
	out := tgbotapi.NewMessage(msg.Chat.ID, lz.T(i18n.MsgSelectGroupForLanguage))
	out.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	if _, err := b.api.Send(out); err != nil {
		slog.Error("handleCommandLanguage: send", "err", err)
	}
}

func (b *Bot) handleCommandTrigger(ctx context.Context, msg *tgbotapi.Message, lz *i18n.Localizer) {
	if !b.serviceAdminIDs[msg.From.ID] {
		b.reply(msg.Chat.ID, msg.MessageID, lz.T(i18n.MsgNotAuthorizedCmd))
		return
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnCancellationReminder), "trigger:cancellation_reminder"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnDayAfterCleanup), "trigger:day_after_cleanup"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnBookingReminder), "trigger:booking_reminder"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lz.T(i18n.BtnAutoBooking), "trigger:auto_booking"),
		),
	)

	out := tgbotapi.NewMessage(msg.Chat.ID, lz.T(i18n.MsgSelectTriggerEvent))
	out.ReplyMarkup = keyboard
	if _, err := b.api.Send(out); err != nil {
		slog.Error("handleCommandTrigger: send", "err", err)
	}
}

// superGroupMessageLink returns a t.me deep link for a supergroup message.
// Supergroup chat IDs have format -100XXXXXXXXX; regular groups have no public link.
func superGroupMessageLink(chatID, messageID int64) string {
	s := strconv.FormatInt(chatID, 10)
	if !strings.HasPrefix(s, "-100") {
		return ""
	}
	channelID := s[4:] // strip "-100" prefix
	return fmt.Sprintf("https://t.me/c/%s/%d", channelID, messageID)
}

// formatGamesListMessage builds the text and keyboard for the /games admin view.
func formatGamesListMessage(games []*models.Game, groups []models.Group, lz *i18n.Localizer) (string, tgbotapi.InlineKeyboardMarkup) {
	groupTitles := make(map[int64]string, len(groups))
	groupLocs := make(map[int64]*time.Location, len(groups))
	for _, g := range groups {
		groupTitles[g.ChatID] = g.Title
		loc, err := time.LoadLocation(g.Timezone)
		if err != nil {
			loc = time.UTC
		}
		groupLocs[g.ChatID] = loc
	}

	var sb strings.Builder
	sb.WriteString(lz.T(i18n.MsgUpcomingGames))

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, game := range games {
		loc := groupLocs[game.ChatID]
		if loc == nil {
			loc = time.UTC
		}
		localDate := game.GameDate.In(loc)
		title := groupTitles[game.ChatID]
		if title == "" {
			title = fmt.Sprintf("Chat %d", game.ChatID)
		}

		sb.WriteString(fmt.Sprintf("📅 %s · %s\n", lz.FormatGameDate(localDate), localDate.Format("15:04")))
		sb.WriteString(lz.Tf(i18n.MsgGameCourtsCapacity, escapeMarkdown(game.Courts), game.CourtsCount*2))
		sb.WriteString(lz.Tf(i18n.MsgGroupLabel, escapeMarkdown(title)))

		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				lz.Tf(i18n.MsgManageGameBtn, lz.FormatDayMonth(localDate), localDate.Format("15:04")),
				fmt.Sprintf("manage:%d", game.ID),
			),
		))
	}

	return sb.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"`", "\\`",
	)
	return replacer.Replace(s)
}

package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/vkhutorov/squash_bot/internal/models"
	"github.com/vkhutorov/squash_bot/internal/storage"
)

// handleCommand dispatches /command messages from private chats.
func (b *Bot) handleCommand(ctx context.Context, msg *tgbotapi.Message) {
	// Extract command name: first word, lowercased, stripping @botname suffix.
	raw := strings.Fields(msg.Text)[0]
	if idx := strings.Index(raw, "@"); idx >= 0 {
		raw = raw[:idx]
	}
	cmd := strings.ToLower(raw)

	switch cmd {
	case "/start", "/help":
		b.handleCommandHelp(ctx, msg)
	case "/my_game":
		b.handleCommandMyGame(ctx, msg)
	case "/games":
		b.handleCommandGames(ctx, msg)
	case "/new_game":
		b.handleCommandNewGame(ctx, msg)
	default:
		b.reply(msg.Chat.ID, msg.MessageID, "Unknown command. Send /help to see available commands.")
	}
}

func (b *Bot) handleCommandHelp(ctx context.Context, msg *tgbotapi.Message) {
	adminGroupIDs := b.adminGroups(msg.From.ID)
	isAdmin := len(adminGroupIDs) > 0

	var sb strings.Builder
	sb.WriteString("Available commands:\n")
	sb.WriteString("/my\\_game — Show your next upcoming game\n")
	sb.WriteString("/help — Show this help message\n")

	if isAdmin {
		sb.WriteString("\nAdmin commands:\n")
		sb.WriteString("/new\\_game — Create a new game\n")
		sb.WriteString("/games — Show and manage upcoming games\n")
	}

	out := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
	out.ParseMode = "Markdown"
	if _, err := b.api.Send(out); err != nil {
		slog.Error("handleCommandHelp: send", "err", err)
	}
}

func (b *Bot) handleCommandMyGame(ctx context.Context, msg *tgbotapi.Message) {
	game, err := b.gameService.GetNextGameForTelegramUser(ctx, msg.From.ID)
	if err != nil {
		slog.Error("handleCommandMyGame: get next game", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, "Failed to fetch your next game. Please try again.")
		return
	}

	if game == nil {
		b.reply(msg.Chat.ID, msg.MessageID, "You have no upcoming registered games.")
		return
	}

	participations, err := b.partService.GetParticipations(ctx, game.ID)
	if err != nil {
		slog.Error("handleCommandMyGame: get participations", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, "Failed to fetch game details. Please try again.")
		return
	}
	guests, err := b.partService.GetGuests(ctx, game.ID)
	if err != nil {
		slog.Error("handleCommandMyGame: get guests", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, "Failed to fetch game details. Please try again.")
		return
	}

	text := "Your next game:\n\n" + FormatGameMessage(game, participations, guests, b.loc)

	out := tgbotapi.NewMessage(msg.Chat.ID, text)

	// Add a URL button linking to the original group message if available.
	if game.MessageID != nil {
		if link := superGroupMessageLink(game.ChatID, *game.MessageID); link != "" {
			keyboard := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonURL("View in group →", link),
				),
			)
			out.ReplyMarkup = keyboard
		}
	}

	if _, err := b.api.Send(out); err != nil {
		slog.Error("handleCommandMyGame: send", "err", err)
	}
}

func (b *Bot) handleCommandGames(ctx context.Context, msg *tgbotapi.Message) {
	adminGroupIDs := b.adminGroups(msg.From.ID)
	if len(adminGroupIDs) == 0 {
		b.reply(msg.Chat.ID, msg.MessageID, "Only group administrators can use this command.")
		return
	}

	games, err := b.gameService.GetUpcomingGamesByChatIDs(ctx, adminGroupIDs)
	if err != nil {
		slog.Error("handleCommandGames: get games", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, "Failed to fetch games. Please try again.")
		return
	}

	groups, err := b.groupRepo.GetAll(ctx)
	if err != nil {
		slog.Error("handleCommandGames: get groups", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, "Failed to fetch group info. Please try again.")
		return
	}

	if len(games) == 0 {
		b.reply(msg.Chat.ID, msg.MessageID, "No upcoming games in your groups.")
		return
	}

	text, keyboard := formatGamesListMessage(games, groups, b.loc)
	out := tgbotapi.NewMessage(msg.Chat.ID, text)
	out.ReplyMarkup = keyboard
	out.ParseMode = "Markdown"
	if _, err := b.api.Send(out); err != nil {
		slog.Error("handleCommandGames: send", "err", err)
	}
}

func (b *Bot) handleCommandNewGame(ctx context.Context, msg *tgbotapi.Message) {
	adminGroupIDs := b.adminGroups(msg.From.ID)
	if len(adminGroupIDs) == 0 {
		b.reply(msg.Chat.ID, msg.MessageID, "Only group administrators can create games.")
		return
	}

	// Strip the "/new_game" first line, pass the rest to parseAdminCommand.
	text := strings.TrimSpace(msg.Text)
	lines := strings.SplitN(text, "\n", 2)
	if len(lines) < 2 || strings.TrimSpace(lines[1]) == "" {
		b.reply(msg.Chat.ID, msg.MessageID,
			"Send the game details after the command:\n\n/new\\_game\nYYYY-MM-DD HH:MM\ncourts: 2,3,4")
		return
	}
	body := strings.TrimSpace(lines[1])

	gameDate, courts, err := parseAdminCommand(body, b.loc)
	if err != nil {
		b.reply(msg.Chat.ID, msg.MessageID,
			"Invalid format. Use:\n\n/new\\_game\nYYYY-MM-DD HH:MM\ncourts: 2,3,4")
		return
	}

	if len(adminGroupIDs) == 1 {
		b.createAndAnnounceGame(ctx, msg.Chat.ID, msg.MessageID, adminGroupIDs[0], gameDate, courts)
		return
	}

	// Admin manages multiple groups — ask which one to post to.
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

// processCourtsEdit handles the admin's text response after clicking "Edit Courts".
func (b *Bot) processCourtsEdit(ctx context.Context, msg *tgbotapi.Message, gameID int64) {
	courts := strings.TrimSpace(msg.Text)
	if courts == "" {
		b.reply(msg.Chat.ID, msg.MessageID, "Invalid format. Expected courts like: 2,3,4")
		return
	}

	// Validate: must be non-empty comma-separated values within length limit.
	if len(courts) > maxCourtsLen {
		b.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("Courts string too long (max %d chars)", maxCourtsLen))
		return
	}
	parts := strings.Split(courts, ",")
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			b.reply(msg.Chat.ID, msg.MessageID, "Invalid format. Expected courts like: 2,3,4")
			return
		}
	}

	// Re-fetch the game to get the chat ID needed for the admin check.
	game, err := b.gameService.GetByID(ctx, gameID)
	if err != nil {
		slog.Error("processCourtsEdit: get game", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, "Game not found.")
		return
	}

	// Re-verify admin status before persisting changes.
	isAdmin, err := b.isAdminInGroup(msg.From.ID, game.ChatID)
	if err != nil {
		slog.Error("processCourtsEdit: check admin", "err", err, "user_id", msg.From.ID, "chat_id", game.ChatID)
		b.reply(msg.Chat.ID, msg.MessageID, "Failed to verify permissions.")
		return
	}
	if !isAdmin {
		b.reply(msg.Chat.ID, msg.MessageID, "You no longer have admin access to this group.")
		return
	}

	if err := b.gameService.UpdateCourts(ctx, gameID, courts); err != nil {
		slog.Error("processCourtsEdit: update courts", "err", err, "game_id", gameID)
		b.reply(msg.Chat.ID, msg.MessageID, "Failed to update courts. Please try again.")
		return
	}

	slog.Info("Courts updated", "game_id", gameID, "courts", courts)

	// Re-fetch the game to pick up the new courts value for the group message.
	game, err = b.gameService.GetByID(ctx, gameID)
	if err != nil {
		slog.Error("processCourtsEdit: get game after update", "err", err)
		b.reply(msg.Chat.ID, msg.MessageID, "Courts updated, but failed to refresh the group message.")
		return
	}

	if game.MessageID != nil {
		participations, err := b.partService.GetParticipations(ctx, gameID)
		if err != nil {
			slog.Error("processCourtsEdit: get participations", "err", err)
		} else {
			guests, err := b.partService.GetGuests(ctx, gameID)
			if err != nil {
				slog.Error("processCourtsEdit: get guests", "err", err)
			} else {
				b.editGameMessage(game.ChatID, int(*game.MessageID), game, participations, guests)
			}
		}
	}

	b.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("Courts updated to: %s ✓", courts))
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
func formatGamesListMessage(games []*models.Game, groups []storage.BotGroup, loc *time.Location) (string, tgbotapi.InlineKeyboardMarkup) {
	groupTitles := make(map[int64]string, len(groups))
	for _, g := range groups {
		groupTitles[g.ChatID] = g.Title
	}

	var sb strings.Builder
	sb.WriteString("*Upcoming games:*\n\n")

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, game := range games {
		localDate := game.GameDate.In(loc)
		title := groupTitles[game.ChatID]
		if title == "" {
			title = fmt.Sprintf("Chat %d", game.ChatID)
		}

		sb.WriteString(fmt.Sprintf("📅 %s · %s\n",
			formatGameDate(localDate), localDate.Format("15:04")))
		sb.WriteString(fmt.Sprintf("🎾 Courts: %s — capacity %d\n",
			escapeMarkdown(game.Courts), game.CourtsCount*2))
		sb.WriteString(fmt.Sprintf("Group: %s\n\n", escapeMarkdown(title)))

		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("Manage: %s %s", localDate.Format("02 Jan"), localDate.Format("15:04")),
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

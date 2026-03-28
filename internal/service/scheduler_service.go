package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/vkhutorov/squash_bot/internal/models"
	"github.com/vkhutorov/squash_bot/internal/storage"
)

type SchedulerService struct {
	api       *tgbotapi.BotAPI
	gameRepo  *storage.GameRepo
	partRepo  *storage.ParticipationRepo
	guestRepo *storage.GuestRepo
	chatID    int64
	loc       *time.Location
	logger    *slog.Logger
}

func NewSchedulerService(
	api *tgbotapi.BotAPI,
	gameRepo *storage.GameRepo,
	partRepo *storage.ParticipationRepo,
	guestRepo *storage.GuestRepo,
	chatID int64,
	loc *time.Location,
	logger *slog.Logger,
) *SchedulerService {
	return &SchedulerService{
		api:       api,
		gameRepo:  gameRepo,
		partRepo:  partRepo,
		guestRepo: guestRepo,
		chatID:    chatID,
		loc:       loc,
		logger:    logger,
	}
}

// RunDayBeforeCheck checks tomorrow's games and sends notifications if player count != capacity.
func (s *SchedulerService) RunDayBeforeCheck() {
	s.logger.Info("day-before check started")
	ctx := context.Background()

	now := time.Now().In(s.loc)
	tomorrow := now.AddDate(0, 0, 1)
	from := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 0, 0, 0, s.loc)
	to := from.AddDate(0, 0, 1)

	games, err := s.gameRepo.GetGamesForDayBefore(ctx, from, to)
	if err != nil {
		s.logger.Error("day-before check: query games", "err", err)
		return
	}
	s.logger.Info("found games", "count", len(games))

	for _, game := range games {
		s.processDayBefore(ctx, game)
	}
}

func (s *SchedulerService) processDayBefore(ctx context.Context, game *models.Game) {
	registeredCount, err := s.partRepo.GetRegisteredCount(ctx, game.ID)
	if err != nil {
		s.logger.Error("day-before check: get registered count", "game_id", game.ID, "err", err)
		return
	}

	guestCount, err := s.guestRepo.GetCountByGame(ctx, game.ID)
	if err != nil {
		s.logger.Error("day-before check: get guest count", "game_id", game.ID, "err", err)
		return
	}

	count := registeredCount + guestCount
	capacity := game.CourtsCount * 2
	var action, text string

	switch {
	case count > capacity:
		action = "over_capacity"
		text = fmt.Sprintf(
			"⚠️ Too many players! %d registered but only %d spots (%d courts). Consider booking an extra court.",
			count, capacity, game.CourtsCount,
		)
	case count < capacity:
		action = "under_capacity"
		text = fmt.Sprintf(
			"📢 Free spots available! %d/%d players registered (%d courts). Invite more friends!",
			count, capacity, game.CourtsCount,
		)
	default:
		action = "skipped"
	}

	s.logger.Info("Day-before check",
		"game_id", game.ID,
		"players", count,
		"capacity", capacity,
		"action", action,
	)

	if action == "skipped" {
		return
	}

	msg := tgbotapi.NewMessage(s.chatID, text)
	if _, err := s.api.Send(msg); err != nil {
		s.logger.Error("day-before check: send notification", "game_id", game.ID, "err", err)
		return
	}

	if err := s.gameRepo.MarkNotifiedDayBefore(ctx, game.ID); err != nil {
		s.logger.Error("day-before check: mark notified", "game_id", game.ID, "err", err)
	}
}

// RunDayAfterCleanup unpins and closes yesterday's games.
func (s *SchedulerService) RunDayAfterCleanup() {
	ctx := context.Background()

	now := time.Now().In(s.loc)
	yesterday := now.AddDate(0, 0, -1)
	from := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, s.loc)
	to := from.AddDate(0, 0, 1)

	games, err := s.gameRepo.GetGamesForDayAfter(ctx, from, to)
	if err != nil {
		s.logger.Error("day-after cleanup: query games", "err", err)
		return
	}

	for _, game := range games {
		s.processDayAfter(ctx, game)
	}
}

func (s *SchedulerService) processDayAfter(ctx context.Context, game *models.Game) {
	messageID := int(*game.MessageID)

	unpin := tgbotapi.UnpinChatMessageConfig{
		ChatID:    game.ChatID,
		MessageID: messageID,
	}
	if _, err := s.api.Request(unpin); err != nil {
		s.logger.Error("day-after cleanup: unpin message", "game_id", game.ID, "message_id", messageID, "err", err)
		// Continue — still remove buttons and mark completed
	}

	participations, err := s.partRepo.GetByGame(ctx, game.ID)
	if err != nil {
		s.logger.Error("day-after cleanup: get participants", "game_id", game.ID, "err", err)
		return
	}

	guests, err := s.guestRepo.GetByGame(ctx, game.ID)
	if err != nil {
		s.logger.Error("day-after cleanup: get guests", "game_id", game.ID, "err", err)
		return
	}

	text := formatCompletedMessage(game, participations, guests, s.loc)
	edit := tgbotapi.NewEditMessageText(game.ChatID, messageID, text)
	// Empty keyboard explicitly removes the inline buttons
	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	edit.ReplyMarkup = &emptyKeyboard

	if _, err := s.api.Send(edit); err != nil {
		s.logger.Error("day-after cleanup: edit message", "game_id", game.ID, "err", err)
		return
	}

	if err := s.gameRepo.MarkCompleted(ctx, game.ID); err != nil {
		s.logger.Error("day-after cleanup: mark completed", "game_id", game.ID, "err", err)
		return
	}

	s.logger.Info("Day-after cleanup",
		"game_id", game.ID,
		"message_id", messageID,
		"unpinned", true,
		"buttons_removed", true,
	)
}

// RunWeeklyReminder sends a DM to every non-bot group admin if no game is scheduled in the next 7 days.
// NOTE: Telegram only allows bots to DM users who have previously started a private chat with the bot.
func (s *SchedulerService) RunWeeklyReminder() {
	s.logger.Info("weekly reminder check started")
	ctx := context.Background()

	// Check if a game is already scheduled in the next 7 days.
	now := time.Now().In(s.loc)
	weekEnd := now.AddDate(0, 0, 7)

	games, err := s.gameRepo.GetUpcomingGames(ctx)
	if err != nil {
		s.logger.Error("weekly reminder: query upcoming games", "err", err)
		return
	}
	for _, g := range games {
		if g.GameDate.Before(weekEnd) {
			s.logger.Info("weekly reminder: game already scheduled, skipping", "game_id", g.ID, "game_date", g.GameDate)
			return
		}
	}

	// No game this week — notify all group admins via DM.
	admins, err := s.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
		ChatConfig: tgbotapi.ChatConfig{ChatID: s.chatID},
	})
	if err != nil {
		s.logger.Error("weekly reminder: get chat administrators", "err", err)
		return
	}

	text := "👋 Reminder: no squash game has been scheduled for this week yet. Don't forget to create one!"

	notified := 0
	for _, admin := range admins {
		if admin.User.IsBot {
			continue
		}
		msg := tgbotapi.NewMessage(admin.User.ID, text)
		if _, err := s.api.Send(msg); err != nil {
			s.logger.Error("weekly reminder: send DM", "user_id", admin.User.ID, "username", admin.User.UserName, "err", err)
			continue
		}
		s.logger.Info("weekly reminder: DM sent", "user_id", admin.User.ID, "username", admin.User.UserName)
		notified++
	}
	s.logger.Info("weekly reminder done", "admins_notified", notified)
}

// formatCompletedMessage renders the final game message without interactive buttons.
func formatCompletedMessage(game *models.Game, participants []*models.GameParticipation, guests []*models.GuestParticipation, loc *time.Location) string {
	capacity := game.CourtsCount * 2

	var registered []*models.GameParticipation
	for _, p := range participants {
		if p.Status == models.StatusRegistered {
			registered = append(registered, p)
		}
	}

	totalCount := len(registered) + len(guests)
	localDate := game.GameDate.In(loc)

	var sb strings.Builder
	sb.WriteString("🏸 Squash Game\n\n")
	sb.WriteString(fmt.Sprintf("📅 %s · %s\n", schedulerFormatDate(localDate), localDate.Format("15:04")))
	sb.WriteString(fmt.Sprintf("🎾 Courts: %s (capacity: %d players)\n\n", game.Courts, capacity))
	sb.WriteString(fmt.Sprintf("Players (%d/%d):\n", totalCount, capacity))

	num := 1
	for _, p := range registered {
		sb.WriteString(fmt.Sprintf("%d. %s\n", num, schedulerPlayerName(p.Player)))
		num++
	}
	for _, g := range guests {
		sb.WriteString(fmt.Sprintf("%d. +1 (by %s)\n", num, schedulerPlayerName(g.InvitedBy)))
		num++
	}

	sb.WriteString("\nGame completed ✓")
	return sb.String()
}

func schedulerFormatDate(t time.Time) string {
	return fmt.Sprintf("%s, %s %d", t.Weekday(), t.Format("January"), t.Day())
}

func schedulerPlayerName(p *models.Player) string {
	if p.Username != nil && *p.Username != "" {
		return "@" + *p.Username
	}
	var parts []string
	if p.FirstName != nil && *p.FirstName != "" {
		parts = append(parts, *p.FirstName)
	}
	if p.LastName != nil && *p.LastName != "" {
		parts = append(parts, *p.LastName)
	}
	return strings.Join(parts, " ")
}

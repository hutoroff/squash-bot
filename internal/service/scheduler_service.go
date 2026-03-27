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
	api      *tgbotapi.BotAPI
	gameRepo *storage.GameRepo
	partRepo *storage.ParticipationRepo
	chatID   int64
	loc      *time.Location
	logger   *slog.Logger
}

func NewSchedulerService(
	api *tgbotapi.BotAPI,
	gameRepo *storage.GameRepo,
	partRepo *storage.ParticipationRepo,
	chatID int64,
	loc *time.Location,
	logger *slog.Logger,
) *SchedulerService {
	return &SchedulerService{
		api:      api,
		gameRepo: gameRepo,
		partRepo: partRepo,
		chatID:   chatID,
		loc:      loc,
		logger:   logger,
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
	count, err := s.partRepo.GetRegisteredCount(ctx, game.ID)
	if err != nil {
		s.logger.Error("day-before check: get registered count", "game_id", game.ID, "err", err)
		return
	}

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

	text := formatCompletedMessage(game, participations, s.loc)
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

// formatCompletedMessage renders the final game message without interactive buttons.
func formatCompletedMessage(game *models.Game, participants []*models.GameParticipation, loc *time.Location) string {
	capacity := game.CourtsCount * 2

	var registered []*models.GameParticipation
	for _, p := range participants {
		if p.Status == models.StatusRegistered {
			registered = append(registered, p)
		}
	}

	localDate := game.GameDate.In(loc)

	var sb strings.Builder
	sb.WriteString("🏸 Squash Game\n\n")
	sb.WriteString(fmt.Sprintf("📅 %s · %s\n", schedulerFormatDate(localDate), localDate.Format("15:04")))
	sb.WriteString(fmt.Sprintf("🎾 Courts: %s (capacity: %d players)\n\n", game.Courts, capacity))
	sb.WriteString(fmt.Sprintf("Players (%d/%d):\n", len(registered), capacity))

	for i, p := range registered {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, schedulerPlayerName(p.Player)))
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

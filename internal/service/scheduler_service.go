package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/vkhutorov/squash_bot/internal/i18n"
	"github.com/vkhutorov/squash_bot/internal/models"
	"github.com/vkhutorov/squash_bot/internal/storage"
)

type SchedulerService struct {
	api       *tgbotapi.BotAPI
	gameRepo  *storage.GameRepo
	partRepo  *storage.ParticipationRepo
	guestRepo *storage.GuestRepo
	groupRepo *storage.GroupRepo
	loc       *time.Location
	logger    *slog.Logger
}

func NewSchedulerService(
	api *tgbotapi.BotAPI,
	gameRepo *storage.GameRepo,
	partRepo *storage.ParticipationRepo,
	guestRepo *storage.GuestRepo,
	groupRepo *storage.GroupRepo,
	loc *time.Location,
	logger *slog.Logger,
) *SchedulerService {
	return &SchedulerService{
		api:       api,
		gameRepo:  gameRepo,
		partRepo:  partRepo,
		guestRepo: guestRepo,
		groupRepo: groupRepo,
		loc:       loc,
		logger:    logger,
	}
}

// groupLang returns a Localizer for the given chatID's stored language.
// Falls back to English if the group is not found or the call fails.
func (s *SchedulerService) groupLang(ctx context.Context, chatID int64) *i18n.Localizer {
	group, err := s.groupRepo.GetByID(ctx, chatID)
	if err != nil || group == nil {
		return i18n.New(i18n.En)
	}
	return i18n.New(i18n.Normalize(group.Language))
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

	lz := s.groupLang(ctx, game.ChatID)

	switch {
	case count > capacity:
		action = "over_capacity"
		text = lz.Tf(i18n.SchedOverCapacity, count, capacity, game.CourtsCount)
	case count < capacity:
		action = "under_capacity"
		text = lz.Tf(i18n.SchedUnderCapacity, count, capacity, game.CourtsCount)
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

	msg := tgbotapi.NewMessage(game.ChatID, text)
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
	s.logger.Info("day-after check started")
	ctx := context.Background()

	now := time.Now().In(s.loc)
	yesterday := now.AddDate(0, 0, -1)
	from := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, s.loc)
	to := from.AddDate(0, 0, 1)
	s.logger.Debug("day-after dates", "from", from, "to", to)

	games, err := s.gameRepo.GetGamesForDayAfter(ctx, from, to)
	if err != nil {
		s.logger.Error("day-after cleanup: query games", "err", err)
		return
	}
	s.logger.Info("found games", "count", len(games))

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

	lz := s.groupLang(ctx, game.ChatID)
	text := formatCompletedMessage(game, participations, guests, s.loc, lz)
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

// adminEntry pairs a chat member with the localizer for their group.
type adminEntry struct {
	member tgbotapi.ChatMember
	lang   *i18n.Localizer
}

// RunWeeklyReminder sends a DM to every non-bot group admin if no game is scheduled in the next 7 days.
// NOTE: Telegram only allows bots to DM users who have previously started a private chat with the bot.
func (s *SchedulerService) RunWeeklyReminder() {
	s.logger.Info("weekly reminder check started")
	ctx := context.Background()

	// Build a set of chat IDs that already have a game within the next 7 days.
	now := time.Now().In(s.loc)
	weekEnd := now.AddDate(0, 0, 7)

	upcomingGames, err := s.gameRepo.GetUpcomingGames(ctx)
	if err != nil {
		s.logger.Error("weekly reminder: query upcoming games", "err", err)
		return
	}
	chatIDsWithGame := make(map[int64]bool)
	for _, g := range upcomingGames {
		if g.GameDate.Before(weekEnd) {
			chatIDsWithGame[g.ChatID] = true
		}
	}

	groups, err := s.groupRepo.GetAll(ctx)
	if err != nil {
		s.logger.Error("weekly reminder: get groups", "err", err)
		return
	}

	// Collect admins only from groups that have no game scheduled this week.
	// Each admin is DM'd once, using the language of the first group they're found in.
	seen := make(map[int64]bool)
	var allAdmins []adminEntry
	for _, g := range groups {
		chatID := g.ChatID
		if chatIDsWithGame[chatID] {
			s.logger.Info("weekly reminder: game already scheduled, skipping group", "chat_id", chatID)
			continue
		}
		lz := i18n.New(i18n.Normalize(g.Language))
		admins, err := s.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
			ChatConfig: tgbotapi.ChatConfig{ChatID: chatID},
		})
		if err != nil {
			s.logger.Error("weekly reminder: get chat administrators", "chat_id", chatID, "err", err)
			continue
		}
		for _, admin := range admins {
			if !admin.User.IsBot && !seen[admin.User.ID] {
				seen[admin.User.ID] = true
				allAdmins = append(allAdmins, adminEntry{member: admin, lang: lz})
			}
		}
	}

	if len(allAdmins) == 0 {
		s.logger.Info("weekly reminder: all groups have games scheduled, no DMs needed")
		return
	}

	notified := 0
	for _, entry := range allAdmins {
		text := entry.lang.T(i18n.SchedWeeklyReminder)
		msg := tgbotapi.NewMessage(entry.member.User.ID, text)
		if _, err := s.api.Send(msg); err != nil {
			s.logger.Error("weekly reminder: send DM", "user_id", entry.member.User.ID, "username", entry.member.User.UserName, "err", err)
			continue
		}
		s.logger.Info("weekly reminder: DM sent", "user_id", entry.member.User.ID, "username", entry.member.User.UserName)
		notified++
	}
	s.logger.Info("weekly reminder done", "admins_notified", notified)
}

// formatCompletedMessage renders the final game message without interactive buttons.
func formatCompletedMessage(game *models.Game, participants []*models.GameParticipation, guests []*models.GuestParticipation, loc *time.Location, lz *i18n.Localizer) string {
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
	sb.WriteString(lz.T(i18n.GameHeader) + "\n\n")
	sb.WriteString(fmt.Sprintf("📅 %s · %s\n", lz.FormatGameDate(localDate), localDate.Format("15:04")))
	sb.WriteString(lz.Tf(i18n.GameCourts, game.Courts, capacity) + "\n\n")
	sb.WriteString(lz.Tf(i18n.GamePlayers, totalCount, capacity) + "\n")

	num := 1
	for _, p := range registered {
		sb.WriteString(fmt.Sprintf("%d. %s\n", num, schedulerPlayerName(p.Player)))
		num++
	}
	for _, g := range guests {
		sb.WriteString(fmt.Sprintf("%d. %s\n", num, lz.Tf(i18n.GameGuestLine, schedulerPlayerName(g.InvitedBy))))
		num++
	}

	sb.WriteString("\n" + lz.T(i18n.GameCompleted))
	return sb.String()
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

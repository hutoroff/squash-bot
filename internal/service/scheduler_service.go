package service

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
	"github.com/vkhutorov/squash_bot/internal/storage"
)

type SchedulerService struct {
	api        *tgbotapi.BotAPI
	gameRepo   *storage.GameRepo
	partRepo   *storage.ParticipationRepo
	guestRepo  *storage.GuestRepo
	groupRepo  *storage.GroupRepo
	venueRepo  *storage.VenueRepo
	loc        *time.Location
	logger     *slog.Logger
	pollWindow time.Duration // half the configured poll interval; used as timing gate for reminders
}

func NewSchedulerService(
	api *tgbotapi.BotAPI,
	gameRepo *storage.GameRepo,
	partRepo *storage.ParticipationRepo,
	guestRepo *storage.GuestRepo,
	groupRepo *storage.GroupRepo,
	venueRepo *storage.VenueRepo,
	loc *time.Location,
	logger *slog.Logger,
	pollWindow time.Duration,
) *SchedulerService {
	return &SchedulerService{
		api:        api,
		gameRepo:   gameRepo,
		partRepo:   partRepo,
		guestRepo:  guestRepo,
		groupRepo:  groupRepo,
		venueRepo:  venueRepo,
		loc:        loc,
		logger:     logger,
		pollWindow: pollWindow,
	}
}

// RunScheduledTasks is called by the single poll cron (default every 5 minutes).
// It dispatches to all three scheduler tasks.
func (s *SchedulerService) RunScheduledTasks() {
	s.RunCancellationReminders()
	s.RunBookingReminders()
	s.RunDayAfterCleanup()
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

// groupTimezone loads the IANA timezone for a group, falling back to the service default.
func (s *SchedulerService) groupTimezone(group *models.Group) *time.Location {
	if group.Timezone == "" {
		return s.loc
	}
	loc, err := time.LoadLocation(group.Timezone)
	if err != nil {
		s.logger.Warn("invalid group timezone, using service default", "timezone", group.Timezone, "chat_id", group.ChatID)
		return s.loc
	}
	return loc
}

// RunCancellationReminders fires capacity notifications 6 hours before the cancellation
// grace period ends for each upcoming game. The grace period is configured per venue
// (default 24 hours). The reminder fires when:
//
//	now ≈ game_date − grace_period_hours − 6h  (within ±pollWindow)
func (s *SchedulerService) RunCancellationReminders() {
	s.logger.Info("cancellation reminder check started")
	ctx := context.Background()
	now := time.Now()

	games, err := s.gameRepo.GetUpcomingUnnotifiedGames(ctx)
	if err != nil {
		s.logger.Error("cancellation reminder: query games", "err", err)
		return
	}
	s.logger.Info("upcoming unnotified games", "count", len(games))

	for _, game := range games {
		gracePeriodHours := 24
		if game.Venue != nil {
			gracePeriodHours = game.Venue.GracePeriodHours
		}
		reminderAt := game.GameDate.Add(-time.Duration(gracePeriodHours+6) * time.Hour)
		diff := now.Sub(reminderAt)
		if diff < 0 {
			diff = -diff
		}
		if diff > s.pollWindow {
			continue
		}
		s.processCancellationReminder(ctx, game)
	}
}

func (s *SchedulerService) processCancellationReminder(ctx context.Context, game *models.Game) {
	registeredCount, err := s.partRepo.GetRegisteredCount(ctx, game.ID)
	if err != nil {
		s.logger.Error("cancellation reminder: get registered count", "game_id", game.ID, "err", err)
		return
	}

	guestCount, err := s.guestRepo.GetCountByGame(ctx, game.ID)
	if err != nil {
		s.logger.Error("cancellation reminder: get guest count", "game_id", game.ID, "err", err)
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

	s.logger.Info("cancellation reminder",
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
		s.logger.Error("cancellation reminder: send notification", "game_id", game.ID, "err", err)
		return
	}

	if err := s.gameRepo.MarkNotifiedDayBefore(ctx, game.ID); err != nil {
		s.logger.Error("cancellation reminder: mark notified", "game_id", game.ID, "err", err)
	}
}

// RunBookingReminders sends a DM to each group admin when court booking opens for a venue.
// Fires at 10:00–10:05 in the group's timezone on each configured game day of the week.
// NOTE: Telegram only allows bots to DM users who have previously started a private chat with the bot.
func (s *SchedulerService) RunBookingReminders() {
	s.logger.Info("booking reminder check started")
	ctx := context.Background()
	now := time.Now()

	groups, err := s.groupRepo.GetAll(ctx)
	if err != nil {
		s.logger.Error("booking reminder: get groups", "err", err)
		return
	}

	notified := 0
	for _, g := range groups {
		groupTZ := s.groupTimezone(&g)
		localNow := now.In(groupTZ)

		// Only fire in the [10:00, 10:05) window in the group's local time.
		if localNow.Hour() != 10 || localNow.Minute() >= 5 {
			continue
		}

		venues, err := s.venueRepo.GetByGroupID(ctx, g.ChatID)
		if err != nil {
			s.logger.Error("booking reminder: get venues", "chat_id", g.ChatID, "err", err)
			continue
		}

		todayWd := int(localNow.Weekday())
		todayStr := localNow.Format("2006-01-02")
		lz := i18n.New(i18n.Normalize(g.Language))

		for _, venue := range venues {
			if venue.GameDays == "" {
				continue
			}
			if !containsDay(venue.GameDays, todayWd) {
				continue
			}
			// Dedup: skip if already sent today in this group's timezone.
			if venue.LastBookingReminderAt != nil &&
				venue.LastBookingReminderAt.In(groupTZ).Format("2006-01-02") == todayStr {
				s.logger.Info("booking reminder: already sent today", "venue_id", venue.ID)
				continue
			}

			if s.sendBookingReminderToAdmins(ctx, g.ChatID, venue, lz) {
				if err := s.venueRepo.SetLastBookingReminderAt(ctx, venue.ID); err != nil {
					s.logger.Error("booking reminder: update last sent", "venue_id", venue.ID, "err", err)
				}
				notified++
			}
		}
	}
	s.logger.Info("booking reminder done", "venues_notified", notified)
}

// sendBookingReminderToAdmins DMs all non-bot group admins with the booking
// reminder. Returns true if at least one message was delivered successfully.
// SetLastBookingReminderAt should only be called when this returns true so that
// a total delivery failure (e.g. network error) does not suppress retries within
// the same scheduling window.
func (s *SchedulerService) sendBookingReminderToAdmins(ctx context.Context, chatID int64, venue *models.Venue, lz *i18n.Localizer) bool {
	admins, err := s.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
		ChatConfig: tgbotapi.ChatConfig{ChatID: chatID},
	})
	if err != nil {
		s.logger.Error("booking reminder: get chat administrators", "chat_id", chatID, "err", err)
		return false
	}

	text := lz.Tf(i18n.SchedBookingReminder, venue.Name, venue.BookingOpensDays)
	seen := make(map[int64]bool)
	sent := 0
	for _, admin := range admins {
		if admin.User.IsBot || seen[admin.User.ID] {
			continue
		}
		seen[admin.User.ID] = true
		msg := tgbotapi.NewMessage(admin.User.ID, text)
		if _, err := s.api.Send(msg); err != nil {
			s.logger.Error("booking reminder: send DM", "user_id", admin.User.ID, "venue_id", venue.ID, "err", err)
			continue
		}
		s.logger.Info("booking reminder: DM sent", "user_id", admin.User.ID, "venue_id", venue.ID)
		sent++
	}
	return sent > 0
}

// RunDayAfterCleanup unpins and closes yesterday's games.
// Runs at 03:00–03:05 in each group's local timezone.
func (s *SchedulerService) RunDayAfterCleanup() {
	s.logger.Info("day-after cleanup check started")
	ctx := context.Background()
	now := time.Now()

	groups, err := s.groupRepo.GetAll(ctx)
	if err != nil {
		s.logger.Error("day-after cleanup: get groups", "err", err)
		return
	}

	processed := 0
	for _, g := range groups {
		groupTZ := s.groupTimezone(&g)
		localNow := now.In(groupTZ)

		// Only fire in the [03:00, 03:05) window in the group's local time.
		if localNow.Hour() != 3 || localNow.Minute() >= 5 {
			continue
		}

		yesterday := localNow.AddDate(0, 0, -1)
		from := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, groupTZ)
		to := from.AddDate(0, 0, 1)

		games, err := s.gameRepo.GetUncompletedGamesByGroupAndDay(ctx, g.ChatID, from, to)
		if err != nil {
			s.logger.Error("day-after cleanup: query games", "chat_id", g.ChatID, "err", err)
			continue
		}
		s.logger.Info("day-after cleanup: found games", "chat_id", g.ChatID, "count", len(games))

		for _, game := range games {
			s.processDayAfter(ctx, game, groupTZ)
			processed++
		}
	}
	s.logger.Info("day-after cleanup done", "games_processed", processed)
}

func (s *SchedulerService) processDayAfter(ctx context.Context, game *models.Game, groupTZ *time.Location) {
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
	text := formatCompletedMessage(game, participations, guests, groupTZ, lz)
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

	s.logger.Info("day-after cleanup",
		"game_id", game.ID,
		"message_id", messageID,
		"unpinned", true,
		"buttons_removed", true,
	)
}

// containsDay reports whether the comma-separated weekday string contains the given day number.
func containsDay(gameDays string, day int) bool {
	dayStr := strconv.Itoa(day)
	for _, part := range strings.Split(gameDays, ",") {
		if strings.TrimSpace(part) == dayStr {
			return true
		}
	}
	return false
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

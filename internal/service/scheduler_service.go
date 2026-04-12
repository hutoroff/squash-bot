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
	api                    *tgbotapi.BotAPI
	gameRepo               *storage.GameRepo
	partRepo               *storage.ParticipationRepo
	guestRepo              *storage.GuestRepo
	groupRepo              *storage.GroupRepo
	venueRepo              *storage.VenueRepo
	bookingClient          BookingServiceClient // optional; nil disables automatic court cancellation and auto-booking
	loc                    *time.Location
	logger                 *slog.Logger
	pollWindow             time.Duration // half the configured poll interval; used as timing gate for reminders
	autoBookingCourtsCount int           // number of courts to book automatically at midnight
}

func NewSchedulerService(
	api *tgbotapi.BotAPI,
	gameRepo *storage.GameRepo,
	partRepo *storage.ParticipationRepo,
	guestRepo *storage.GuestRepo,
	groupRepo *storage.GroupRepo,
	venueRepo *storage.VenueRepo,
	bookingClient BookingServiceClient,
	loc *time.Location,
	logger *slog.Logger,
	pollWindow time.Duration,
	autoBookingCourtsCount int,
) *SchedulerService {
	return &SchedulerService{
		api:                    api,
		gameRepo:               gameRepo,
		partRepo:               partRepo,
		guestRepo:              guestRepo,
		groupRepo:              groupRepo,
		venueRepo:              venueRepo,
		bookingClient:          bookingClient,
		loc:                    loc,
		logger:                 logger,
		pollWindow:             pollWindow,
		autoBookingCourtsCount: autoBookingCourtsCount,
	}
}

// RunScheduledTasks is called by the single poll cron (default every 5 minutes).
// It dispatches to all scheduler tasks.
func (s *SchedulerService) RunScheduledTasks() {
	s.RunAutoBooking()
	s.RunCancellationReminders()
	s.RunBookingReminders()
	s.RunDayAfterCleanup()
}

// groupTZByID loads the IANA timezone for the group identified by chatID.
// Returns (loc, true) on success and (nil, false) on any error or not-found.
// Callers must not proceed with timezone-sensitive operations when ok is false,
// because acting on a guessed timezone can cause the wrong Eversports slot
// window to be queried or canceled.
func (s *SchedulerService) groupTZByID(ctx context.Context, chatID int64) (*time.Location, bool) {
	group, err := s.groupRepo.GetByID(ctx, chatID)
	if err != nil {
		s.logger.Error("cannot resolve group timezone", "chat_id", chatID, "err", err)
		return nil, false
	}
	if group == nil {
		s.logger.Error("cannot resolve group timezone: group not found", "chat_id", chatID)
		return nil, false
	}
	return s.groupTimezone(group), true
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
func (s *SchedulerService) RunCancellationReminders() { s.runCancellationReminders(false) }

// ForceRunCancellationReminders processes all upcoming unnotified games regardless of
// how far away their reminder time is, bypassing the ±pollWindow scheduling gate.
// Already-notified games (notified_day_before=true) are still skipped.
// Intended for manual triggers.
func (s *SchedulerService) ForceRunCancellationReminders() { s.runCancellationReminders(true) }

func (s *SchedulerService) runCancellationReminders(force bool) {
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
		if !force && diff > s.pollWindow {
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
	lz := s.groupLang(ctx, game.ChatID)

	// Determine how many courts can be fully freed (each court needs 2 players).
	courtsToCancel := 0
	if count < capacity {
		courtsToCancel = (capacity - count) / 2
	}

	// Attempt automatic court cancellation when a booking client is configured.
	// We need the group's timezone to query Eversports in local time; if the lookup
	// fails we skip cancellation entirely rather than risk querying the wrong window.
	var result *courtCancellationResult
	groupTZ, tzOK := s.groupTZByID(ctx, game.ChatID)
	if !tzOK {
		s.logger.Warn("cancellation reminder: skipping court cancellation (timezone unavailable)",
			"game_id", game.ID)
		result = buildNoOpResult(game)
	} else {
		var cancelErr error
		result, cancelErr = s.cancelUnusedCourts(ctx, game, courtsToCancel, groupTZ)
		if cancelErr != nil {
			s.logger.Error("cancellation reminder: court cancellation failed",
				"game_id", game.ID, "err", cancelErr)
			// Continue with notification even if cancellation failed; use no-op result.
			result = buildNoOpResult(game)
		}
	}

	gameDateTime := game.GameDate.Format("02.01 15:04")
	newCourtsCount := result.remainingCount
	newCapacity := newCourtsCount * 2
	canceledStr := formatCanceledCourts(result.canceledCourts)

	scenario := determineScenario(count, newCourtsCount, result.canceledCourts)

	var text string
	switch scenario {
	case "all_canceled":
		text = lz.Tf(i18n.SchedReminderAllCanceled, canceledStr, gameDateTime)
	case "canceled_balanced":
		text = lz.Tf(i18n.SchedReminderCanceled, canceledStr, gameDateTime, count, newCapacity, newCourtsCount)
	case "odd_canceled":
		text = lz.Tf(i18n.SchedReminderOddCanceled, canceledStr, gameDateTime, count, newCapacity, newCourtsCount)
	case "odd_no_cancel":
		text = lz.Tf(i18n.SchedReminderOddNoCancel, gameDateTime, count, newCapacity, newCourtsCount)
	default: // all_good
		text = lz.Tf(i18n.SchedReminderAllGood, gameDateTime, count, newCapacity, newCourtsCount)
	}

	s.logger.Info("cancellation reminder",
		"game_id", game.ID,
		"players", count,
		"capacity", capacity,
		"courts_to_cancel", courtsToCancel,
		"canceled", len(result.canceledCourts),
		"new_courts", newCourtsCount,
		"scenario", scenario,
	)

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
func (s *SchedulerService) RunBookingReminders() { s.runBookingReminders(false) }

// ForceRunBookingReminders bypasses the [10:00, 10:05) time window check.
// game_days validation and the same-day dedup guard (last_booking_reminder_at)
// still apply. Intended for manual triggers.
func (s *SchedulerService) ForceRunBookingReminders() { s.runBookingReminders(true) }

func (s *SchedulerService) runBookingReminders(force bool) {
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
		if !force && (localNow.Hour() != 10 || localNow.Minute() >= 5) {
			continue
		}

		venues, err := s.venueRepo.GetByGroupID(ctx, g.ChatID)
		if err != nil {
			s.logger.Error("booking reminder: get venues", "chat_id", g.ChatID, "err", err)
			continue
		}

		todayStr := localNow.Format("2006-01-02")
		lz := i18n.New(i18n.Normalize(g.Language))

		for _, venue := range venues {
			if venue.GameDays == "" {
				continue
			}
			if !containsDay(venue.GameDays, int(localNow.AddDate(0, 0, venue.BookingOpensDays).Weekday())) {
				continue
			}
			// Dedup: skip if already sent today in this group's timezone.
			if venue.LastBookingReminderAt != nil &&
				venue.LastBookingReminderAt.In(groupTZ).Format("2006-01-02") == todayStr {
				s.logger.Info("booking reminder: already sent today", "venue_id", venue.ID)
				continue
			}

			// Skip if a game is already created for the target date — no need to prompt.
			targetStart, targetEnd := bookingTargetWindow(localNow, venue.BookingOpensDays)
			existingGames, err := s.gameRepo.GetUncompletedGamesByGroupAndDay(ctx, g.ChatID, targetStart, targetEnd)
			if err != nil {
				s.logger.Error("booking reminder: check existing games", "venue_id", venue.ID, "err", err)
				// Fail-open: proceed with the reminder rather than silently suppressing it.
			} else if len(existingGames) > 0 {
				s.logger.Info("booking reminder: game already created for target date, skipping",
					"venue_id", venue.ID, "target_date", targetStart.Format("2006-01-02"))
				continue
			}

			// If auto-booking was done today, send a group notification instead of DM reminder.
			autoBookedToday := venue.LastAutoBookingAt != nil &&
				venue.LastAutoBookingAt.In(groupTZ).Format("2006-01-02") == todayStr

			var sent bool
			if autoBookedToday && venue.PreferredGameTime != "" {
				sent = s.sendAutoBookingGroupNotification(ctx, g.ChatID, venue, localNow, lz)
			} else {
				sent = s.sendBookingReminderToAdmins(ctx, g.ChatID, venue, lz)
			}

			if sent {
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

// sendAutoBookingGroupNotification sends a group message confirming auto-booking was done.
// Used by RunBookingReminders when auto-booking already ran today for the venue.
// Returns true if the message was sent successfully.
func (s *SchedulerService) sendAutoBookingGroupNotification(
	ctx context.Context,
	chatID int64,
	venue *models.Venue,
	localNow time.Time,
	lz *i18n.Localizer,
) bool {
	gameDate := localNow.AddDate(0, 0, venue.BookingOpensDays)
	gameDateStr := fmt.Sprintf("%d-%02d-%02d", gameDate.Year(), gameDate.Month(), gameDate.Day())

	text := lz.Tf(i18n.SchedBookingReminderAutoBooked, venue.Name, gameDateStr, venue.PreferredGameTime)
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := s.api.Send(msg); err != nil {
		s.logger.Error("booking reminder: send auto-booking group notification",
			"chat_id", chatID, "venue_id", venue.ID, "err", err)
		return false
	}
	s.logger.Info("booking reminder: auto-booking group notification sent",
		"chat_id", chatID, "venue_id", venue.ID)
	return true
}

// RunDayAfterCleanup unpins and closes yesterday's games.
// Runs at 03:00–03:05 in each group's local timezone.
func (s *SchedulerService) RunDayAfterCleanup() { s.runDayAfterCleanup(false) }

// ForceRunDayAfterCleanup bypasses the [03:00, 03:05) time window check.
// Intended for manual triggers.
func (s *SchedulerService) ForceRunDayAfterCleanup() { s.runDayAfterCleanup(true) }

func (s *SchedulerService) runDayAfterCleanup(force bool) {
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
		if !force && (localNow.Hour() != 3 || localNow.Minute() >= 5) {
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

// bookingTargetWindow returns the [start, end) UTC time range that covers the
// target game day for a booking reminder. localNow is the current time already
// converted to the group's timezone; days is venue.BookingOpensDays.
// The window spans from midnight to midnight (exclusive) of that local day.
func bookingTargetWindow(localNow time.Time, days int) (start, end time.Time) {
	target := localNow.AddDate(0, 0, days)
	start = time.Date(target.Year(), target.Month(), target.Day(), 0, 0, 0, 0, target.Location())
	end = start.AddDate(0, 0, 1)
	return
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

// determineScenario classifies the outcome of a cancellation reminder into one of four
// named scenarios used to select the notification message.
//
// count is the total registered player count.
// newCourtsCount is the courts count after any cancellations.
// canceledCourts are the court IDs that were successfully canceled (nil = none).
// determineScenario classifies the outcome of a cancellation reminder into one of four
// named scenarios used to select the notification message.
//
// count is the total registered player count.
// newCourtsCount is the courts count after any cancellations.
// canceledCourts are the court IDs that were successfully canceled (nil = none).
//
// "odd" scenarios only apply when count < newCapacity (there is an actual free spot).
// When count >= newCapacity (at or over capacity) the outcome is "all_good".
func determineScenario(count, newCourtsCount int, canceledCourts []int) string {
	didCancel := len(canceledCourts) > 0
	newCapacity := newCourtsCount * 2

	switch {
	case newCourtsCount == 0:
		return "all_canceled"
	case didCancel && count == newCapacity:
		return "canceled_balanced"
	case count < newCapacity && count%2 == 1 && didCancel:
		return "odd_canceled"
	case count < newCapacity && count%2 == 1:
		return "odd_no_cancel"
	default:
		return "all_good"
	}
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

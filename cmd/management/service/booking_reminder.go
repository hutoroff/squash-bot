package service

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/gameformat"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

// BookingReminderJob fires at 10:00–10:05 in each group's timezone on configured game days.
// If courts were auto-booked for the target date (recorded in auto_booking_results), it creates
// a game and posts the standard announcement to the group. Otherwise it DMs group admins with
// a booking reminder.
type BookingReminderJob struct {
	api                   TelegramAPI
	gameRepo              GameRepository
	groupRepo             GroupRepository
	venueRepo             VenueRepository
	autoBookingResultRepo AutoBookingResultRepository
	loc                   *time.Location
	logger                *slog.Logger
}

func NewBookingReminderJob(
	api TelegramAPI,
	gameRepo GameRepository,
	groupRepo GroupRepository,
	venueRepo VenueRepository,
	autoBookingResultRepo AutoBookingResultRepository,
	loc *time.Location,
	logger *slog.Logger,
) *BookingReminderJob {
	return &BookingReminderJob{
		api:                   api,
		gameRepo:              gameRepo,
		groupRepo:             groupRepo,
		venueRepo:             venueRepo,
		autoBookingResultRepo: autoBookingResultRepo,
		loc:                   loc,
		logger:                logger,
	}
}

func (j *BookingReminderJob) name() string   { return "booking_reminder" }
func (j *BookingReminderJob) run(force bool) { j.runBookingReminders(force) }

func (j *BookingReminderJob) runBookingReminders(force bool) {
	j.logger.Info("booking reminder check started")
	ctx := context.Background()
	now := time.Now()

	groups, err := j.groupRepo.GetAll(ctx)
	if err != nil {
		j.logger.Error("booking reminder: get groups", "err", err)
		return
	}

	notified := 0
	for _, g := range groups {
		groupTZ := resolveGroupTimezone(&g, j.loc, j.logger)
		localNow := now.In(groupTZ)

		// Only fire in the [10:00, 10:05) window in the group's local time.
		if !force && (localNow.Hour() != 10 || localNow.Minute() >= 5) {
			continue
		}

		venues, err := j.venueRepo.GetByGroupID(ctx, g.ChatID)
		if err != nil {
			j.logger.Error("booking reminder: get venues", "chat_id", g.ChatID, "err", err)
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
				j.logger.Info("booking reminder: already sent today", "venue_id", venue.ID)
				continue
			}

			targetStart, _ := bookingTargetWindow(localNow, venue.BookingOpensDays)
			gameDate := time.Date(targetStart.Year(), targetStart.Month(), targetStart.Day(), 0, 0, 0, 0, groupTZ)

			var sent bool
			if venue.AutoBookingEnabled {
				sent = j.handleAutoBookingReminder(ctx, g.ChatID, venue, gameDate, localNow, groupTZ, lz)
			} else {
				sent = j.handleManualReminder(ctx, g.ChatID, venue, targetStart, lz)
			}

			if sent {
				if err := j.venueRepo.SetLastBookingReminderAt(ctx, venue.ID); err != nil {
					j.logger.Error("booking reminder: update last sent", "venue_id", venue.ID, "err", err)
				}
				notified++
			}
		}
	}
	j.logger.Info("booking reminder done", "venues_notified", notified)
}

// handleAutoBookingReminder processes the booking reminder for a venue with auto_booking_enabled.
// For each auto_booking_result without a game: creates a game and announces it.
// If no results exist: DMs admins with a booking reminder.
// Returns true if any action was taken (game created or DM sent).
func (j *BookingReminderJob) handleAutoBookingReminder(
	ctx context.Context,
	chatID int64,
	venue *models.Venue,
	gameDate time.Time,
	localNow time.Time,
	groupTZ *time.Location,
	lz *i18n.Localizer,
) bool {
	results, err := j.autoBookingResultRepo.GetByVenueAndDate(ctx, venue.ID, gameDate)
	if err != nil {
		j.logger.Error("booking reminder: check auto-booking result", "venue_id", venue.ID, "err", err)
		// Fail-open: fall through to admin DM.
	}

	if len(results) == 0 {
		// Auto-booking didn't run or failed for all slots — DM admins.
		return j.sendBookingReminderToAdmins(chatID, venue, lz)
	}

	anyActioned := false
	for _, result := range results {
		if result.GameID != nil {
			j.logger.Info("booking reminder: game already created for slot, skipping",
				"venue_id", venue.ID, "game_time", result.GameTime, "game_id", *result.GameID)
			// Count pre-existing games as handled so the timestamp is written.
			anyActioned = true
			continue
		}
		if j.createGameAndAnnounce(ctx, chatID, venue, result, localNow, groupTZ, lz) {
			anyActioned = true
		} else {
			// Game creation failed — DM admins so the booking isn't lost.
			j.logger.Warn("booking reminder: game creation failed, falling back to admin DM",
				"venue_id", venue.ID, "game_time", result.GameTime)
			if j.sendBookingReminderToAdmins(chatID, venue, lz) {
				anyActioned = true
			}
		}
	}
	return anyActioned
}

// handleManualReminder processes the booking reminder for a venue without auto_booking_enabled.
// Skips if a game already exists on the target date; otherwise DMs admins.
func (j *BookingReminderJob) handleManualReminder(
	ctx context.Context,
	chatID int64,
	venue *models.Venue,
	targetStart time.Time,
	lz *i18n.Localizer,
) bool {
	targetEnd := targetStart.AddDate(0, 0, 1)
	existingGames, err := j.gameRepo.GetUncompletedGamesByGroupAndDay(ctx, chatID, targetStart, targetEnd)
	if err != nil {
		j.logger.Error("booking reminder: check existing games", "venue_id", venue.ID, "err", err)
		// Fail-open: proceed rather than silently suppressing.
	} else if len(existingGames) > 0 {
		j.logger.Info("booking reminder: game already created for target date, skipping",
			"venue_id", venue.ID, "target_date", targetStart.Format("2006-01-02"))
		return false
	}
	return j.sendBookingReminderToAdmins(chatID, venue, lz)
}

// createGameAndAnnounce creates a game from the stored auto-booking result and posts the
// standard game announcement to the group chat, pinned silently.
// Returns true if both the game record and the Telegram message were created successfully.
func (j *BookingReminderJob) createGameAndAnnounce(
	ctx context.Context,
	chatID int64,
	venue *models.Venue,
	result *models.AutoBookingResult,
	localNow time.Time,
	groupTZ *time.Location,
	lz *i18n.Localizer,
) bool {
	gameDate := result.GameDate

	// Use the result's game_time as the authoritative start time.
	if result.GameTime != "" {
		parts := strings.SplitN(result.GameTime, ":", 2)
		if len(parts) == 2 {
			h, errH := strconv.Atoi(parts[0])
			m, errM := strconv.Atoi(parts[1])
			if errH == nil && errM == nil {
				gameDate = time.Date(
					gameDate.Year(), gameDate.Month(), gameDate.Day(),
					h, m, 0, 0, groupTZ,
				)
			}
		}
	}

	venueID := venue.ID
	created, err := j.gameRepo.Create(ctx, &models.Game{
		ChatID:      chatID,
		GameDate:    gameDate,
		Courts:      result.Courts,
		CourtsCount: result.CourtsCount,
		VenueID:     &venueID,
	})
	if err != nil {
		j.logger.Error("booking reminder: create game", "venue_id", venue.ID, "err", err)
		return false
	}

	// Link the auto_booking_result to the game for cancellation routing.
	if err := j.autoBookingResultRepo.SetGameID(ctx, result.ID, created.ID); err != nil {
		j.logger.Error("booking reminder: set game_id on result",
			"result_id", result.ID, "game_id", created.ID, "err", err)
		// Non-fatal: game is created; cancellation falls back to GetByVenueAndDate.
	}

	// Re-fetch with hydrated venue so the formatter has venue name and address.
	game, err := j.gameRepo.GetByID(ctx, created.ID)
	if err != nil {
		j.logger.Error("booking reminder: fetch created game", "game_id", created.ID, "err", err)
		return false
	}

	msgText := gameformat.FormatGameMessage(game, nil, nil, groupTZ, localNow.UTC(), lz)
	keyboard := gameformat.GameKeyboard(game.ID, lz)

	announcement := tgbotapi.NewMessage(chatID, msgText)
	announcement.ReplyMarkup = keyboard
	sent, err := j.api.Send(announcement)
	if err != nil {
		j.logger.Error("booking reminder: send game announcement", "game_id", game.ID, "chat_id", chatID, "err", err)
		return false
	}

	pin := tgbotapi.PinChatMessageConfig{
		ChatID:              chatID,
		MessageID:           sent.MessageID,
		DisableNotification: true,
	}
	if _, err := j.api.Request(pin); err != nil {
		j.logger.Error("booking reminder: pin game message", "game_id", game.ID, "err", err)
		// Non-fatal: game is still created and announced.
	}

	if err := j.gameRepo.UpdateMessageID(ctx, game.ID, int64(sent.MessageID)); err != nil {
		j.logger.Error("booking reminder: update message_id", "game_id", game.ID, "err", err)
	}

	j.logger.Info("booking reminder: game created and announced",
		"game_id", game.ID, "chat_id", chatID, "venue_id", venue.ID,
		"game_date", gameDate.Format(time.DateOnly), "game_time", result.GameTime, "courts", result.Courts)
	return true
}

// sendBookingReminderToAdmins DMs all non-bot group admins with the booking reminder.
// Returns true if at least one message was delivered successfully.
func (j *BookingReminderJob) sendBookingReminderToAdmins(chatID int64, venue *models.Venue, lz *i18n.Localizer) bool {
	admins, err := j.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
		ChatConfig: tgbotapi.ChatConfig{ChatID: chatID},
	})
	if err != nil {
		j.logger.Error("booking reminder: get chat administrators", "chat_id", chatID, "err", err)
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
		msg.ParseMode = "Markdown"
		if _, err := j.api.Send(msg); err != nil {
			j.logger.Error("booking reminder: send DM", "user_id", admin.User.ID, "venue_id", venue.ID, "err", err)
			continue
		}
		j.logger.Info("booking reminder: DM sent", "user_id", admin.User.ID, "venue_id", venue.ID)
		sent++
	}
	return sent > 0
}

// bookingTargetWindow returns the [start, end) time range covering the target game day.
// localNow is the current time already in the group's timezone; days is venue.BookingOpensDays.
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

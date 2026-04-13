package service

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

// BookingReminderJob sends DMs to group admins when court booking opens for a venue.
// Fires at 10:00–10:05 in each group's timezone on configured game days.
type BookingReminderJob struct {
	api       TelegramAPI
	gameRepo  GameRepository
	groupRepo GroupRepository
	venueRepo VenueRepository
	loc       *time.Location
	logger    *slog.Logger
}

func NewBookingReminderJob(
	api TelegramAPI,
	gameRepo GameRepository,
	groupRepo GroupRepository,
	venueRepo VenueRepository,
	loc *time.Location,
	logger *slog.Logger,
) *BookingReminderJob {
	return &BookingReminderJob{
		api:       api,
		gameRepo:  gameRepo,
		groupRepo: groupRepo,
		venueRepo: venueRepo,
		loc:       loc,
		logger:    logger,
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

			// Skip if a game is already created for the target date.
			targetStart, targetEnd := bookingTargetWindow(localNow, venue.BookingOpensDays)
			existingGames, err := j.gameRepo.GetUncompletedGamesByGroupAndDay(ctx, g.ChatID, targetStart, targetEnd)
			if err != nil {
				j.logger.Error("booking reminder: check existing games", "venue_id", venue.ID, "err", err)
				// Fail-open: proceed with the reminder rather than silently suppressing it.
			} else if len(existingGames) > 0 {
				j.logger.Info("booking reminder: game already created for target date, skipping",
					"venue_id", venue.ID, "target_date", targetStart.Format("2006-01-02"))
				continue
			}

			autoBookedToday := venue.LastAutoBookingAt != nil &&
				venue.LastAutoBookingAt.In(groupTZ).Format("2006-01-02") == todayStr

			var sent bool
			if autoBookedToday && venue.PreferredGameTime != "" {
				sent = j.sendAutoBookingGroupNotification(g.ChatID, venue, localNow, lz)
			} else {
				sent = j.sendBookingReminderToAdmins(g.ChatID, venue, lz)
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

// sendAutoBookingGroupNotification sends a group message confirming auto-booking was done.
func (j *BookingReminderJob) sendAutoBookingGroupNotification(
	chatID int64,
	venue *models.Venue,
	localNow time.Time,
	lz *i18n.Localizer,
) bool {
	gameDate := localNow.AddDate(0, 0, venue.BookingOpensDays)
	gameDateStr := fmt.Sprintf("%d-%02d-%02d", gameDate.Year(), gameDate.Month(), gameDate.Day())

	text := lz.Tf(i18n.SchedBookingReminderAutoBooked, venue.Name, gameDateStr, venue.PreferredGameTime)
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := j.api.Send(msg); err != nil {
		j.logger.Error("booking reminder: send auto-booking group notification",
			"chat_id", chatID, "venue_id", venue.ID, "err", err)
		return false
	}
	j.logger.Info("booking reminder: auto-booking group notification sent",
		"chat_id", chatID, "venue_id", venue.ID)
	return true
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

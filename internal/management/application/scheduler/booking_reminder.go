package scheduler

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/gameformat"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"
	"github.com/hutoroff/squash-bot/internal/models"
)

// BookingReminderJob fires at 10:00–10:05 in each group's timezone on configured game days.
type BookingReminderJob struct {
	api                   outbound.TelegramAPI
	gameRepo              outbound.GameRepository
	groupRepo             outbound.GroupRepository
	venueRepo             outbound.VenueRepository
	autoBookingResultRepo outbound.AutoBookingResultRepository
	loc                   *time.Location
	logger                *slog.Logger
}

func NewBookingReminderJob(
	api outbound.TelegramAPI,
	gameRepo outbound.GameRepository,
	groupRepo outbound.GroupRepository,
	venueRepo outbound.VenueRepository,
	autoBookingResultRepo outbound.AutoBookingResultRepository,
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
	}

	if len(results) == 0 {
		return j.sendBookingReminderToAdmins(chatID, venue, lz)
	}

	anyActioned := false
	for _, result := range results {
		if result.GameID != nil {
			j.logger.Info("booking reminder: game already created for slot, skipping",
				"venue_id", venue.ID, "game_time", result.GameTime, "game_id", *result.GameID)
			anyActioned = true
			continue
		}
		if j.createGameAndAnnounce(ctx, chatID, venue, result, localNow, groupTZ, lz) {
			anyActioned = true
		} else {
			j.logger.Warn("booking reminder: game creation failed, falling back to admin DM",
				"venue_id", venue.ID, "game_time", result.GameTime)
			if j.sendBookingReminderToAdmins(chatID, venue, lz) {
				anyActioned = true
			}
		}
	}
	return anyActioned
}

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
	} else if len(existingGames) > 0 {
		j.logger.Info("booking reminder: game already created for target date, skipping",
			"venue_id", venue.ID, "target_date", targetStart.Format("2006-01-02"))
		return false
	}
	return j.sendBookingReminderToAdmins(chatID, venue, lz)
}

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

	if err := j.autoBookingResultRepo.SetGameID(ctx, result.ID, created.ID); err != nil {
		j.logger.Error("booking reminder: set game_id on result",
			"result_id", result.ID, "game_id", created.ID, "err", err)
	}

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
	}

	if err := j.gameRepo.UpdateMessageID(ctx, game.ID, int64(sent.MessageID)); err != nil {
		j.logger.Error("booking reminder: update message_id", "game_id", game.ID, "err", err)
	}

	j.logger.Info("booking reminder: game created and announced",
		"game_id", game.ID, "chat_id", chatID, "venue_id", venue.ID,
		"game_date", gameDate.Format(time.DateOnly), "game_time", result.GameTime, "courts", result.Courts)
	return true
}

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

func bookingTargetWindow(localNow time.Time, days int) (start, end time.Time) {
	target := localNow.AddDate(0, 0, days)
	start = time.Date(target.Year(), target.Month(), target.Day(), 0, 0, 0, 0, target.Location())
	end = start.AddDate(0, 0, 1)
	return
}

func containsDay(gameDays string, day int) bool {
	dayStr := strconv.Itoa(day)
	for _, part := range strings.Split(gameDays, ",") {
		if strings.TrimSpace(part) == dayStr {
			return true
		}
	}
	return false
}

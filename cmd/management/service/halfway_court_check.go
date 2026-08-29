package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/gameformat"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

// halfwayCourtCanceler is satisfied by *CancellationReminderJob. Widens
// unusedCourtCanceler with loadCourtBookingEntries, which HalfwayCourtCheckJob
// needs to confirm a game has active court bookings before cancellation.
type halfwayCourtCanceler interface {
	unusedCourtCanceler
	loadCourtBookingEntries(ctx context.Context, game *models.Game) ([]*models.CourtBooking, error)
}

// HalfwayCourtCheckJob fires at a venue-configured point between booking opening
// and the grace-period deadline. It releases half (rounded down) of the
// currently-unneeded courts; existing jobs clean up the rest later.
type HalfwayCourtCheckJob struct {
	api       TelegramAPI
	gameRepo  GameRepository
	partRepo  ParticipationRepository
	guestRepo GuestRepository
	groupRepo GroupRepository
	notifier  Notifier
	canceler  halfwayCourtCanceler
	loc       *time.Location
	logger    *slog.Logger
	now       func() time.Time
}

func NewHalfwayCourtCheckJob(
	api TelegramAPI,
	gameRepo GameRepository,
	partRepo ParticipationRepository,
	guestRepo GuestRepository,
	groupRepo GroupRepository,
	notifier Notifier,
	canceler halfwayCourtCanceler,
	loc *time.Location,
	logger *slog.Logger,
) *HalfwayCourtCheckJob {
	return &HalfwayCourtCheckJob{
		api:       api,
		gameRepo:  gameRepo,
		partRepo:  partRepo,
		guestRepo: guestRepo,
		groupRepo: groupRepo,
		notifier:  notifier,
		canceler:  canceler,
		loc:       loc,
		logger:    logger,
	}
}

func (j *HalfwayCourtCheckJob) name() string   { return "halfway_court_check" }
func (j *HalfwayCourtCheckJob) run(force bool) { j.runHalfwayCourtCheck(force) }

func (j *HalfwayCourtCheckJob) runHalfwayCourtCheck(force bool) {
	j.logger.Debug("halfway court check started")
	ctx := context.Background()
	now := time.Now()
	if j.now != nil {
		now = j.now()
	}

	games, err := j.gameRepo.GetUpcomingGamesForHalfwayCheck(ctx)
	if err != nil {
		j.logger.Error("halfway court check: query games", "err", err)
		return
	}
	j.logger.Debug("upcoming games for halfway check", "count", len(games))

	for _, game := range games {
		gracePeriodHours := 24
		if game.Venue != nil {
			gracePeriodHours = game.Venue.GracePeriodHours
		}
		deadline := game.GameDate.Add(-time.Duration(gracePeriodHours) * time.Hour)

		if now.After(deadline) {
			// Later jobs (final court check / reminder) own cleanup past the deadline.
			j.markDone(ctx, game.ID)
			continue
		}

		if game.MessageID == nil {
			// Announcement not published yet, so nobody could have joined and a
			// notification would be unthreaded; retry after publication.
			continue
		}

		if game.Venue == nil {
			// No venue linked, so no court_bookings can exist yet; retry later without marking done.
			continue
		}
		groupTZ, tzOK := groupTZByID(ctx, j.groupRepo, game.ChatID, j.loc, j.logger)
		if !tzOK {
			j.logger.Warn("halfway court check: skipping court cancellation (timezone unavailable)", "game_id", game.ID)
			continue
		}
		bookingOpensAt := bookingOpensAt(game.GameDate, game.Venue.BookingOpensDays, groupTZ)
		if !force && now.Before(preventiveCancellationAt(bookingOpensAt, deadline, game.Venue.PreventiveCancellationFraction)) {
			continue
		}

		entries, err := j.canceler.loadCourtBookingEntries(ctx, game)
		if err != nil {
			j.logger.Warn("halfway court check: load booking entries failed",
				"game_id", game.ID, "err", err)
			continue
		}
		if len(entries) == 0 {
			// Auto-booking may not have run yet; retry on the next poll without marking done.
			continue
		}

		j.processHalfwayCheck(ctx, game)
	}
}

func bookingOpensAt(gameDate time.Time, days int, loc *time.Location) time.Time {
	local := gameDate.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -days)
}

func preventiveCancellationAt(bookingOpensAt, deadline time.Time, fraction string) time.Time {
	if !models.IsPreventiveCancellationFraction(fraction) {
		fraction = models.PreventiveCancellationFractionDefault
	}
	window := deadline.Sub(bookingOpensAt)
	switch fraction {
	case "1/3":
		return bookingOpensAt.Add(window / 3)
	case "2/3":
		return bookingOpensAt.Add(window / 3 * 2)
	default:
		return bookingOpensAt.Add(window / 2)
	}
}

func (j *HalfwayCourtCheckJob) processHalfwayCheck(ctx context.Context, game *models.Game) {
	registeredCount, err := j.partRepo.GetRegisteredCount(ctx, game.ID)
	if err != nil {
		j.logger.Error("halfway court check: get registered count", "game_id", game.ID, "err", err)
		return
	}

	guestCount, err := j.guestRepo.GetCountByGame(ctx, game.ID)
	if err != nil {
		j.logger.Error("halfway court check: get guest count", "game_id", game.ID, "err", err)
		return
	}

	count := registeredCount + guestCount
	capacity := game.Capacity()
	playersPerCourt := capacity / game.CourtsCount

	unneeded := 0
	if count < capacity {
		unneeded = (capacity - count) / playersPerCourt
	}
	courtsToCancel := unneeded / 2

	if courtsToCancel == 0 {
		j.logger.Info("halfway court check: no courts to cancel",
			"game_id", game.ID, "players", count, "capacity", capacity)
		j.markDone(ctx, game.ID)
		return
	}

	lz := groupLang(ctx, j.groupRepo, game.ChatID)
	groupTZ, tzOK := groupTZByID(ctx, j.groupRepo, game.ChatID, j.loc, j.logger)
	displayLoc := j.loc
	if tzOK {
		displayLoc = groupTZ
	}

	if !tzOK {
		j.logger.Warn("halfway court check: skipping court cancellation (timezone unavailable)",
			"game_id", game.ID)
		j.markDone(ctx, game.ID)
		return
	}

	result, cancelErr := j.canceler.cancelUnusedCourts(ctx, game, courtsToCancel, groupTZ)
	if cancelErr != nil {
		gameDateTime := game.GameDate.In(displayLoc).Format("02.01 15:04")
		j.logger.Error("halfway court check: court cancellation failed",
			"game_id", game.ID, "err", cancelErr)
		notifyAdminsCancellationFailure(ctx, j.api, j.logger, "halfway court check",
			game.ChatID, gameDateTime, cancelErr, lz)
		j.markDone(ctx, game.ID)
		return
	}

	if len(result.cancelErrors) > 0 {
		gameDateTime := game.GameDate.In(displayLoc).Format("02.01 15:04")
		notifyAdminsCancellationFailure(ctx, j.api, j.logger, "halfway court check",
			game.ChatID, gameDateTime, errors.Join(result.cancelErrors...), lz)
	}

	if len(result.canceledCourts) == 0 {
		j.logger.Info("halfway court check: no courts actually canceled",
			"game_id", game.ID)
		j.markDone(ctx, game.ID)
		return
	}

	if j.notifier != nil {
		j.notifier.EditGameMessage(ctx, game.ID)
	}

	gameDateTime := game.GameDate.In(displayLoc).Format("02.01 15:04")
	newCourtsCount := result.remainingCount
	newCapacity := newCourtsCount * playersPerCourt
	canceledStr := formatCanceledCourts(result.canceledCourts)

	unit := gameformat.UnitName(game.Sport, lz)
	text := lz.Tf(i18n.SchedHalfwayCheckCanceled, unit, canceledStr, gameDateTime, count, newCapacity, newCourtsCount, unit)

	j.logger.Info("halfway court check: courts released",
		"game_id", game.ID,
		"players", count,
		"capacity", capacity,
		"canceled", len(result.canceledCourts),
		"new_courts", newCourtsCount,
	)

	msg := tgbotapi.NewMessage(game.ChatID, text)
	if game.MessageID != nil {
		msg.ReplyToMessageID = int(*game.MessageID)
	}
	if _, err := j.api.Send(msg); err != nil {
		j.logger.Error("halfway court check: send notification", "game_id", game.ID, "err", err)
	}

	j.markDone(ctx, game.ID)
}

func (j *HalfwayCourtCheckJob) markDone(ctx context.Context, gameID int64) {
	if err := j.gameRepo.MarkHalfwayCourtCheckDone(ctx, gameID); err != nil {
		j.logger.Error("halfway court check: mark done", "game_id", gameID, "err", err)
	}
}

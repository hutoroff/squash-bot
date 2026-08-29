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

const finalCheckLeadTime = 15 * time.Minute

// unusedCourtCanceler is satisfied by *CancellationReminderJob. Injected into
// FinalCourtCheckJob so it can reuse the existing cancellation logic without
// duplicating it.
type unusedCourtCanceler interface {
	cancelUnusedCourts(ctx context.Context, game *models.Game, courtsToCancel int, loc *time.Location) (*courtCancellationResult, error)
}

// FinalCourtCheckJob fires ~15 minutes before each game's grace-period deadline.
// It re-counts players vs. booked courts and releases any newly-excess courts,
// notifying the group only when it actually cancels something.
type FinalCourtCheckJob struct {
	api        TelegramAPI
	gameRepo   GameRepository
	partRepo   ParticipationRepository
	guestRepo  GuestRepository
	groupRepo  GroupRepository
	notifier   Notifier
	canceler   unusedCourtCanceler
	loc        *time.Location
	logger     *slog.Logger
	pollWindow time.Duration
}

func NewFinalCourtCheckJob(
	api TelegramAPI,
	gameRepo GameRepository,
	partRepo ParticipationRepository,
	guestRepo GuestRepository,
	groupRepo GroupRepository,
	notifier Notifier,
	canceler unusedCourtCanceler,
	loc *time.Location,
	logger *slog.Logger,
	pollWindow time.Duration,
) *FinalCourtCheckJob {
	return &FinalCourtCheckJob{
		api:        api,
		gameRepo:   gameRepo,
		partRepo:   partRepo,
		guestRepo:  guestRepo,
		groupRepo:  groupRepo,
		notifier:   notifier,
		canceler:   canceler,
		loc:        loc,
		logger:     logger,
		pollWindow: pollWindow,
	}
}

func (j *FinalCourtCheckJob) name() string   { return "final_court_check" }
func (j *FinalCourtCheckJob) run(force bool) { j.runFinalCourtCheck(force) }

func (j *FinalCourtCheckJob) runFinalCourtCheck(force bool) {
	j.logger.Debug("final court check started")
	ctx := context.Background()
	now := time.Now()

	games, err := j.gameRepo.GetUpcomingGamesForFinalCheck(ctx)
	if err != nil {
		j.logger.Error("final court check: query games", "err", err)
		return
	}
	j.logger.Debug("upcoming games for final check", "count", len(games))

	for _, game := range games {
		gracePeriodHours := 24
		if game.Venue != nil {
			gracePeriodHours = game.Venue.GracePeriodHours
		}
		finalCheckAt := game.GameDate.Add(-time.Duration(gracePeriodHours)*time.Hour - finalCheckLeadTime)
		diff := now.Sub(finalCheckAt)
		if diff < 0 {
			diff = -diff
		}
		if !force && diff > j.pollWindow {
			continue
		}
		j.processFinalCheck(ctx, game)
	}
}

func (j *FinalCourtCheckJob) processFinalCheck(ctx context.Context, game *models.Game) {
	registeredCount, err := j.partRepo.GetRegisteredCount(ctx, game.ID)
	if err != nil {
		j.logger.Error("final court check: get registered count", "game_id", game.ID, "err", err)
		return
	}

	guestCount, err := j.guestRepo.GetCountByGame(ctx, game.ID)
	if err != nil {
		j.logger.Error("final court check: get guest count", "game_id", game.ID, "err", err)
		return
	}

	count := registeredCount + guestCount
	capacity := game.Capacity()
	playersPerCourt := capacity / game.CourtsCount

	courtsToCancel := 0
	if count < capacity {
		courtsToCancel = (capacity - count) / playersPerCourt
	}

	if courtsToCancel == 0 {
		j.logger.Info("final court check: no courts to cancel",
			"game_id", game.ID, "players", count, "capacity", capacity)
		j.markDone(ctx, game.ID)
		return
	}

	// Defer group lookups until we know cancellation may actually happen.
	lz := groupLang(ctx, j.groupRepo, game.ChatID)
	groupTZ, tzOK := groupTZByID(ctx, j.groupRepo, game.ChatID, j.loc, j.logger)
	displayLoc := j.loc
	if tzOK {
		displayLoc = groupTZ
	}

	if !tzOK {
		j.logger.Warn("final court check: skipping court cancellation (timezone unavailable)",
			"game_id", game.ID)
		j.markDone(ctx, game.ID)
		return
	}

	result, cancelErr := j.canceler.cancelUnusedCourts(ctx, game, courtsToCancel, groupTZ)
	if cancelErr != nil {
		// This covers both the benign "no court_bookings records" path and the rarer
		// "courts canceled but DB write failed" path. Both warrant an admin DM so that
		// manual follow-up is possible if courts were physically released but the DB
		// row was not updated.
		gameDateTime := game.GameDate.In(displayLoc).Format("02.01 15:04")
		j.logger.Error("final court check: court cancellation failed",
			"game_id", game.ID, "err", cancelErr)
		notifyAdminsCancellationFailure(ctx, j.api, j.logger, "final court check",
			game.ChatID, gameDateTime, cancelErr, lz)
		j.markDone(ctx, game.ID)
		return
	}

	if len(result.cancelErrors) > 0 {
		gameDateTime := game.GameDate.In(displayLoc).Format("02.01 15:04")
		notifyAdminsCancellationFailure(ctx, j.api, j.logger, "final court check",
			game.ChatID, gameDateTime, errors.Join(result.cancelErrors...), lz)
	}

	if len(result.canceledCourts) == 0 {
		j.logger.Info("final court check: no courts actually canceled",
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
	freeSpots := playersPerCourt - count%playersPerCourt

	scenario := determineScenario(count, newCourtsCount, result.canceledCourts, playersPerCourt)

	var text string
	unit := gameformat.UnitName(game.Sport, lz)
	switch scenario {
	case "all_canceled":
		text = lz.Tf(i18n.SchedFinalCheckAllCanceled, unit, canceledStr, gameDateTime)
	case "odd_canceled":
		text = lz.Tf(i18n.SchedFinalCheckOddCanceled, unit, canceledStr, freeSpots, gameDateTime, count, newCapacity, newCourtsCount, unit)
	default: // canceled_balanced and any unexpected scenario
		text = lz.Tf(i18n.SchedFinalCheckCanceled, unit, canceledStr, gameDateTime, count, newCapacity, newCourtsCount, unit)
	}

	j.logger.Info("final court check: courts released",
		"game_id", game.ID,
		"players", count,
		"capacity", capacity,
		"canceled", len(result.canceledCourts),
		"new_courts", newCourtsCount,
		"scenario", scenario,
	)

	msg := tgbotapi.NewMessage(game.ChatID, text)
	if game.MessageID != nil {
		msg.ReplyToMessageID = int(*game.MessageID)
	}
	if _, err := j.api.Send(msg); err != nil {
		j.logger.Error("final court check: send notification", "game_id", game.ID, "err", err)
	}

	j.markDone(ctx, game.ID)
}

func (j *FinalCourtCheckJob) markDone(ctx context.Context, gameID int64) {
	if err := j.gameRepo.MarkFinalCourtCheckDone(ctx, gameID); err != nil {
		j.logger.Error("final court check: mark done", "game_id", gameID, "err", err)
	}
}

package service

import (
	"context"
	"log/slog"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/vkhutorov/squash_bot/internal/i18n"
	"github.com/vkhutorov/squash_bot/internal/models"
)

// CancellationReminderJob fires capacity notifications 6 hours before the cancellation
// grace period ends for each upcoming unnotified game.
type CancellationReminderJob struct {
	api           TelegramAPI
	gameRepo      GameRepository
	partRepo      ParticipationRepository
	guestRepo     GuestRepository
	groupRepo     GroupRepository
	bookingClient BookingServiceClient // optional; nil disables court cancellation
	loc           *time.Location
	logger        *slog.Logger
	pollWindow    time.Duration // timing gate: ±pollWindow around reminderAt
}

func NewCancellationReminderJob(
	api TelegramAPI,
	gameRepo GameRepository,
	partRepo ParticipationRepository,
	guestRepo GuestRepository,
	groupRepo GroupRepository,
	bookingClient BookingServiceClient,
	loc *time.Location,
	logger *slog.Logger,
	pollWindow time.Duration,
) *CancellationReminderJob {
	return &CancellationReminderJob{
		api:           api,
		gameRepo:      gameRepo,
		partRepo:      partRepo,
		guestRepo:     guestRepo,
		groupRepo:     groupRepo,
		bookingClient: bookingClient,
		loc:           loc,
		logger:        logger,
		pollWindow:    pollWindow,
	}
}

func (j *CancellationReminderJob) name() string   { return "cancellation_reminder" }
func (j *CancellationReminderJob) run(force bool) { j.runCancellationReminders(force) }

func (j *CancellationReminderJob) runCancellationReminders(force bool) {
	j.logger.Info("cancellation reminder check started")
	ctx := context.Background()
	now := time.Now()

	games, err := j.gameRepo.GetUpcomingUnnotifiedGames(ctx)
	if err != nil {
		j.logger.Error("cancellation reminder: query games", "err", err)
		return
	}
	j.logger.Info("upcoming unnotified games", "count", len(games))

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
		if !force && diff > j.pollWindow {
			continue
		}
		j.processCancellationReminder(ctx, game)
	}
}

func (j *CancellationReminderJob) processCancellationReminder(ctx context.Context, game *models.Game) {
	registeredCount, err := j.partRepo.GetRegisteredCount(ctx, game.ID)
	if err != nil {
		j.logger.Error("cancellation reminder: get registered count", "game_id", game.ID, "err", err)
		return
	}

	guestCount, err := j.guestRepo.GetCountByGame(ctx, game.ID)
	if err != nil {
		j.logger.Error("cancellation reminder: get guest count", "game_id", game.ID, "err", err)
		return
	}

	count := registeredCount + guestCount
	capacity := game.CourtsCount * 2
	lz := groupLang(ctx, j.groupRepo, game.ChatID)

	// Determine how many courts can be fully freed (each court needs 2 players).
	courtsToCancel := 0
	if count < capacity {
		courtsToCancel = (capacity - count) / 2
	}

	// Attempt automatic court cancellation when a booking client is configured.
	var result *courtCancellationResult
	groupTZ, tzOK := groupTZByID(ctx, j.groupRepo, game.ChatID, j.loc, j.logger)
	if !tzOK {
		j.logger.Warn("cancellation reminder: skipping court cancellation (timezone unavailable)",
			"game_id", game.ID)
		result = buildNoOpResult(game)
	} else {
		var cancelErr error
		result, cancelErr = j.cancelUnusedCourts(ctx, game, courtsToCancel, groupTZ)
		if cancelErr != nil {
			j.logger.Error("cancellation reminder: court cancellation failed",
				"game_id", game.ID, "err", cancelErr)
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
	case "even_no_cancel":
		text = lz.Tf(i18n.SchedReminderEvenNoCancel, gameDateTime, count, newCapacity, newCourtsCount)
	default: // all_good
		text = lz.Tf(i18n.SchedReminderAllGood, gameDateTime, count, newCapacity, newCourtsCount)
	}

	j.logger.Info("cancellation reminder",
		"game_id", game.ID,
		"players", count,
		"capacity", capacity,
		"courts_to_cancel", courtsToCancel,
		"canceled", len(result.canceledCourts),
		"new_courts", newCourtsCount,
		"scenario", scenario,
	)

	msg := tgbotapi.NewMessage(game.ChatID, text)
	if _, err := j.api.Send(msg); err != nil {
		j.logger.Error("cancellation reminder: send notification", "game_id", game.ID, "err", err)
		return
	}

	if err := j.gameRepo.MarkNotifiedDayBefore(ctx, game.ID); err != nil {
		j.logger.Error("cancellation reminder: mark notified", "game_id", game.ID, "err", err)
	}
}

// determineScenario classifies the outcome of a cancellation reminder into a named scenario.
//
// count is the total registered player count.
// newCourtsCount is the courts count after any cancellations.
// canceledCourts are the court IDs that were successfully canceled (nil = none).
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
	case count < newCapacity && count%2 == 0:
		return "even_no_cancel"
	default: // count >= newCapacity
		return "all_good"
	}
}

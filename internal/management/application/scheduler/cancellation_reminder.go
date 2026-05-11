package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/management/application/ports/inbound"
	"github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"
	"github.com/hutoroff/squash-bot/internal/models"
)

// CancellationReminderJob fires capacity notifications 6 hours before the cancellation
// grace period ends for each upcoming unnotified game.
type CancellationReminderJob struct {
	api                   outbound.TelegramAPI
	gameRepo              outbound.GameRepository
	partRepo              outbound.ParticipationRepository
	guestRepo             outbound.GuestRepository
	groupRepo             outbound.GroupRepository
	notifier              outbound.Notifier
	bookingClient         outbound.BookingServiceClient
	courtBookingRepo      outbound.CourtBookingRepository
	autoBookingResultRepo outbound.AutoBookingResultRepository
	credService           inbound.VenueCredentialUseCases
	auditSvc              inbound.AuditUseCases
	loc                   *time.Location
	logger                *slog.Logger
	pollWindow            time.Duration
}

func NewCancellationReminderJob(
	api outbound.TelegramAPI,
	gameRepo outbound.GameRepository,
	partRepo outbound.ParticipationRepository,
	guestRepo outbound.GuestRepository,
	groupRepo outbound.GroupRepository,
	notifier outbound.Notifier,
	bookingClient outbound.BookingServiceClient,
	courtBookingRepo outbound.CourtBookingRepository,
	autoBookingResultRepo outbound.AutoBookingResultRepository,
	credService inbound.VenueCredentialUseCases,
	auditSvc inbound.AuditUseCases,
	loc *time.Location,
	logger *slog.Logger,
	pollWindow time.Duration,
) *CancellationReminderJob {
	return &CancellationReminderJob{
		api:                   api,
		gameRepo:              gameRepo,
		partRepo:              partRepo,
		guestRepo:             guestRepo,
		groupRepo:             groupRepo,
		notifier:              notifier,
		bookingClient:         bookingClient,
		courtBookingRepo:      courtBookingRepo,
		autoBookingResultRepo: autoBookingResultRepo,
		credService:           credService,
		auditSvc:              auditSvc,
		loc:                   loc,
		logger:                logger,
		pollWindow:            pollWindow,
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

	courtsToCancel := 0
	if count < capacity {
		courtsToCancel = (capacity - count) / 2
	}

	var result *courtCancellationResult
	displayLoc := j.loc
	groupTZ, tzOK := groupTZByID(ctx, j.groupRepo, game.ChatID, j.loc, j.logger)
	if !tzOK {
		j.logger.Warn("cancellation reminder: skipping court cancellation (timezone unavailable)",
			"game_id", game.ID)
		result = buildNoOpResult(game)
	} else {
		displayLoc = groupTZ
		var cancelErr error
		result, cancelErr = j.cancelUnusedCourts(ctx, game, courtsToCancel, groupTZ)
		if cancelErr != nil {
			j.logger.Error("cancellation reminder: court cancellation failed",
				"game_id", game.ID, "err", cancelErr)
			result = buildNoOpResult(game)
			j.notifyCancellationError(ctx, game.ChatID, game.GameDate.In(groupTZ).Format("02.01 15:04"), cancelErr, lz)
		} else if len(result.cancelErrors) > 0 {
			j.notifyCancellationError(ctx, game.ChatID, game.GameDate.In(groupTZ).Format("02.01 15:04"), errors.Join(result.cancelErrors...), lz)
		}
	}

	if len(result.canceledCourts) > 0 && j.notifier != nil {
		j.notifier.EditGameMessage(ctx, game.ID)
	}

	gameDateTime := game.GameDate.In(displayLoc).Format("02.01 15:04")
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
	if game.MessageID != nil {
		msg.ReplyToMessageID = int(*game.MessageID)
	}
	if _, err := j.api.Send(msg); err != nil {
		j.logger.Error("cancellation reminder: send notification", "game_id", game.ID, "err", err)
		return
	}

	if err := j.gameRepo.MarkNotifiedDayBefore(ctx, game.ID); err != nil {
		j.logger.Error("cancellation reminder: mark notified", "game_id", game.ID, "err", err)
	}
}

func (j *CancellationReminderJob) notifyCancellationError(ctx context.Context, chatID int64, gameDateTime string, cancelErr error, lz *i18n.Localizer) {
	admins, err := j.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
		ChatConfig: tgbotapi.ChatConfig{ChatID: chatID},
	})
	if err != nil {
		j.logger.Error("cancellation reminder: get chat administrators for error DM", "chat_id", chatID, "err", err)
		return
	}
	text := lz.Tf(i18n.SchedCancellationFailDM, gameDateTime, cancelErr.Error())
	seen := make(map[int64]bool)
	for _, admin := range admins {
		if admin.User.IsBot || seen[admin.User.ID] {
			continue
		}
		seen[admin.User.ID] = true
		msg := tgbotapi.NewMessage(admin.User.ID, text)
		msg.DisableNotification = true
		if _, err := j.api.Send(msg); err != nil {
			j.logger.Error("cancellation reminder: send cancellation error DM",
				"user_id", admin.User.ID, "chat_id", chatID, "err", err)
		}
	}
}

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
	default:
		return "all_good"
	}
}

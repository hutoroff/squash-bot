package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/gameformat"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

// DayAfterCleanupJob unpins and closes yesterday's games.
// Runs at 03:00–03:05 in each group's local timezone.
type DayAfterCleanupJob struct {
	api       TelegramAPI
	gameRepo  GameRepository
	partRepo  ParticipationRepository
	guestRepo GuestRepository
	groupRepo GroupRepository
	loc       *time.Location
	logger    *slog.Logger
}

func NewDayAfterCleanupJob(
	api TelegramAPI,
	gameRepo GameRepository,
	partRepo ParticipationRepository,
	guestRepo GuestRepository,
	groupRepo GroupRepository,
	loc *time.Location,
	logger *slog.Logger,
) *DayAfterCleanupJob {
	return &DayAfterCleanupJob{
		api:       api,
		gameRepo:  gameRepo,
		partRepo:  partRepo,
		guestRepo: guestRepo,
		groupRepo: groupRepo,
		loc:       loc,
		logger:    logger,
	}
}

func (j *DayAfterCleanupJob) name() string   { return "day_after_cleanup" }
func (j *DayAfterCleanupJob) run(force bool) { j.runDayAfterCleanup(force) }

func (j *DayAfterCleanupJob) runDayAfterCleanup(force bool) {
	j.logger.Info("day-after cleanup check started")
	ctx := context.Background()
	now := time.Now()

	groups, err := j.groupRepo.GetAll(ctx)
	if err != nil {
		j.logger.Error("day-after cleanup: get groups", "err", err)
		return
	}

	processed := 0
	for _, g := range groups {
		groupTZ := resolveGroupTimezone(&g, j.loc, j.logger)
		localNow := now.In(groupTZ)

		// Only fire in the [03:00, 03:05) window in the group's local time.
		if !force && (localNow.Hour() != 3 || localNow.Minute() >= 5) {
			continue
		}

		yesterday := localNow.AddDate(0, 0, -1)
		from := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, groupTZ)
		to := from.AddDate(0, 0, 1)

		games, err := j.gameRepo.GetUncompletedGamesByGroupAndDay(ctx, g.ChatID, from, to)
		if err != nil {
			j.logger.Error("day-after cleanup: query games", "chat_id", g.ChatID, "err", err)
			continue
		}
		j.logger.Info("day-after cleanup: found games", "chat_id", g.ChatID, "count", len(games))

		for _, game := range games {
			j.processDayAfter(ctx, game, groupTZ)
			processed++
		}
	}
	j.logger.Info("day-after cleanup done", "games_processed", processed)
}

func (j *DayAfterCleanupJob) processDayAfter(ctx context.Context, game *models.Game, groupTZ *time.Location) {
	if game.MessageID == nil {
		j.logger.Warn("day-after cleanup: skipping game with nil message_id", "game_id", game.ID)
		return
	}
	messageID := int(*game.MessageID)

	unpin := tgbotapi.UnpinChatMessageConfig{
		ChatID:    game.ChatID,
		MessageID: messageID,
	}
	if _, err := j.api.Request(unpin); err != nil {
		j.logger.Error("day-after cleanup: unpin message", "game_id", game.ID, "message_id", messageID, "err", err)
		// Continue — still remove buttons and mark completed
	}

	participations, err := j.partRepo.GetByGame(ctx, game.ID)
	if err != nil {
		j.logger.Error("day-after cleanup: get participants", "game_id", game.ID, "err", err)
		return
	}

	guests, err := j.guestRepo.GetByGame(ctx, game.ID)
	if err != nil {
		j.logger.Error("day-after cleanup: get guests", "game_id", game.ID, "err", err)
		return
	}

	lz := groupLang(ctx, j.groupRepo, game.ChatID)
	text := formatCompletedMessage(game, participations, guests, groupTZ, lz)
	edit := tgbotapi.NewEditMessageText(game.ChatID, messageID, text)
	// Empty keyboard explicitly removes the inline buttons.
	emptyKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	edit.ReplyMarkup = &emptyKeyboard

	if _, err := j.api.Send(edit); err != nil {
		j.logger.Error("day-after cleanup: edit message", "game_id", game.ID, "err", err)
		return
	}

	if err := j.gameRepo.MarkCompleted(ctx, game.ID); err != nil {
		j.logger.Error("day-after cleanup: mark completed", "game_id", game.ID, "err", err)
		return
	}

	j.logger.Info("day-after cleanup",
		"game_id", game.ID,
		"message_id", messageID,
		"unpinned", true,
		"buttons_removed", true,
	)
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
		sb.WriteString(fmt.Sprintf("%d. %s\n", num, gameformat.PlayerDisplayName(p.Player)))
		num++
	}
	for _, g := range guests {
		sb.WriteString(fmt.Sprintf("%d. %s\n", num, lz.Tf(i18n.GameGuestLine, gameformat.PlayerDisplayName(g.InvitedBy))))
		num++
	}

	sb.WriteString("\n" + lz.T(i18n.GameCompleted))
	return sb.String()
}

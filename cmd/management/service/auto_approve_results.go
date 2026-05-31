package service

import (
	"context"
	"log/slog"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/models"
)

// AutoApproveResultsJob auto-approves pending game results after 48 h.
// Runs on every poll (no time gate) — the 48 h cutoff is enforced by the query.
type AutoApproveResultsJob struct {
	api        TelegramAPI
	resultRepo GameResultRepository
	playerRepo PlayerRepository
	auditSvc   *AuditService
	logger     *slog.Logger
}

func NewAutoApproveResultsJob(
	api TelegramAPI,
	resultRepo GameResultRepository,
	playerRepo PlayerRepository,
	auditSvc *AuditService,
	logger *slog.Logger,
) *AutoApproveResultsJob {
	return &AutoApproveResultsJob{
		api:        api,
		resultRepo: resultRepo,
		playerRepo: playerRepo,
		auditSvc:   auditSvc,
		logger:     logger,
	}
}

func (j *AutoApproveResultsJob) name() string   { return "auto_approve_results" }
func (j *AutoApproveResultsJob) run(force bool) { j.runAutoApprove() }

func (j *AutoApproveResultsJob) runAutoApprove() {
	ctx := context.Background()
	cutoff := time.Now().Add(-autoApproveWindow)

	pending, err := j.resultRepo.ListPendingOlderThan(ctx, cutoff)
	if err != nil {
		j.logger.Error("auto_approve_results: list pending", "err", err)
		return
	}
	if len(pending) == 0 {
		return
	}
	j.logger.Info("auto_approve_results: processing", "count", len(pending))

	for _, res := range pending {
		now := time.Now()
		if err := j.resultRepo.Decide(ctx, res.ID, models.GameResultAutoApproved, now); err != nil {
			j.logger.Error("auto_approve_results: decide", "result_id", res.ID, "err", err)
			continue
		}
		res.Status = models.GameResultAutoApproved
		res.DecidedAt = &now

		j.auditSvc.RecordGameResultAutoApproved(ctx, res.ID, res.GroupID)

		// Edit the opponent DM card to remove the action buttons.
		if res.ApprovalChatID != nil && res.ApprovalMessageID != nil {
			edit := tgbotapi.NewEditMessageText(*res.ApprovalChatID, *res.ApprovalMessageID, "⌛ Auto-approved (no response within 48 h)")
			if _, err := j.api.Request(edit); err != nil {
				j.logger.Warn("auto_approve_results: edit opponent card", "result_id", res.ID, "err", err)
			}
		}

		// DM both author and opponent (best-effort).
		j.dmAutoApproved(ctx, res)
	}
}

func (j *AutoApproveResultsJob) dmAutoApproved(ctx context.Context, res *models.GameResult) {
	// Look up author telegram_id for DM.
	author, err := j.playerByID(ctx, res.AuthorID)
	if err == nil && author != nil {
		msg := tgbotapi.NewMessage(author.TelegramID, "⌛ Your game result was auto-approved (opponent did not respond within 48 h).")
		if _, err := j.api.Send(msg); err != nil {
			j.logger.Warn("auto_approve_results: dm author", "result_id", res.ID, "err", err)
		}
	}

	opponent, err := j.playerByID(ctx, res.OpponentID)
	if err == nil && opponent != nil {
		msg := tgbotapi.NewMessage(opponent.TelegramID, "⌛ A game result was auto-approved because you did not respond within 48 h.")
		if _, err := j.api.Send(msg); err != nil {
			j.logger.Warn("auto_approve_results: dm opponent", "result_id", res.ID, "err", err)
		}
	}
}

func (j *AutoApproveResultsJob) playerByID(ctx context.Context, playerID int64) (*models.Player, error) {
	return j.playerRepo.GetByID(ctx, playerID)
}

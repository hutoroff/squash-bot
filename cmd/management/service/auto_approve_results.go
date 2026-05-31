package service

import (
	"context"
	"log/slog"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AutoApproveResultsJob auto-approves pending game results after 48 h.
// Runs on every poll (no time gate) — the 48 h cutoff is enforced by the query.
type AutoApproveResultsJob struct {
	api        TelegramAPI
	pool       *pgxpool.Pool // nil in unit tests; required for atomic Decide+Apply
	resultRepo GameResultRepository
	playerRepo PlayerRepository
	ratingSvc  *RatingService // optional
	auditSvc   *AuditService
	logger     *slog.Logger
}

func NewAutoApproveResultsJob(
	api TelegramAPI,
	pool *pgxpool.Pool,
	resultRepo GameResultRepository,
	playerRepo PlayerRepository,
	auditSvc *AuditService,
	logger *slog.Logger,
) *AutoApproveResultsJob {
	return &AutoApproveResultsJob{
		api:        api,
		pool:       pool,
		resultRepo: resultRepo,
		playerRepo: playerRepo,
		auditSvc:   auditSvc,
		logger:     logger,
	}
}

// SetRatingService injects the optional rating service.
func (j *AutoApproveResultsJob) SetRatingService(rs *RatingService) {
	j.ratingSvc = rs
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
		if err := j.autoApproveOne(ctx, res, now); err != nil {
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

// autoApproveOne flips status to auto_approved and applies the rating in one
// transaction so the leaderboard never lags behind the decision. Falls back to
// a plain Decide when rating or pool wiring is absent.
func (j *AutoApproveResultsJob) autoApproveOne(ctx context.Context, res *models.GameResult, now time.Time) error {
	if j.ratingSvc == nil || j.pool == nil {
		return j.resultRepo.Decide(ctx, res.ID, models.GameResultAutoApproved, now)
	}
	tx, err := j.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := j.resultRepo.DecideInTx(ctx, tx, res.ID, models.GameResultAutoApproved, now); err != nil {
		return err
	}
	if err := j.ratingSvc.ApplyInTx(ctx, tx, res); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/resultscore"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrGameResultNotFound     = errors.New("game result not found")
	ErrGameResultForbidden    = errors.New("actor is not allowed to perform this action")
	ErrGameResultBadScore     = resultscore.ErrInvalid
	ErrGameResultNotInGame    = errors.New("author or opponent is not registered in this game")
	ErrGameResultSamePlayer   = errors.New("author and opponent must be different players")
	ErrGameResultWindowClosed = errors.New("game is outside the result submission window")
	ErrOpponentOptedOut       = errors.New("opponent has opted out of game results")
	ErrAuthorOptedOut         = errors.New("author has opted out of game results")
	ErrResultsNotSupported    = errors.New("results are not supported for this game")
)

const autoApproveWindow = 48 * time.Hour

// GameResultService handles submit/approve/reject/cancel of 1-v-1 game results.
type GameResultService struct {
	pool              *pgxpool.Pool // nil in unit tests; required for atomic Decide+Apply
	resultRepo        GameResultRepository
	gameRepo          GameRepository
	playerRepo        PlayerRepository
	participationRepo ParticipationRepository
	auditSvc          *AuditService
	ratingSvc         *RatingService // nil if not configured
	userRepo          UserRepository // nil disables opt-out check (unit tests)
	resultWindowDays  int
}

func NewGameResultService(
	pool *pgxpool.Pool,
	resultRepo GameResultRepository,
	gameRepo GameRepository,
	playerRepo PlayerRepository,
	participationRepo ParticipationRepository,
	auditSvc *AuditService,
	resultWindowDays int,
	userRepo UserRepository,
) *GameResultService {
	return &GameResultService{
		pool:              pool,
		resultRepo:        resultRepo,
		gameRepo:          gameRepo,
		playerRepo:        playerRepo,
		participationRepo: participationRepo,
		auditSvc:          auditSvc,
		userRepo:          userRepo,
		resultWindowDays:  resultWindowDays,
	}
}

// SetRatingService injects the optional rating service after construction
// (avoids circular dependency at wiring time).
func (s *GameResultService) SetRatingService(rs *RatingService) {
	s.ratingSvc = rs
}

// enrich populates res.Author and res.Opponent from the player repo (best-effort; errors ignored).
func (s *GameResultService) enrich(ctx context.Context, res *models.GameResult) {
	if author, err := s.playerRepo.GetByID(ctx, res.AuthorID); err == nil {
		res.Author = author
	}
	if opp, err := s.playerRepo.GetByID(ctx, res.OpponentID); err == nil {
		res.Opponent = opp
	}
}

// Submit creates a new pending game result.
// authorUserID is the canonical user ID of the submitter; opponentPlayerID is the DB player ID.
// winnerPlayerID nil = draw. score "" = not provided.
// Returns the persisted result (with AutoApproveAt set to SubmittedAt+48h).
func (s *GameResultService) Submit(
	ctx context.Context,
	gameID int64,
	authorUserID int64,
	opponentPlayerID int64,
	winnerPlayerID *int64,
	score string,
	actorDisplay string,
	scoreKinds ...models.ScoreKind,
) (*models.GameResult, error) {
	// Omitted kind preserves compatibility with clients predating typed scores.
	var scoreKind models.ScoreKind
	if len(scoreKinds) > 1 {
		return nil, ErrGameResultBadScore
	}
	if len(scoreKinds) == 1 {
		scoreKind = scoreKinds[0]
	}
	if scoreKind != "" && !scoreKind.Valid() {
		return nil, ErrGameResultBadScore
	}
	if score == "" {
		scoreKind = ""
	}
	author, err := s.playerRepo.GetByUserID(ctx, authorUserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGameResultNotInGame
		}
		return nil, fmt.Errorf("get author: %w", err)
	}

	// Check if the author has opted out of results.
	if s.userRepo != nil {
		authorUser, err := s.userRepo.GetByID(ctx, authorUserID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get author user: %w", err)
		}
		if authorUser != nil && authorUser.ResultsOptOut {
			return nil, ErrAuthorOptedOut
		}
	}

	if author.ID == opponentPlayerID {
		return nil, ErrGameResultSamePlayer
	}

	game, err := s.gameRepo.GetByID(ctx, gameID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGameNotFound
		}
		return nil, fmt.Errorf("get game: %w", err)
	}
	if game.PlayersPerCourt != 2 {
		return nil, ErrResultsNotSupported
	}

	// Reject games outside the submission window (future, or older than the
	// configured number of days, evaluated in the group's local timezone).
	inWindow, err := s.gameRepo.GameInResultWindow(ctx, gameID, s.resultWindowDays)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGameNotFound
		}
		return nil, fmt.Errorf("check result window: %w", err)
	}
	if !inWindow {
		return nil, ErrGameResultWindowClosed
	}

	// Validate both players are registered in the game.
	parts, err := s.participationRepo.GetByGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("get participations: %w", err)
	}
	authorReg, oppReg := false, false
	for _, p := range parts {
		if p.Status != models.StatusRegistered {
			continue
		}
		if p.PlayerID == author.ID {
			authorReg = true
		}
		if p.PlayerID == opponentPlayerID {
			oppReg = true
		}
	}
	if !authorReg || !oppReg {
		return nil, ErrGameResultNotInGame
	}

	oppPlayer, err := s.playerRepo.GetByID(ctx, opponentPlayerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGameResultNotInGame
		}
		return nil, fmt.Errorf("get opponent: %w", err)
	}

	// Check if the opponent has opted out of results.
	if s.userRepo != nil {
		oppUser, err := s.userRepo.GetByID(ctx, oppPlayer.UserID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get opponent user: %w", err)
		}
		if oppUser != nil && oppUser.ResultsOptOut {
			return nil, ErrOpponentOptedOut
		}
	}

	if err := validateScore(score, author.ID, opponentPlayerID, winnerPlayerID); err != nil {
		return nil, err
	}

	now := time.Now()
	res := &models.GameResult{
		GameID:      gameID,
		GroupID:     game.ChatID,
		AuthorID:    author.ID,
		OpponentID:  opponentPlayerID,
		WinnerID:    winnerPlayerID,
		Score:       score,
		ScoreKind:   scoreKind,
		Status:      models.GameResultPending,
		SubmittedAt: now,
	}

	id, err := s.resultRepo.Create(ctx, res)
	if err != nil {
		return nil, fmt.Errorf("create game result: %w", err)
	}
	res.ID = id

	autoAt := now.Add(autoApproveWindow)
	res.AutoApproveAt = &autoAt

	res.Author = author
	res.Opponent = oppPlayer

	s.auditSvc.RecordGameResultSubmitted(ctx, id, game.ChatID, authorUserID, actorDisplay)
	return res, nil
}

// Get returns a game result by ID.
func (s *GameResultService) Get(ctx context.Context, id int64) (*models.GameResult, error) {
	res, err := s.resultRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, ErrGameResultNotFound
	}
	s.enrich(ctx, res)
	return res, nil
}

// SetApprovalMessage stores the opponent DM chat/message IDs so the auto-approve job can edit the card.
func (s *GameResultService) SetApprovalMessage(ctx context.Context, id, chatID int64, messageID int) error {
	return s.resultRepo.SetApprovalMessage(ctx, id, chatID, messageID)
}

// Approve marks the result approved. actorUserID must match the stored opponent.
func (s *GameResultService) Approve(ctx context.Context, id, actorUserID int64, actorDisplay string) (*models.GameResult, error) {
	return s.decide(ctx, id, actorUserID, actorDisplay, models.GameResultApproved, false)
}

// Reject marks the result rejected. actorUserID must match the stored opponent.
func (s *GameResultService) Reject(ctx context.Context, id, actorUserID int64, actorDisplay string) (*models.GameResult, error) {
	return s.decide(ctx, id, actorUserID, actorDisplay, models.GameResultRejected, false)
}

// CancelByAuthor allows the submitting player to cancel a pending result.
func (s *GameResultService) CancelByAuthor(ctx context.Context, id, actorUserID int64, actorDisplay string) (*models.GameResult, error) {
	return s.decide(ctx, id, actorUserID, actorDisplay, models.GameResultCanceled, true)
}

func (s *GameResultService) decide(
	ctx context.Context,
	id, actorUserID int64,
	actorDisplay string,
	newStatus models.GameResultStatus,
	authorAction bool,
) (*models.GameResult, error) {
	res, err := s.resultRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, ErrGameResultNotFound
	}

	// Resolve actor player record.
	actor, err := s.playerRepo.GetByUserID(ctx, actorUserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGameResultForbidden
		}
		return nil, fmt.Errorf("get actor: %w", err)
	}

	if authorAction {
		if actor.ID != res.AuthorID {
			return nil, ErrGameResultForbidden
		}
	} else {
		if actor.ID != res.OpponentID {
			return nil, ErrGameResultForbidden
		}
	}

	now := time.Now()
	if err := s.commitDecision(ctx, res, newStatus, now); err != nil {
		if errors.Is(err, storage.ErrGameResultNotPending) {
			return nil, storage.ErrGameResultNotPending
		}
		return nil, fmt.Errorf("decide game result: %w", err)
	}
	res.Status = newStatus
	res.DecidedAt = &now
	s.enrich(ctx, res)

	switch newStatus {
	case models.GameResultApproved:
		s.auditSvc.RecordGameResultApproved(ctx, id, res.GroupID, actorUserID, actorDisplay)
	case models.GameResultRejected:
		s.auditSvc.RecordGameResultRejected(ctx, id, res.GroupID, actorUserID, actorDisplay)
	case models.GameResultCanceled:
		s.auditSvc.RecordGameResultCanceled(ctx, id, res.GroupID, actorUserID, actorDisplay)
	}
	return res, nil
}

// commitDecision flips the result status and, for approvals, applies the rating
// update inside the same transaction so a successful HTTP response always
// implies an updated leaderboard. Falls back to a plain (non-tx) Decide when
// the rating service or pool is not wired up (unit tests, rating disabled).
func (s *GameResultService) commitDecision(ctx context.Context, res *models.GameResult, newStatus models.GameResultStatus, now time.Time) error {
	if newStatus != models.GameResultApproved || s.ratingSvc == nil || s.pool == nil {
		return s.resultRepo.Decide(ctx, res.ID, newStatus, now)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin decision tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := s.resultRepo.DecideInTx(ctx, tx, res.ID, newStatus, now); err != nil {
		return err
	}
	if err := s.ratingSvc.ApplyInTx(ctx, tx, res); err != nil {
		return fmt.Errorf("apply rating: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit decision tx: %w", err)
	}
	return nil
}

func validateScore(score string, authorID, opponentID int64, winnerID *int64) error {
	return resultscore.Validate(score, authorID, opponentID, winnerID)
}

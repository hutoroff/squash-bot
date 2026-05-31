package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/service/rating"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LeaderboardEntry is a single row in the leaderboard response.
type LeaderboardEntry struct {
	Rank        int            `json:"rank"`
	Player      *models.Player `json:"player"`
	Rating      float64        `json:"rating"`
	RD          float64        `json:"rd"`
	GamesPlayed int            `json:"games_played"`
	DeltaToday  float64        `json:"delta_today"` // 0 if no change today
}

// RatingService applies Glicko-2 updates and builds leaderboards.
type RatingService struct {
	pool       *pgxpool.Pool
	ratingRepo PlayerRatingRepository
	changeRepo RatingChangeRepository
	groupRepo  GroupRepository
	auditSvc   *AuditService
	logger     *slog.Logger
}

func NewRatingService(
	pool *pgxpool.Pool,
	ratingRepo PlayerRatingRepository,
	changeRepo RatingChangeRepository,
	groupRepo GroupRepository,
	auditSvc *AuditService,
	logger *slog.Logger,
) *RatingService {
	return &RatingService{
		pool:       pool,
		ratingRepo: ratingRepo,
		changeRepo: changeRepo,
		groupRepo:  groupRepo,
		auditSvc:   auditSvc,
		logger:     logger,
	}
}

// Apply updates Glicko-2 ratings for both players in a game result. Opens its
// own transaction; use ApplyInTx when the caller already holds one (e.g. when
// the rating update must commit atomically with a game_results status flip).
func (s *RatingService) Apply(ctx context.Context, result *models.GameResult) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := s.ApplyInTx(ctx, tx, result); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rating tx: %w", err)
	}
	return nil
}

// ApplyInTx performs the Glicko-2 update inside the caller-provided transaction.
// The caller is responsible for Begin/Commit/Rollback. Both player_ratings
// upserts and both rating_changes inserts land in the same transaction so the
// current rating and its delta-today history can never diverge.
func (s *RatingService) ApplyInTx(ctx context.Context, tx pgx.Tx, result *models.GameResult) error {
	// Load both ratings inside the transaction (SELECT FOR UPDATE).
	// Order by player_id ASC to prevent deadlocks.
	playerIDs := []int64{result.AuthorID, result.OpponentID}
	if playerIDs[0] > playerIDs[1] {
		playerIDs[0], playerIDs[1] = playerIDs[1], playerIDs[0]
	}

	ratings := make(map[int64]*models.PlayerRating, 2)
	for _, pid := range playerIDs {
		pr, err := s.getOrInitForUpdate(ctx, tx, result.GroupID, pid)
		if err != nil {
			return fmt.Errorf("get rating for player %d: %w", pid, err)
		}
		ratings[pid] = pr
	}

	authorRating := ratings[result.AuthorID]
	opponentRating := ratings[result.OpponentID]

	// Compute scores.
	var authorScore, opponentScore float64
	switch {
	case result.WinnerID == nil:
		authorScore, opponentScore = 0.5, 0.5
	case *result.WinnerID == result.AuthorID:
		authorScore, opponentScore = 1, 0
	default:
		authorScore, opponentScore = 0, 1
	}

	authorNew := rating.Apply(
		rating.Rating{R: authorRating.Rating, RD: authorRating.RD, Sigma: authorRating.Volatility},
		[]rating.MatchResult{{
			Opponent: rating.Rating{R: opponentRating.Rating, RD: opponentRating.RD, Sigma: opponentRating.Volatility},
			Score:    authorScore,
		}},
		rating.Tau,
	)
	opponentNew := rating.Apply(
		rating.Rating{R: opponentRating.Rating, RD: opponentRating.RD, Sigma: opponentRating.Volatility},
		[]rating.MatchResult{{
			Opponent: rating.Rating{R: authorRating.Rating, RD: authorRating.RD, Sigma: authorRating.Volatility},
			Score:    opponentScore,
		}},
		rating.Tau,
	)

	now := time.Now()

	authorUpdated := &models.PlayerRating{
		GroupID: result.GroupID, PlayerID: result.AuthorID,
		Rating: authorNew.R, RD: authorNew.RD, Volatility: authorNew.Sigma,
		GamesPlayed: authorRating.GamesPlayed + 1, UpdatedAt: now,
	}
	opponentUpdated := &models.PlayerRating{
		GroupID: result.GroupID, PlayerID: result.OpponentID,
		Rating: opponentNew.R, RD: opponentNew.RD, Volatility: opponentNew.Sigma,
		GamesPlayed: opponentRating.GamesPlayed + 1, UpdatedAt: now,
	}

	if err := s.upsertInTx(ctx, tx, authorUpdated); err != nil {
		return fmt.Errorf("upsert author rating: %w", err)
	}
	if err := s.upsertInTx(ctx, tx, opponentUpdated); err != nil {
		return fmt.Errorf("upsert opponent rating: %w", err)
	}

	if err := s.changeRepo.InsertInTx(ctx, tx, &models.RatingChange{
		GameResultID: result.ID, GroupID: result.GroupID, PlayerID: result.AuthorID,
		OldRating: authorRating.Rating, NewRating: authorNew.R,
		OldRD: authorRating.RD, NewRD: authorNew.RD,
		Delta: authorNew.R - authorRating.Rating, AppliedAt: now,
	}); err != nil {
		return fmt.Errorf("insert author rating change: %w", err)
	}
	if err := s.changeRepo.InsertInTx(ctx, tx, &models.RatingChange{
		GameResultID: result.ID, GroupID: result.GroupID, PlayerID: result.OpponentID,
		OldRating: opponentRating.Rating, NewRating: opponentNew.R,
		OldRD: opponentRating.RD, NewRD: opponentNew.RD,
		Delta: opponentNew.R - opponentRating.Rating, AppliedAt: now,
	}); err != nil {
		return fmt.Errorf("insert opponent rating change: %w", err)
	}

	// Audit record is best-effort and stays outside the rating commit; it logs
	// internally on failure and must not block the rating update.
	s.auditSvc.RecordRatingUpdated(ctx, result.ID, result.GroupID,
		result.AuthorID, authorNew.R-authorRating.Rating,
		result.OpponentID, opponentNew.R-opponentRating.Rating)

	return nil
}

// ListGroupsForPlayer returns the group IDs where the player has a rating with
// at least one game played. Thin pass-through used by the API layer.
func (s *RatingService) ListGroupsForPlayer(ctx context.Context, playerID int64) ([]int64, error) {
	return s.ratingRepo.ListGroupsForPlayer(ctx, playerID)
}

// GetLeaderboard returns the rated players for a group ordered by rating DESC,
// with today's delta (in the group's local timezone) included.
func (s *RatingService) GetLeaderboard(ctx context.Context, groupID int64) ([]LeaderboardEntry, error) {
	ratings, err := s.ratingRepo.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}

	// Fetch today's changes for delta display.
	group, err := s.groupRepo.GetByID(ctx, groupID)
	var todayChanges map[int64]float64
	if err == nil && group != nil {
		loc, locErr := time.LoadLocation(group.Timezone)
		if locErr != nil {
			loc = time.UTC
		}
		now := time.Now().In(loc)
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		dayEnd := dayStart.Add(24 * time.Hour)
		changes, chErr := s.changeRepo.ListByGroupAndDateRange(ctx, groupID, dayStart.UTC(), dayEnd.UTC())
		if chErr == nil {
			todayChanges = make(map[int64]float64)
			for _, c := range changes {
				todayChanges[c.PlayerID] += c.Delta
			}
		}
	}

	entries := make([]LeaderboardEntry, 0, len(ratings))
	for i, r := range ratings {
		// Hide players with 0 games (they have default rating only).
		if r.GamesPlayed == 0 {
			continue
		}
		entry := LeaderboardEntry{
			Rank:        i + 1,
			Player:      r.Player,
			Rating:      r.Rating,
			RD:          r.RD,
			GamesPlayed: r.GamesPlayed,
		}
		if todayChanges != nil {
			entry.DeltaToday = todayChanges[r.PlayerID]
		}
		entries = append(entries, entry)
	}
	// Fix rank numbering (we skipped 0-game players above).
	for i := range entries {
		entries[i].Rank = i + 1
	}
	return entries, nil
}

func (s *RatingService) getOrInitForUpdate(ctx context.Context, tx pgx.Tx, groupID, playerID int64) (*models.PlayerRating, error) {
	const selectQ = `
		SELECT group_id, player_id, rating, rd, volatility, games_played, updated_at
		FROM player_ratings
		WHERE group_id = $1 AND player_id = $2
		FOR UPDATE`
	const insertQ = `
		INSERT INTO player_ratings (group_id, player_id, rating, rd, volatility, games_played, updated_at)
		VALUES ($1, $2, 1500, 350, 0.06, 0, NOW())
		ON CONFLICT (group_id, player_id) DO NOTHING
		RETURNING group_id, player_id, rating, rd, volatility, games_played, updated_at`
	const selectNoLock = `
		SELECT group_id, player_id, rating, rd, volatility, games_played, updated_at
		FROM player_ratings
		WHERE group_id = $1 AND player_id = $2`

	scan := func(row pgx.Row) (*models.PlayerRating, error) {
		var pr models.PlayerRating
		err := row.Scan(&pr.GroupID, &pr.PlayerID, &pr.Rating, &pr.RD, &pr.Volatility, &pr.GamesPlayed, &pr.UpdatedAt)
		if err != nil {
			return nil, err
		}
		return &pr, nil
	}

	pr, err := scan(tx.QueryRow(ctx, selectQ, groupID, playerID))
	if err == nil {
		return pr, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	// Not found — insert defaults.
	pr, err = scan(tx.QueryRow(ctx, insertQ, groupID, playerID))
	if err == nil {
		return pr, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	// Concurrent insert — re-read without lock (we're already in tx).
	return scan(tx.QueryRow(ctx, selectNoLock, groupID, playerID))
}

func (s *RatingService) upsertInTx(ctx context.Context, tx pgx.Tx, pr *models.PlayerRating) error {
	const q = `
		INSERT INTO player_ratings (group_id, player_id, rating, rd, volatility, games_played, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (group_id, player_id) DO UPDATE
		  SET rating = EXCLUDED.rating, rd = EXCLUDED.rd, volatility = EXCLUDED.volatility,
		      games_played = EXCLUDED.games_played, updated_at = EXCLUDED.updated_at`
	_, err := tx.Exec(ctx, q,
		pr.GroupID, pr.PlayerID, pr.Rating, pr.RD, pr.Volatility, pr.GamesPlayed, pr.UpdatedAt,
	)
	return err
}

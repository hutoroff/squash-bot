package storage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PlayerRatingRepo struct {
	pool *pgxpool.Pool
}

func NewPlayerRatingRepo(pool *pgxpool.Pool) *PlayerRatingRepo {
	return &PlayerRatingRepo{pool: pool}
}

// GetOrInit returns the existing player rating or inserts default values if not present.
func (r *PlayerRatingRepo) GetOrInit(ctx context.Context, groupID, playerID int64) (*models.PlayerRating, error) {
	const q = `
		SELECT group_id, player_id, rating, rd, volatility, games_played, updated_at
		FROM player_ratings WHERE group_id = $1 AND player_id = $2`
	row := r.pool.QueryRow(ctx, q, groupID, playerID)
	pr, err := scanPlayerRating(row)
	if err == nil {
		return pr, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get player rating: %w", err)
	}
	// Insert defaults.
	const ins = `
		INSERT INTO player_ratings (group_id, player_id, rating, rd, volatility, games_played, updated_at)
		VALUES ($1, $2, 1500, 350, 0.06, 0, NOW())
		ON CONFLICT (group_id, player_id) DO NOTHING
		RETURNING group_id, player_id, rating, rd, volatility, games_played, updated_at`
	row = r.pool.QueryRow(ctx, ins, groupID, playerID)
	pr, err = scanPlayerRating(row)
	if err == nil {
		return pr, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("init player rating: %w", err)
	}
	// Another writer inserted concurrently; retry read.
	row = r.pool.QueryRow(ctx, q, groupID, playerID)
	return scanPlayerRating(row)
}

// Upsert inserts or updates a player rating row.
func (r *PlayerRatingRepo) Upsert(ctx context.Context, pr *models.PlayerRating) error {
	const q = `
		INSERT INTO player_ratings (group_id, player_id, rating, rd, volatility, games_played, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (group_id, player_id) DO UPDATE
		  SET rating = EXCLUDED.rating,
		      rd = EXCLUDED.rd,
		      volatility = EXCLUDED.volatility,
		      games_played = EXCLUDED.games_played,
		      updated_at = EXCLUDED.updated_at`
	_, err := r.pool.Exec(ctx, q,
		pr.GroupID, pr.PlayerID, pr.Rating, pr.RD, pr.Volatility, pr.GamesPlayed, pr.UpdatedAt,
	)
	return err
}

// ListByGroup returns all rated players for a group, ordered by rating DESC.
// Players who have opted out of results are excluded.
// Player records are JOINed in.
func (r *PlayerRatingRepo) ListByGroup(ctx context.Context, groupID int64) ([]*models.PlayerRating, error) {
	const q = `
		SELECT pr.group_id, pr.player_id, pr.rating, pr.rd, pr.volatility, pr.games_played, pr.updated_at,
		       p.id, p.user_id, ti.external_id, ti.username, ti.first_name, ti.last_name, p.created_at
		FROM player_ratings pr
		JOIN players p ON p.id = pr.player_id
		JOIN users u ON u.id = p.user_id
		LEFT JOIN user_identities ti ON ti.user_id = p.user_id AND ti.provider = 'telegram'
		WHERE pr.group_id = $1
		  AND u.results_opt_out = FALSE
		ORDER BY pr.rating DESC`
	rows, err := r.pool.Query(ctx, q, groupID)
	if err != nil {
		return nil, fmt.Errorf("list player ratings: %w", err)
	}
	defer rows.Close()
	var result []*models.PlayerRating
	for rows.Next() {
		var pr models.PlayerRating
		var p models.Player
		var externalID *string
		if err := rows.Scan(
			&pr.GroupID, &pr.PlayerID, &pr.Rating, &pr.RD, &pr.Volatility, &pr.GamesPlayed, &pr.UpdatedAt,
			&p.ID, &p.UserID, &externalID, &p.Username, &p.FirstName, &p.LastName, &p.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan player rating: %w", err)
		}
		if externalID != nil {
			if tgID, err := strconv.ParseInt(*externalID, 10, 64); err == nil {
				p.TelegramID = tgID
			}
		}
		pr.Player = &p
		result = append(result, &pr)
	}
	return result, rows.Err()
}

func scanPlayerRating(s scanner) (*models.PlayerRating, error) {
	var pr models.PlayerRating
	err := s.Scan(&pr.GroupID, &pr.PlayerID, &pr.Rating, &pr.RD, &pr.Volatility, &pr.GamesPlayed, &pr.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &pr, nil
}

// ── RatingChangeRepo ──────────────────────────────────────────────────────────

type RatingChangeRepo struct {
	pool *pgxpool.Pool
}

func NewRatingChangeRepo(pool *pgxpool.Pool) *RatingChangeRepo {
	return &RatingChangeRepo{pool: pool}
}

const insertRatingChange = `INSERT INTO rating_changes
 (game_result_id, group_id, player_id, old_rating, new_rating, old_rd, new_rd, delta, applied_at,
 policy_version, evidence_weight, score_kind, policy_reason, score_aware_enabled)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`

func ratingChangeArgs(c models.RatingChange) []any {
	if c.PolicyVersion == "" {
		c.PolicyVersion = "glicko2-v1"
	}
	if c.EvidenceWeight == 0 {
		c.EvidenceWeight = 1
	}
	if c.PolicyReason == "" {
		c.PolicyReason = "legacy"
	}
	return []any{c.GameResultID, c.GroupID, c.PlayerID, c.OldRating, c.NewRating, c.OldRD, c.NewRD, c.Delta, c.AppliedAt,
		c.PolicyVersion, c.EvidenceWeight, c.ScoreKind, c.PolicyReason, c.ScoreAwareEnabled}
}
func (r *RatingChangeRepo) Insert(ctx context.Context, change *models.RatingChange) error {
	_, err := r.pool.Exec(ctx, insertRatingChange, ratingChangeArgs(*change)...)
	return err
}

func (r *RatingChangeRepo) InsertInTx(ctx context.Context, tx pgx.Tx, change *models.RatingChange) error {
	_, err := tx.Exec(ctx, insertRatingChange, ratingChangeArgs(*change)...)
	return err
}

func (r *RatingChangeRepo) ListByGroupAndDateRange(ctx context.Context, groupID int64, from, to time.Time) ([]*models.RatingChange, error) {
	const q = `
		SELECT id, game_result_id, group_id, player_id, old_rating, new_rating, old_rd, new_rd, delta, applied_at,
 policy_version, evidence_weight, score_kind, policy_reason, score_aware_enabled
		FROM rating_changes
		WHERE group_id = $1 AND applied_at >= $2 AND applied_at < $3
		ORDER BY applied_at DESC`
	rows, err := r.pool.Query(ctx, q, groupID, from, to)
	if err != nil {
		return nil, fmt.Errorf("list rating changes: %w", err)
	}
	defer rows.Close()
	var result []*models.RatingChange
	for rows.Next() {
		var rc models.RatingChange
		if err := rows.Scan(
			&rc.ID, &rc.GameResultID, &rc.GroupID, &rc.PlayerID,
			&rc.OldRating, &rc.NewRating, &rc.OldRD, &rc.NewRD, &rc.Delta, &rc.AppliedAt,
			&rc.PolicyVersion, &rc.EvidenceWeight, &rc.ScoreKind, &rc.PolicyReason, &rc.ScoreAwareEnabled,
		); err != nil {
			return nil, fmt.Errorf("scan rating change: %w", err)
		}
		result = append(result, &rc)
	}
	return result, rows.Err()
}

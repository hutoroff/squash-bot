package storage

import (
	"context"
	"errors"
	"fmt"
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

// ListGroupsForPlayer returns group IDs where the player has a rating row with
// at least one game played. Used by the leaderboard group picker.
func (r *PlayerRatingRepo) ListGroupsForPlayer(ctx context.Context, playerID int64) ([]int64, error) {
	const q = `
		SELECT group_id
		FROM player_ratings
		WHERE player_id = $1 AND games_played > 0
		ORDER BY group_id`
	rows, err := r.pool.Query(ctx, q, playerID)
	if err != nil {
		return nil, fmt.Errorf("list groups for player: %w", err)
	}
	defer rows.Close()
	var groupIDs []int64
	for rows.Next() {
		var gid int64
		if err := rows.Scan(&gid); err != nil {
			return nil, fmt.Errorf("scan group_id: %w", err)
		}
		groupIDs = append(groupIDs, gid)
	}
	return groupIDs, rows.Err()
}

// ListByGroup returns all rated players for a group, ordered by rating DESC.
// Player records are JOINed in.
func (r *PlayerRatingRepo) ListByGroup(ctx context.Context, groupID int64) ([]*models.PlayerRating, error) {
	const q = `
		SELECT pr.group_id, pr.player_id, pr.rating, pr.rd, pr.volatility, pr.games_played, pr.updated_at,
		       p.id, p.telegram_id, p.username, p.first_name, p.last_name, p.created_at
		FROM player_ratings pr
		JOIN players p ON p.id = pr.player_id
		WHERE pr.group_id = $1
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
		if err := rows.Scan(
			&pr.GroupID, &pr.PlayerID, &pr.Rating, &pr.RD, &pr.Volatility, &pr.GamesPlayed, &pr.UpdatedAt,
			&p.ID, &p.TelegramID, &p.Username, &p.FirstName, &p.LastName, &p.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan player rating: %w", err)
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

func (r *RatingChangeRepo) Insert(ctx context.Context, change *models.RatingChange) error {
	const q = `
		INSERT INTO rating_changes
			(game_result_id, group_id, player_id, old_rating, new_rating, old_rd, new_rd, delta, applied_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := r.pool.Exec(ctx, q,
		change.GameResultID, change.GroupID, change.PlayerID,
		change.OldRating, change.NewRating, change.OldRD, change.NewRD, change.Delta, change.AppliedAt,
	)
	return err
}

func (r *RatingChangeRepo) InsertInTx(ctx context.Context, tx pgx.Tx, change *models.RatingChange) error {
	const q = `
		INSERT INTO rating_changes
			(game_result_id, group_id, player_id, old_rating, new_rating, old_rd, new_rd, delta, applied_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := tx.Exec(ctx, q,
		change.GameResultID, change.GroupID, change.PlayerID,
		change.OldRating, change.NewRating, change.OldRD, change.NewRD, change.Delta, change.AppliedAt,
	)
	return err
}

func (r *RatingChangeRepo) ListByGroupAndDateRange(ctx context.Context, groupID int64, from, to time.Time) ([]*models.RatingChange, error) {
	const q = `
		SELECT id, game_result_id, group_id, player_id, old_rating, new_rating, old_rd, new_rd, delta, applied_at
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
		); err != nil {
			return nil, fmt.Errorf("scan rating change: %w", err)
		}
		result = append(result, &rc)
	}
	return result, rows.Err()
}

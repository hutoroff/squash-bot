package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PlayerRepo struct {
	pool *pgxpool.Pool
}

func NewPlayerRepo(pool *pgxpool.Pool) *PlayerRepo {
	return &PlayerRepo{pool: pool}
}

const playerSelect = `
	SELECT p.id, p.user_id, ti.external_id, ti.username, ti.first_name, ti.last_name, p.created_at
	FROM players p
	LEFT JOIN user_identities ti ON ti.user_id = p.user_id AND ti.provider = 'telegram'`

// Upsert lazily creates the players row for userID on first join; a second
// join is a no-op. The profile fields live on user_identities now — resolve
// owns them.
func (r *PlayerRepo) Upsert(ctx context.Context, userID int64) (*models.Player, error) {
	const q = `
		INSERT INTO players (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO NOTHING`

	slog.Debug("PlayerRepo.Upsert", "user_id", userID)

	if _, err := r.pool.Exec(ctx, q, userID); err != nil {
		return nil, fmt.Errorf("upsert player: %w", err)
	}
	return r.GetByUserID(ctx, userID)
}

func (r *PlayerRepo) GetByUserID(ctx context.Context, userID int64) (*models.Player, error) {
	const q = playerSelect + ` WHERE p.user_id = $1`

	slog.Debug("PlayerRepo.GetByUserID", "user_id", userID)

	row := r.pool.QueryRow(ctx, q, userID)
	return scanPlayer(row)
}

func (r *PlayerRepo) GetByID(ctx context.Context, id int64) (*models.Player, error) {
	const q = playerSelect + ` WHERE p.id = $1`

	slog.Debug("PlayerRepo.GetByID", "id", id)

	row := r.pool.QueryRow(ctx, q, id)
	return scanPlayer(row)
}

// PlayerIDByUserID returns the player ID for the given user, or ok=false if
// the user has never joined a game (no players row yet).
func (r *PlayerRepo) PlayerIDByUserID(ctx context.Context, userID int64) (id int64, ok bool, err error) {
	const q = `SELECT id FROM players WHERE user_id = $1`
	err = r.pool.QueryRow(ctx, q, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("player id by user id: %w", err)
	}
	return id, true, nil
}

func scanPlayer(s scanner) (*models.Player, error) {
	var p models.Player
	var externalID *string
	err := s.Scan(&p.ID, &p.UserID, &externalID, &p.Username, &p.FirstName, &p.LastName, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("scan player: %w", err)
	}
	if externalID != nil {
		if tgID, err := strconv.ParseInt(*externalID, 10, 64); err == nil {
			p.TelegramID = tgID
		}
	}
	return &p, nil
}

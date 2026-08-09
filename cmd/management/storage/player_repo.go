package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

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

func (r *PlayerRepo) Upsert(ctx context.Context, player *models.Player) (*models.Player, error) {
	const q = `
		INSERT INTO players (telegram_id, username, first_name, last_name)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (telegram_id) DO UPDATE
		    SET username   = EXCLUDED.username,
		        first_name = EXCLUDED.first_name,
		        last_name  = EXCLUDED.last_name
		RETURNING id, telegram_id, username, first_name, last_name, created_at`

	slog.Debug("PlayerRepo.Upsert", "telegram_id", player.TelegramID)

	row := r.pool.QueryRow(ctx, q,
		player.TelegramID, player.Username, player.FirstName, player.LastName,
	)
	return scanPlayer(row)
}

func (r *PlayerRepo) GetByTelegramID(ctx context.Context, telegramID int64) (*models.Player, error) {
	const q = `
		SELECT id, telegram_id, username, first_name, last_name, created_at
		FROM players WHERE telegram_id = $1`

	slog.Debug("PlayerRepo.GetByTelegramID", "telegram_id", telegramID)

	row := r.pool.QueryRow(ctx, q, telegramID)
	return scanPlayer(row)
}

func (r *PlayerRepo) GetByID(ctx context.Context, id int64) (*models.Player, error) {
	const q = `
		SELECT id, telegram_id, username, first_name, last_name, created_at
		FROM players WHERE id = $1`

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
	err := s.Scan(&p.ID, &p.TelegramID, &p.Username, &p.FirstName, &p.LastName, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("scan player: %w", err)
	}
	return &p, nil
}

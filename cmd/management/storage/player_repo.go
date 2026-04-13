package storage

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hutoroff/squash-bot/internal/models"
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

func scanPlayer(s scanner) (*models.Player, error) {
	var p models.Player
	err := s.Scan(&p.ID, &p.TelegramID, &p.Username, &p.FirstName, &p.LastName, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("scan player: %w", err)
	}
	return &p, nil
}

package storage

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vkhutorov/squash_bot/internal/models"
)

type ParticipationRepo struct {
	pool *pgxpool.Pool
}

func NewParticipationRepo(pool *pgxpool.Pool) *ParticipationRepo {
	return &ParticipationRepo{pool: pool}
}

func (r *ParticipationRepo) Upsert(ctx context.Context, gameID, playerID int64, status models.ParticipationStatus) error {
	const q = `
		INSERT INTO game_participations (game_id, player_id, status)
		VALUES ($1, $2, $3)
		ON CONFLICT (game_id, player_id) DO UPDATE
		    SET status = EXCLUDED.status`

	slog.Debug("ParticipationRepo.Upsert", "game_id", gameID, "player_id", playerID, "status", status)

	_, err := r.pool.Exec(ctx, q, gameID, playerID, status)
	return err
}

func (r *ParticipationRepo) GetByGame(ctx context.Context, gameID int64) ([]*models.GameParticipation, error) {
	const q = `
		SELECT gp.id, gp.game_id, gp.player_id, gp.status, gp.created_at,
		       p.id, p.telegram_id, p.username, p.first_name, p.last_name, p.created_at
		FROM game_participations gp
		JOIN players p ON p.id = gp.player_id
		WHERE gp.game_id = $1
		ORDER BY gp.created_at`

	slog.Debug("ParticipationRepo.GetByGame", "game_id", gameID)

	rows, err := r.pool.Query(ctx, q, gameID)
	if err != nil {
		return nil, fmt.Errorf("query participations: %w", err)
	}
	defer rows.Close()

	var participations []*models.GameParticipation
	for rows.Next() {
		var gp models.GameParticipation
		var p models.Player
		err := rows.Scan(
			&gp.ID, &gp.GameID, &gp.PlayerID, &gp.Status, &gp.CreatedAt,
			&p.ID, &p.TelegramID, &p.Username, &p.FirstName, &p.LastName, &p.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan participation: %w", err)
		}
		gp.Player = &p
		participations = append(participations, &gp)
	}
	return participations, rows.Err()
}

func (r *ParticipationRepo) GetRegisteredCount(ctx context.Context, gameID int64) (int, error) {
	const q = `
		SELECT COUNT(*) FROM game_participations
		WHERE game_id = $1 AND status = 'registered'`

	slog.Debug("ParticipationRepo.GetRegisteredCount", "game_id", gameID)

	var count int
	err := r.pool.QueryRow(ctx, q, gameID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count registered: %w", err)
	}
	return count, nil
}

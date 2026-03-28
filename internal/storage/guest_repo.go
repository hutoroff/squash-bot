package storage

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vkhutorov/squash_bot/internal/models"
)

type GuestRepo struct {
	pool *pgxpool.Pool
}

func NewGuestRepo(pool *pgxpool.Pool) *GuestRepo {
	return &GuestRepo{pool: pool}
}

// AddGuest inserts a new guest invited by the given player for the given game.
func (r *GuestRepo) AddGuest(ctx context.Context, gameID, invitedByPlayerID int64) error {
	const q = `INSERT INTO guest_participations (game_id, invited_by_player_id) VALUES ($1, $2)`
	slog.Debug("GuestRepo.AddGuest", "game_id", gameID, "invited_by_player_id", invitedByPlayerID)
	_, err := r.pool.Exec(ctx, q, gameID, invitedByPlayerID)
	return err
}

// RemoveLatestGuest deletes the most recently added guest that was invited by
// the given player for the given game. Returns true if a row was deleted.
// Uses (created_at DESC, id DESC) as a stable tiebreaker so concurrent inserts
// with the same timestamp are handled deterministically.
func (r *GuestRepo) RemoveLatestGuest(ctx context.Context, gameID, invitedByPlayerID int64) (bool, error) {
	const q = `
		DELETE FROM guest_participations
		WHERE id = (
			SELECT id FROM guest_participations
			WHERE game_id = $1 AND invited_by_player_id = $2
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		)`
	slog.Debug("GuestRepo.RemoveLatestGuest", "game_id", gameID, "invited_by_player_id", invitedByPlayerID)
	result, err := r.pool.Exec(ctx, q, gameID, invitedByPlayerID)
	if err != nil {
		return false, err
	}
	return result.RowsAffected() > 0, nil
}

// GetByGame returns all guest participations for a game in insertion order.
// Uses (created_at, id) so the list is stable even when timestamps collide.
// Each entry has InvitedBy populated from a JOIN with the players table.
func (r *GuestRepo) GetByGame(ctx context.Context, gameID int64) ([]*models.GuestParticipation, error) {
	const q = `
		SELECT gp.id, gp.game_id, gp.invited_by_player_id, gp.created_at,
		       p.id, p.telegram_id, p.username, p.first_name, p.last_name, p.created_at
		FROM guest_participations gp
		JOIN players p ON p.id = gp.invited_by_player_id
		WHERE gp.game_id = $1
		ORDER BY gp.created_at, gp.id`
	slog.Debug("GuestRepo.GetByGame", "game_id", gameID)

	rows, err := r.pool.Query(ctx, q, gameID)
	if err != nil {
		return nil, fmt.Errorf("query guests: %w", err)
	}
	defer rows.Close()

	var guests []*models.GuestParticipation
	for rows.Next() {
		var g models.GuestParticipation
		var p models.Player
		if err := rows.Scan(
			&g.ID, &g.GameID, &g.InvitedByPlayerID, &g.CreatedAt,
			&p.ID, &p.TelegramID, &p.Username, &p.FirstName, &p.LastName, &p.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan guest: %w", err)
		}
		g.InvitedBy = &p
		guests = append(guests, &g)
	}
	return guests, rows.Err()
}

// GetCountByGame returns the total number of guests for a game.
func (r *GuestRepo) GetCountByGame(ctx context.Context, gameID int64) (int, error) {
	const q = `SELECT COUNT(*) FROM guest_participations WHERE game_id = $1`
	slog.Debug("GuestRepo.GetCountByGame", "game_id", gameID)
	var count int
	if err := r.pool.QueryRow(ctx, q, gameID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count guests: %w", err)
	}
	return count, nil
}

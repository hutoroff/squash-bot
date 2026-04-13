package storage

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type GuestRepo struct {
	pool *pgxpool.Pool
}

func NewGuestRepo(pool *pgxpool.Pool) *GuestRepo {
	return &GuestRepo{pool: pool}
}

// AddGuest inserts a new guest if the game still has capacity.
// Capacity enforcement is atomic: an advisory lock on the game ID serialises
// concurrent "+1" callbacks so that the read-check-write cannot race.
// Returns (true, nil) on success and (false, nil) when the game is already full.
func (r *GuestRepo) AddGuest(ctx context.Context, gameID, invitedByPlayerID int64) (bool, error) {
	slog.Debug("GuestRepo.AddGuest", "game_id", gameID, "invited_by_player_id", invitedByPlayerID)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Serialise all concurrent AddGuest calls for the same game. The lock is
	// automatically released when the transaction commits or rolls back.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, gameID); err != nil {
		return false, fmt.Errorf("advisory lock: %w", err)
	}

	// Read the game's capacity and current occupancy in one query.
	const countQ = `
		SELECT g.courts_count * 2,
		       (SELECT COUNT(*) FROM game_participations WHERE game_id = $1 AND status = 'registered') +
		       (SELECT COUNT(*) FROM guest_participations  WHERE game_id = $1)
		FROM games g
		WHERE g.id = $1`

	var capacity, occupancy int
	if err := tx.QueryRow(ctx, countQ, gameID).Scan(&capacity, &occupancy); err != nil {
		return false, fmt.Errorf("read capacity: %w", err)
	}

	if occupancy >= capacity {
		return false, nil // tx.Rollback releases the advisory lock
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO guest_participations (game_id, invited_by_player_id) VALUES ($1, $2)`,
		gameID, invitedByPlayerID,
	); err != nil {
		return false, fmt.Errorf("insert guest: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
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

	guests := make([]*models.GuestParticipation, 0)
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

// DeleteByID removes a specific guest participation by primary key, scoped to the
// given game. The game_id constraint prevents stale or tampered callback data from
// deleting a guest belonging to a different game.
// Returns true if a row was deleted.
func (r *GuestRepo) DeleteByID(ctx context.Context, gameID, guestID int64) (bool, error) {
	const q = `DELETE FROM guest_participations WHERE id = $1 AND game_id = $2`
	slog.Debug("GuestRepo.DeleteByID", "guest_id", guestID, "game_id", gameID)
	result, err := r.pool.Exec(ctx, q, guestID, gameID)
	if err != nil {
		return false, err
	}
	return result.RowsAffected() > 0, nil
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

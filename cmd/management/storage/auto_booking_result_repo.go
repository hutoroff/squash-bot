package storage

import (
	"context"
	"errors"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AutoBookingResultRepo implements service.AutoBookingResultRepository.
type AutoBookingResultRepo struct {
	pool *pgxpool.Pool
}

func NewAutoBookingResultRepo(pool *pgxpool.Pool) *AutoBookingResultRepo {
	return &AutoBookingResultRepo{pool: pool}
}

// Save inserts a new auto-booking result for a specific time slot.
// Duplicate entries (same venue_id + game_date + game_time) are silently ignored.
func (r *AutoBookingResultRepo) Save(ctx context.Context, venueID int64, gameDate time.Time, gameTime, courts string, courtsCount int) error {
	const q = `
		INSERT INTO auto_booking_results (venue_id, game_date, game_time, courts, courts_count)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (venue_id, game_date, game_time) DO NOTHING`
	_, err := r.pool.Exec(ctx, q, venueID, gameDate, gameTime, courts, courtsCount)
	return err
}

// GetByVenueAndDate returns all auto-booking results for a venue on a specific game date.
// Returns an empty slice (not nil) when no rows exist.
func (r *AutoBookingResultRepo) GetByVenueAndDate(ctx context.Context, venueID int64, gameDate time.Time) ([]*models.AutoBookingResult, error) {
	const q = `
		SELECT id, venue_id, game_date, game_time, courts, courts_count, game_id, created_at
		FROM auto_booking_results
		WHERE venue_id = $1 AND game_date = $2
		ORDER BY game_time ASC`
	rows, err := r.pool.Query(ctx, q, venueID, gameDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []*models.AutoBookingResult
	for rows.Next() {
		var res models.AutoBookingResult
		if err := rows.Scan(&res.ID, &res.VenueID, &res.GameDate, &res.GameTime,
			&res.Courts, &res.CourtsCount, &res.GameID, &res.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, &res)
	}
	if results == nil {
		results = []*models.AutoBookingResult{}
	}
	return results, rows.Err()
}

// GetByVenueAndDateAndTime returns the result for an exact (venue, date, time) combination,
// or (nil, nil) when no row exists. Used by AutoBookingJob for per-slot dedup.
func (r *AutoBookingResultRepo) GetByVenueAndDateAndTime(ctx context.Context, venueID int64, gameDate time.Time, gameTime string) (*models.AutoBookingResult, error) {
	const q = `
		SELECT id, venue_id, game_date, game_time, courts, courts_count, game_id, created_at
		FROM auto_booking_results
		WHERE venue_id = $1 AND game_date = $2 AND game_time = $3`
	row := r.pool.QueryRow(ctx, q, venueID, gameDate, gameTime)
	var res models.AutoBookingResult
	err := row.Scan(&res.ID, &res.VenueID, &res.GameDate, &res.GameTime,
		&res.Courts, &res.CourtsCount, &res.GameID, &res.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// GetByGameID returns the auto-booking result that was used to create a specific game,
// or (nil, nil) when no row exists. Used by CancellationReminderJob to route cancellation.
func (r *AutoBookingResultRepo) GetByGameID(ctx context.Context, gameID int64) (*models.AutoBookingResult, error) {
	const q = `
		SELECT id, venue_id, game_date, game_time, courts, courts_count, game_id, created_at
		FROM auto_booking_results
		WHERE game_id = $1`
	row := r.pool.QueryRow(ctx, q, gameID)
	var res models.AutoBookingResult
	err := row.Scan(&res.ID, &res.VenueID, &res.GameDate, &res.GameTime,
		&res.Courts, &res.CourtsCount, &res.GameID, &res.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// SetGameID links an auto-booking result to the Telegram game created by BookingReminderJob.
func (r *AutoBookingResultRepo) SetGameID(ctx context.Context, resultID, gameID int64) error {
	const q = `UPDATE auto_booking_results SET game_id = $1 WHERE id = $2`
	_, err := r.pool.Exec(ctx, q, gameID, resultID)
	return err
}

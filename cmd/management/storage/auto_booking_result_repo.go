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

// Save inserts a new auto-booking result. Duplicate entries (same venue_id + game_date)
// are silently ignored — the first successful write wins.
func (r *AutoBookingResultRepo) Save(ctx context.Context, venueID int64, gameDate time.Time, courts string, courtsCount int) error {
	const q = `
		INSERT INTO auto_booking_results (venue_id, game_date, courts, courts_count)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (venue_id, game_date) DO NOTHING`
	_, err := r.pool.Exec(ctx, q, venueID, gameDate, courts, courtsCount)
	return err
}

// GetByVenueAndDate returns the stored auto-booking result for a venue on a specific game date,
// or (nil, nil) when no row exists.
func (r *AutoBookingResultRepo) GetByVenueAndDate(ctx context.Context, venueID int64, gameDate time.Time) (*models.AutoBookingResult, error) {
	const q = `
		SELECT id, venue_id, game_date, courts, courts_count, created_at
		FROM auto_booking_results
		WHERE venue_id = $1 AND game_date = $2`
	row := r.pool.QueryRow(ctx, q, venueID, gameDate)
	var res models.AutoBookingResult
	err := row.Scan(&res.ID, &res.VenueID, &res.GameDate, &res.Courts, &res.CourtsCount, &res.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &res, nil
}

package storage

import (
	"context"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CourtBookingRepo struct {
	pool *pgxpool.Pool
}

func NewCourtBookingRepo(pool *pgxpool.Pool) *CourtBookingRepo {
	return &CourtBookingRepo{pool: pool}
}

func (r *CourtBookingRepo) Save(ctx context.Context, b *models.CourtBooking) error {
	const q = `
		INSERT INTO court_bookings (venue_id, game_date, court_uuid, court_label, match_id, booking_uuid, credential_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (match_id) DO NOTHING`
	_, err := r.pool.Exec(ctx, q,
		b.VenueID, b.GameDate, b.CourtUUID, b.CourtLabel, b.MatchID, b.BookingUUID, b.CredentialID)
	return err
}

// GetByVenueAndDate returns active (non-canceled) bookings for the venue and date.
func (r *CourtBookingRepo) GetByVenueAndDate(ctx context.Context, venueID int64, gameDate time.Time) ([]*models.CourtBooking, error) {
	const q = `
		SELECT id, venue_id, game_date, court_uuid, court_label, match_id, booking_uuid,
		       credential_id, canceled_at, created_at
		FROM court_bookings
		WHERE venue_id = $1 AND game_date = $2 AND canceled_at IS NULL
		ORDER BY id ASC`
	rows, err := r.pool.Query(ctx, q, venueID, gameDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*models.CourtBooking
	for rows.Next() {
		var b models.CourtBooking
		if err := rows.Scan(
			&b.ID, &b.VenueID, &b.GameDate, &b.CourtUUID, &b.CourtLabel,
			&b.MatchID, &b.BookingUUID, &b.CredentialID, &b.CanceledAt, &b.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, &b)
	}
	return result, rows.Err()
}

// MarkCanceled soft-deletes a court booking by setting canceled_at to the current time.
func (r *CourtBookingRepo) MarkCanceled(ctx context.Context, matchID string) error {
	const q = `UPDATE court_bookings SET canceled_at = NOW() WHERE match_id = $1 AND canceled_at IS NULL`
	_, err := r.pool.Exec(ctx, q, matchID)
	return err
}

// HasActiveByCredentialID returns true if any active (non-canceled) booking uses the credential.
func (r *CourtBookingRepo) HasActiveByCredentialID(ctx context.Context, credentialID int64) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM court_bookings WHERE credential_id = $1 AND canceled_at IS NULL)`
	var exists bool
	err := r.pool.QueryRow(ctx, q, credentialID).Scan(&exists)
	return exists, err
}

// HasActiveByVenueID returns true if any active (non-canceled) booking exists for the venue.
func (r *CourtBookingRepo) HasActiveByVenueID(ctx context.Context, venueID int64) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM court_bookings WHERE venue_id = $1 AND canceled_at IS NULL)`
	var exists bool
	err := r.pool.QueryRow(ctx, q, venueID).Scan(&exists)
	return exists, err
}

// MarkCanceledByVenueAndDate soft-deletes all active bookings for the venue on the given date.
func (r *CourtBookingRepo) MarkCanceledByVenueAndDate(ctx context.Context, venueID int64, gameDate time.Time) error {
	const q = `UPDATE court_bookings SET canceled_at = NOW() WHERE venue_id = $1 AND game_date = $2 AND canceled_at IS NULL`
	_, err := r.pool.Exec(ctx, q, venueID, gameDate)
	return err
}

package outbound

import (
	"context"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

type CourtBookingRepository interface {
	// Save inserts a new court booking record. Silently ignores duplicates by match_id.
	Save(ctx context.Context, booking *models.CourtBooking) error
	// GetByVenueAndDate returns active (non-canceled) bookings for the venue and date.
	GetByVenueAndDate(ctx context.Context, venueID int64, gameDate time.Time) ([]*models.CourtBooking, error)
	// GetByVenueAndDateAndTime returns active bookings filtered by time slot.
	// Falls back to game_time='' rows (legacy) when no time-specific rows exist.
	GetByVenueAndDateAndTime(ctx context.Context, venueID int64, gameDate time.Time, gameTime string) ([]*models.CourtBooking, error)
	// MarkCanceled soft-deletes the booking by setting canceled_at to NOW().
	MarkCanceled(ctx context.Context, matchID string) error
	// HasActiveByCredentialID returns true if any non-canceled booking uses the credential.
	HasActiveByCredentialID(ctx context.Context, credentialID int64) (bool, error)
	// HasActiveByVenueID returns true if any non-canceled booking exists for the venue.
	HasActiveByVenueID(ctx context.Context, venueID int64) (bool, error)
	// MarkCanceledByVenueAndDate soft-deletes all active bookings for the venue on the given date.
	MarkCanceledByVenueAndDate(ctx context.Context, venueID int64, gameDate time.Time) error
}

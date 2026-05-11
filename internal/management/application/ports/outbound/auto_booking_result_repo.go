package outbound

import (
	"context"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

type AutoBookingResultRepository interface {
	// Save persists the courts booked by AutoBookingJob. Duplicate (venue_id, game_date, game_time) rows are ignored.
	Save(ctx context.Context, venueID int64, gameDate time.Time, gameTime, courts string, courtsCount int) error
	// GetByVenueAndDate returns all results for the venue and date, ordered by game_time ASC.
	GetByVenueAndDate(ctx context.Context, venueID int64, gameDate time.Time) ([]*models.AutoBookingResult, error)
	// GetByVenueAndDateAndTime returns the result for an exact (venue, date, time) triple, or nil if none.
	GetByVenueAndDateAndTime(ctx context.Context, venueID int64, gameDate time.Time, gameTime string) (*models.AutoBookingResult, error)
	// GetByGameID returns the result linked to the given game, or nil if none.
	GetByGameID(ctx context.Context, gameID int64) (*models.AutoBookingResult, error)
	// SetGameID links an auto-booking result to the Telegram game created by BookingReminderJob.
	SetGameID(ctx context.Context, resultID, gameID int64) error
}

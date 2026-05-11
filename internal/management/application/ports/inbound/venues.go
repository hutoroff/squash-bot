package inbound

import (
	"context"

	"github.com/hutoroff/squash-bot/internal/models"
)

type VenueUseCases interface {
	CreateVenue(ctx context.Context, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int, preferredGameTimes, autoBookingCourts string, autoBookingEnabled bool, autoBookingGamesCount int) (*models.Venue, error)
	GetVenuesByGroup(ctx context.Context, groupID int64) ([]*models.Venue, error)
	GetVenueByID(ctx context.Context, id int64) (*models.Venue, error)
	UpdateVenue(ctx context.Context, id, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int, preferredGameTimes, autoBookingCourts string, autoBookingEnabled bool, autoBookingGamesCount int) (*models.Venue, error)
	DeleteVenue(ctx context.Context, id, groupID int64) error
}

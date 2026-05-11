package outbound

import (
	"context"

	"github.com/hutoroff/squash-bot/internal/models"
)

type VenueRepository interface {
	Create(ctx context.Context, venue *models.Venue) (*models.Venue, error)
	GetByID(ctx context.Context, id int64) (*models.Venue, error)
	GetByIDAndGroupID(ctx context.Context, id, groupID int64) (*models.Venue, error)
	GetByGroupID(ctx context.Context, groupID int64) ([]*models.Venue, error)
	Update(ctx context.Context, venue *models.Venue) (*models.Venue, error)
	Delete(ctx context.Context, id, groupID int64) error
	SetLastBookingReminderAt(ctx context.Context, venueID int64) error
	SetLastAutoBookingAt(ctx context.Context, venueID int64) error
}

package inbound

import (
	"context"
	"time"

	"github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"
	"github.com/hutoroff/squash-bot/internal/models"
)

type VenueCredentialUseCases interface {
	Add(ctx context.Context, venueID, groupID int64, login, password string, priority, maxCourts int) (*models.VenueCredential, error)
	List(ctx context.Context, venueID, groupID int64) ([]*models.VenueCredential, error)
	Remove(ctx context.Context, credentialID, venueID, groupID int64) error
	PrioritiesInUse(ctx context.Context, venueID, groupID int64) ([]int, error)
	// Scheduler-facing methods
	GetDecryptedByID(ctx context.Context, credID int64) (*outbound.DecryptedCredential, error)
	ListForBooking(ctx context.Context, venueID int64, cooldown time.Duration) ([]outbound.DecryptedCredential, error)
	MarkError(ctx context.Context, credID int64) error
}

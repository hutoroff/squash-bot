package outbound

import (
	"context"

	"github.com/hutoroff/squash-bot/internal/models"
)

// DecryptedCredential is a credential with its plaintext password available for
// use by the auto-booking scheduler. It is never persisted or returned via any API.
type DecryptedCredential struct {
	ID        int64
	VenueID   int64
	Login     string
	Password  string
	Priority  int
	MaxCourts int
}

type VenueCredentialRepository interface {
	// Create inserts a new credential. enc_password must already be encrypted.
	Create(ctx context.Context, venueID int64, login, encPassword string, priority, maxCourts int) (*models.VenueCredential, error)
	// ListByVenueID returns all credentials without EncryptedPassword (API-safe).
	ListByVenueID(ctx context.Context, venueID int64) ([]*models.VenueCredential, error)
	// ListWithPasswordByVenueID returns all credentials including EncryptedPassword (scheduler only).
	ListWithPasswordByVenueID(ctx context.Context, venueID int64) ([]*models.VenueCredential, error)
	// GetWithPasswordByID returns a single credential including EncryptedPassword.
	GetWithPasswordByID(ctx context.Context, id int64) (*models.VenueCredential, error)
	// Delete removes a credential scoped to venueID.
	Delete(ctx context.Context, id, venueID int64) error
	// ExistsByLogin reports whether a credential with the given login already exists for the venue.
	ExistsByLogin(ctx context.Context, venueID int64, login string) (bool, error)
	// PrioritiesInUse returns all priority values currently in use for the venue.
	PrioritiesInUse(ctx context.Context, venueID int64) ([]int, error)
	// SetLastErrorAt records the current timestamp as the last error time for a credential.
	SetLastErrorAt(ctx context.Context, id int64) error
}

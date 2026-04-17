package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// ErrDuplicateCredentialLogin is returned when a credential with the same login
// already exists for the venue.
var ErrDuplicateCredentialLogin = errors.New("a credential with this login already exists for this venue")

// ErrCredentialInUse is returned when trying to delete a credential that still
// has active (non-canceled) court bookings linked to it.
var ErrCredentialInUse = errors.New("credential has active court bookings and cannot be removed")

// ErrCredentialNotFound is returned when the credential does not exist or is not
// accessible to the requesting group (venue ownership check failed).
var ErrCredentialNotFound = errors.New("credential not found")

// DecryptedCredential is a credential with its plaintext password available for
// use by the auto-booking job. It is never persisted or returned via any API.
type DecryptedCredential struct {
	ID        int64
	VenueID   int64
	Login     string
	Password  string
	Priority  int
	MaxCourts int
}

// VenueCredentialService manages encrypted booking credentials per venue.
type VenueCredentialService struct {
	repo             VenueCredentialRepository
	venueRepo        VenueRepository
	courtBookingRepo CourtBookingRepository
	enc              *Encryptor
}

func NewVenueCredentialService(repo VenueCredentialRepository, venueRepo VenueRepository, courtBookingRepo CourtBookingRepository, enc *Encryptor) *VenueCredentialService {
	return &VenueCredentialService{repo: repo, venueRepo: venueRepo, courtBookingRepo: courtBookingRepo, enc: enc}
}

// Add encrypts the password and stores a new credential for the venue.
// Returns ErrDuplicateCredentialLogin when the login is already in use for the venue.
// Verifies that the caller's group owns the venue.
func (s *VenueCredentialService) Add(ctx context.Context, venueID, groupID int64, login, password string, priority, maxCourts int) (*models.VenueCredential, error) {
	if _, err := s.venueRepo.GetByIDAndGroupID(ctx, venueID, groupID); err != nil {
		return nil, fmt.Errorf("venue not found or not owned by group: %w", err)
	}
	exists, err := s.repo.ExistsByLogin(ctx, venueID, login)
	if err != nil {
		return nil, fmt.Errorf("check login: %w", err)
	}
	if exists {
		return nil, ErrDuplicateCredentialLogin
	}
	encPassword, err := s.enc.Encrypt(password)
	if err != nil {
		return nil, fmt.Errorf("encrypt password: %w", err)
	}
	cred, err := s.repo.Create(ctx, venueID, login, encPassword, priority, maxCourts)
	if err != nil {
		return nil, fmt.Errorf("store credential: %w", err)
	}
	return cred, nil
}

// List returns all credentials for the venue ordered by priority ASC.
// Verifies group ownership.
func (s *VenueCredentialService) List(ctx context.Context, venueID, groupID int64) ([]*models.VenueCredential, error) {
	if _, err := s.venueRepo.GetByIDAndGroupID(ctx, venueID, groupID); err != nil {
		return nil, fmt.Errorf("venue not found or not owned by group: %w", err)
	}
	return s.repo.ListByVenueID(ctx, venueID)
}

// Remove deletes a credential. Verifies group ownership and that no active court
// bookings are linked to the credential.
func (s *VenueCredentialService) Remove(ctx context.Context, credentialID, venueID, groupID int64) error {
	if _, err := s.venueRepo.GetByIDAndGroupID(ctx, venueID, groupID); err != nil {
		return ErrCredentialNotFound
	}
	if s.courtBookingRepo != nil {
		hasActive, err := s.courtBookingRepo.HasActiveByCredentialID(ctx, credentialID)
		if err != nil {
			return fmt.Errorf("check active bookings: %w", err)
		}
		if hasActive {
			return ErrCredentialInUse
		}
	}
	if err := s.repo.Delete(ctx, credentialID, venueID); err != nil {
		return ErrCredentialNotFound
	}
	return nil
}

// GetDecryptedByID returns the decrypted credential for a given ID.
// Used by CancellationReminderJob to obtain credentials for per-court cancellation.
func (s *VenueCredentialService) GetDecryptedByID(ctx context.Context, credID int64) (*DecryptedCredential, error) {
	c, err := s.repo.GetWithPasswordByID(ctx, credID)
	if err != nil {
		return nil, fmt.Errorf("get credential %d: %w", credID, err)
	}
	if c == nil {
		return nil, fmt.Errorf("credential %d not found", credID)
	}
	password, err := s.enc.Decrypt(c.EncryptedPassword)
	if err != nil {
		return nil, fmt.Errorf("decrypt credential %d: %w", credID, err)
	}
	return &DecryptedCredential{
		ID:        c.ID,
		VenueID:   c.VenueID,
		Login:     c.Login,
		Password:  password,
		Priority:  c.Priority,
		MaxCourts: c.MaxCourts,
	}, nil
}

// PrioritiesInUse returns the priority values currently in use for the venue.
// Verifies group ownership.
func (s *VenueCredentialService) PrioritiesInUse(ctx context.Context, venueID, groupID int64) ([]int, error) {
	if _, err := s.venueRepo.GetByIDAndGroupID(ctx, venueID, groupID); err != nil {
		return nil, fmt.Errorf("venue not found or not owned by group: %w", err)
	}
	return s.repo.PrioritiesInUse(ctx, venueID)
}

// ListForBooking returns credentials that are usable for booking, in priority order.
// Credentials whose last_error_at is within the cooldown window are excluded.
// If the result is empty, the caller should skip booking entirely (no usable credentials).
func (s *VenueCredentialService) ListForBooking(ctx context.Context, venueID int64, cooldown time.Duration) ([]DecryptedCredential, error) {
	all, err := s.repo.ListWithPasswordByVenueID(ctx, venueID)
	if err != nil {
		return nil, fmt.Errorf("list credentials for booking: %w", err)
	}

	cutoff := time.Now().Add(-cooldown)
	var result []DecryptedCredential
	for _, c := range all {
		if c.LastErrorAt != nil && c.LastErrorAt.After(cutoff) {
			continue
		}
		password, err := s.enc.Decrypt(c.EncryptedPassword)
		if err != nil {
			slog.Warn("ListForBooking: decrypt failed, skipping credential", "id", c.ID, "err", err)
			continue
		}
		result = append(result, DecryptedCredential{
			ID:        c.ID,
			VenueID:   c.VenueID,
			Login:     c.Login,
			Password:  password,
			Priority:  c.Priority,
			MaxCourts: c.MaxCourts,
		})
	}
	return result, nil
}

// MarkError sets last_error_at to now for the given credential.
func (s *VenueCredentialService) MarkError(ctx context.Context, credID int64) error {
	if err := s.repo.SetLastErrorAt(ctx, credID); err != nil {
		return fmt.Errorf("mark credential error: %w", err)
	}
	return nil
}

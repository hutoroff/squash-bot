package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/hutoroff/squash-bot/internal/models"
)

// ErrDuplicateCredentialLogin is returned when a credential with the same login
// already exists for the venue.
var ErrDuplicateCredentialLogin = errors.New("a credential with this login already exists for this venue")

// VenueCredentialService manages encrypted booking credentials per venue.
type VenueCredentialService struct {
	repo      VenueCredentialRepository
	venueRepo VenueRepository
	enc       *Encryptor
}

func NewVenueCredentialService(repo VenueCredentialRepository, venueRepo VenueRepository, enc *Encryptor) *VenueCredentialService {
	return &VenueCredentialService{repo: repo, venueRepo: venueRepo, enc: enc}
}

// Add encrypts the password and stores a new credential for the venue.
// Returns ErrDuplicateCredentialLogin when the login is already in use for the venue.
// Verifies that the caller's group owns the venue.
func (s *VenueCredentialService) Add(ctx context.Context, venueID, groupID int64, login, password string, priority int) (*models.VenueCredential, error) {
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
	cred, err := s.repo.Create(ctx, venueID, login, encPassword, priority)
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

// Remove deletes a credential. Verifies group ownership.
func (s *VenueCredentialService) Remove(ctx context.Context, credentialID, venueID, groupID int64) error {
	if _, err := s.venueRepo.GetByIDAndGroupID(ctx, venueID, groupID); err != nil {
		return fmt.Errorf("venue not found or not owned by group: %w", err)
	}
	if err := s.repo.Delete(ctx, credentialID, venueID); err != nil {
		return fmt.Errorf("delete credential: %w", err)
	}
	return nil
}

// PrioritiesInUse returns the priority values currently in use for the venue.
// Verifies group ownership.
func (s *VenueCredentialService) PrioritiesInUse(ctx context.Context, venueID, groupID int64) ([]int, error) {
	if _, err := s.venueRepo.GetByIDAndGroupID(ctx, venueID, groupID); err != nil {
		return nil, fmt.Errorf("venue not found or not owned by group: %w", err)
	}
	return s.repo.PrioritiesInUse(ctx, venueID)
}

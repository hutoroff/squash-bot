package venue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"
	"github.com/hutoroff/squash-bot/internal/models"
)

var ErrDuplicateCredentialLogin = errors.New("a credential with this login already exists for this venue")
var ErrCredentialInUse = errors.New("credential has active court bookings and cannot be removed")
var ErrCredentialNotFound = errors.New("credential not found")

type CredentialService struct {
	repo             outbound.VenueCredentialRepository
	venueRepo        outbound.VenueRepository
	courtBookingRepo outbound.CourtBookingRepository
	enc              outbound.CredentialEncryptor
}

func NewCredentialService(repo outbound.VenueCredentialRepository, venueRepo outbound.VenueRepository, courtBookingRepo outbound.CourtBookingRepository, enc outbound.CredentialEncryptor) *CredentialService {
	return &CredentialService{repo: repo, venueRepo: venueRepo, courtBookingRepo: courtBookingRepo, enc: enc}
}

func (s *CredentialService) Add(ctx context.Context, venueID, groupID int64, login, password string, priority, maxCourts int) (*models.VenueCredential, error) {
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

func (s *CredentialService) List(ctx context.Context, venueID, groupID int64) ([]*models.VenueCredential, error) {
	if _, err := s.venueRepo.GetByIDAndGroupID(ctx, venueID, groupID); err != nil {
		return nil, fmt.Errorf("venue not found or not owned by group: %w", err)
	}
	return s.repo.ListByVenueID(ctx, venueID)
}

func (s *CredentialService) Remove(ctx context.Context, credentialID, venueID, groupID int64) error {
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

func (s *CredentialService) GetDecryptedByID(ctx context.Context, credID int64) (*outbound.DecryptedCredential, error) {
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
	return &outbound.DecryptedCredential{
		ID:        c.ID,
		VenueID:   c.VenueID,
		Login:     c.Login,
		Password:  password,
		Priority:  c.Priority,
		MaxCourts: c.MaxCourts,
	}, nil
}

func (s *CredentialService) PrioritiesInUse(ctx context.Context, venueID, groupID int64) ([]int, error) {
	if _, err := s.venueRepo.GetByIDAndGroupID(ctx, venueID, groupID); err != nil {
		return nil, fmt.Errorf("venue not found or not owned by group: %w", err)
	}
	return s.repo.PrioritiesInUse(ctx, venueID)
}

func (s *CredentialService) ListForBooking(ctx context.Context, venueID int64, cooldown time.Duration) ([]outbound.DecryptedCredential, error) {
	all, err := s.repo.ListWithPasswordByVenueID(ctx, venueID)
	if err != nil {
		return nil, fmt.Errorf("list credentials for booking: %w", err)
	}

	cutoff := time.Now().Add(-cooldown)
	var result []outbound.DecryptedCredential
	for _, c := range all {
		if c.LastErrorAt != nil && c.LastErrorAt.After(cutoff) {
			continue
		}
		password, err := s.enc.Decrypt(c.EncryptedPassword)
		if err != nil {
			slog.Warn("ListForBooking: decrypt failed, skipping credential", "id", c.ID, "err", err)
			continue
		}
		result = append(result, outbound.DecryptedCredential{
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

func (s *CredentialService) MarkError(ctx context.Context, credID int64) error {
	if err := s.repo.SetLastErrorAt(ctx, credID); err != nil {
		return fmt.Errorf("mark credential error: %w", err)
	}
	return nil
}

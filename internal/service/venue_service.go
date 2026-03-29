package service

import (
	"context"
	"fmt"

	"github.com/vkhutorov/squash_bot/internal/models"
	"github.com/vkhutorov/squash_bot/internal/storage"
)

type VenueService struct {
	repo *storage.VenueRepo
}

func NewVenueService(repo *storage.VenueRepo) *VenueService {
	return &VenueService{repo: repo}
}

func (s *VenueService) CreateVenue(ctx context.Context, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int) (*models.Venue, error) {
	venue := &models.Venue{
		GroupID:          groupID,
		Name:             name,
		Courts:           courts,
		TimeSlots:        timeSlots,
		Address:          address,
		GracePeriodHours: gracePeriodHours,
		GameDays:         gameDays,
		BookingOpensDays: bookingOpensDays,
	}
	created, err := s.repo.Create(ctx, venue)
	if err != nil {
		return nil, fmt.Errorf("create venue: %w", err)
	}
	return created, nil
}

func (s *VenueService) GetVenuesByGroup(ctx context.Context, groupID int64) ([]*models.Venue, error) {
	return s.repo.GetByGroupID(ctx, groupID)
}

func (s *VenueService) GetVenueByID(ctx context.Context, id int64) (*models.Venue, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *VenueService) UpdateVenue(ctx context.Context, id, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int) (*models.Venue, error) {
	venue := &models.Venue{
		ID:               id,
		GroupID:          groupID,
		Name:             name,
		Courts:           courts,
		TimeSlots:        timeSlots,
		Address:          address,
		GracePeriodHours: gracePeriodHours,
		GameDays:         gameDays,
		BookingOpensDays: bookingOpensDays,
	}
	updated, err := s.repo.Update(ctx, venue)
	if err != nil {
		return nil, fmt.Errorf("update venue: %w", err)
	}
	return updated, nil
}

func (s *VenueService) DeleteVenue(ctx context.Context, id, groupID int64) error {
	if err := s.repo.Delete(ctx, id, groupID); err != nil {
		return fmt.Errorf("delete venue: %w", err)
	}
	return nil
}

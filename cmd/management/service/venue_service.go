package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hutoroff/squash-bot/internal/models"
)

type VenueService struct {
	repo VenueRepository
}

func NewVenueService(repo VenueRepository) *VenueService {
	return &VenueService{repo: repo}
}

// validatePreferredGameTime returns an error if preferredGameTime is set but
// does not appear in the comma-separated timeSlots list.
func validatePreferredGameTime(preferredGameTime, timeSlots string) error {
	if preferredGameTime == "" {
		return nil
	}
	for _, slot := range strings.Split(timeSlots, ",") {
		if strings.TrimSpace(slot) == preferredGameTime {
			return nil
		}
	}
	return fmt.Errorf("preferred_game_time %q is not present in time_slots %q", preferredGameTime, timeSlots)
}

// validateAutoBookingCourts returns an error if autoBookingCourts contains
// non-integer values or duplicates. Values are Eversports facility court IDs
// and are not constrained to the venue's courts label list.
func validateAutoBookingCourts(autoBookingCourts string) error {
	if autoBookingCourts == "" {
		return nil
	}
	seen := make(map[string]bool)
	for _, c := range strings.Split(autoBookingCourts, ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, err := strconv.Atoi(c); err != nil {
			return fmt.Errorf("auto_booking_courts contains non-integer value %q", c)
		}
		if seen[c] {
			return fmt.Errorf("auto_booking_courts contains duplicate court %q", c)
		}
		seen[c] = true
	}
	return nil
}

func (s *VenueService) CreateVenue(ctx context.Context, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int, preferredGameTime, autoBookingCourts string, autoBookingEnabled bool) (*models.Venue, error) {
	if err := validatePreferredGameTime(preferredGameTime, timeSlots); err != nil {
		return nil, err
	}
	if err := validateAutoBookingCourts(autoBookingCourts); err != nil {
		return nil, err
	}
	venue := &models.Venue{
		GroupID:            groupID,
		Name:               name,
		Courts:             courts,
		TimeSlots:          timeSlots,
		Address:            address,
		GracePeriodHours:   gracePeriodHours,
		GameDays:           gameDays,
		BookingOpensDays:   bookingOpensDays,
		PreferredGameTime:  preferredGameTime,
		AutoBookingCourts:  autoBookingCourts,
		AutoBookingEnabled: autoBookingEnabled,
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

func (s *VenueService) UpdateVenue(ctx context.Context, id, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int, preferredGameTime, autoBookingCourts string, autoBookingEnabled bool) (*models.Venue, error) {
	if err := validatePreferredGameTime(preferredGameTime, timeSlots); err != nil {
		return nil, err
	}
	if err := validateAutoBookingCourts(autoBookingCourts); err != nil {
		return nil, err
	}
	venue := &models.Venue{
		ID:                 id,
		GroupID:            groupID,
		Name:               name,
		Courts:             courts,
		TimeSlots:          timeSlots,
		Address:            address,
		GracePeriodHours:   gracePeriodHours,
		GameDays:           gameDays,
		BookingOpensDays:   bookingOpensDays,
		PreferredGameTime:  preferredGameTime,
		AutoBookingCourts:  autoBookingCourts,
		AutoBookingEnabled: autoBookingEnabled,
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

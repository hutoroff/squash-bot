package venue

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"
	"github.com/hutoroff/squash-bot/internal/models"
)

var ErrVenueHasActiveBookings = errors.New("venue has active court bookings and cannot be deleted")

type Service struct {
	repo             outbound.VenueRepository
	courtBookingRepo outbound.CourtBookingRepository
}

func NewService(repo outbound.VenueRepository, courtBookingRepo outbound.CourtBookingRepository) *Service {
	return &Service{repo: repo, courtBookingRepo: courtBookingRepo}
}

func validatePreferredGameTimes(preferredGameTimes, timeSlots string) error {
	if preferredGameTimes == "" {
		return nil
	}
	slotSet := make(map[string]bool)
	for _, slot := range strings.Split(timeSlots, ",") {
		slotSet[strings.TrimSpace(slot)] = true
	}
	for _, t := range strings.Split(preferredGameTimes, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if !slotSet[t] {
			return fmt.Errorf("preferred_game_times entry %q is not present in time_slots %q", t, timeSlots)
		}
	}
	return nil
}

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

func (s *Service) CreateVenue(ctx context.Context, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int, preferredGameTimes, autoBookingCourts string, autoBookingEnabled bool, autoBookingGamesCount int) (*models.Venue, error) {
	if err := validatePreferredGameTimes(preferredGameTimes, timeSlots); err != nil {
		return nil, err
	}
	if err := validateAutoBookingCourts(autoBookingCourts); err != nil {
		return nil, err
	}
	v := &models.Venue{
		GroupID:               groupID,
		Name:                  name,
		Courts:                courts,
		TimeSlots:             timeSlots,
		Address:               address,
		GracePeriodHours:      gracePeriodHours,
		GameDays:              gameDays,
		BookingOpensDays:      bookingOpensDays,
		PreferredGameTimes:    preferredGameTimes,
		AutoBookingCourts:     autoBookingCourts,
		AutoBookingEnabled:    autoBookingEnabled,
		AutoBookingGamesCount: autoBookingGamesCount,
	}
	created, err := s.repo.Create(ctx, v)
	if err != nil {
		return nil, fmt.Errorf("create venue: %w", err)
	}
	return created, nil
}

func (s *Service) GetVenuesByGroup(ctx context.Context, groupID int64) ([]*models.Venue, error) {
	return s.repo.GetByGroupID(ctx, groupID)
}

func (s *Service) GetVenueByID(ctx context.Context, id int64) (*models.Venue, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) UpdateVenue(ctx context.Context, id, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int, preferredGameTimes, autoBookingCourts string, autoBookingEnabled bool, autoBookingGamesCount int) (*models.Venue, error) {
	if err := validatePreferredGameTimes(preferredGameTimes, timeSlots); err != nil {
		return nil, err
	}
	if err := validateAutoBookingCourts(autoBookingCourts); err != nil {
		return nil, err
	}
	v := &models.Venue{
		ID:                    id,
		GroupID:               groupID,
		Name:                  name,
		Courts:                courts,
		TimeSlots:             timeSlots,
		Address:               address,
		GracePeriodHours:      gracePeriodHours,
		GameDays:              gameDays,
		BookingOpensDays:      bookingOpensDays,
		PreferredGameTimes:    preferredGameTimes,
		AutoBookingCourts:     autoBookingCourts,
		AutoBookingEnabled:    autoBookingEnabled,
		AutoBookingGamesCount: autoBookingGamesCount,
	}
	updated, err := s.repo.Update(ctx, v)
	if err != nil {
		return nil, fmt.Errorf("update venue: %w", err)
	}
	return updated, nil
}

func (s *Service) DeleteVenue(ctx context.Context, id, groupID int64) error {
	hasActive, err := s.courtBookingRepo.HasActiveByVenueID(ctx, id)
	if err != nil {
		return fmt.Errorf("check active bookings: %w", err)
	}
	if hasActive {
		return ErrVenueHasActiveBookings
	}
	if err := s.repo.Delete(ctx, id, groupID); err != nil {
		return fmt.Errorf("delete venue: %w", err)
	}
	return nil
}

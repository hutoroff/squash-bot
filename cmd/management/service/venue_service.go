package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/sport"
)

// ErrVenueHasActiveBookings is returned when trying to delete a venue that still
// has active (non-canceled) court bookings.
var (
	ErrVenueHasActiveBookings = errors.New("venue has active court bookings and cannot be deleted")
	ErrInvalidVenue           = errors.New("invalid venue")
)

type VenueService struct {
	repo             VenueRepository
	courtBookingRepo CourtBookingRepository
}

func NewVenueService(repo VenueRepository, courtBookingRepo CourtBookingRepository) *VenueService {
	return &VenueService{repo: repo, courtBookingRepo: courtBookingRepo}
}

// validatePreferredGameTimes returns an error if any of the comma-separated times
// in preferredGameTimes is not present in the timeSlots list.
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

func validatePreventiveCancellationFraction(fraction string) error {
	if !models.IsPreventiveCancellationFraction(fraction) {
		return fmt.Errorf("preventive_cancellation_fraction must be 1/3, 1/2, or 2/3")
	}
	return nil
}

func validateVenue(venue *models.Venue) error {
	if len(venue.Sports) == 0 && venue.Courts != "" {
		venue.Sports = []models.VenueSport{{Sport: string(sport.Default), Courts: venue.Courts}}
	}
	if len(venue.Sports) == 0 {
		return fmt.Errorf("%w: at least one sport is required", ErrInvalidVenue)
	}
	seen := make(map[string]bool, len(venue.Sports))
	for _, venueSport := range venue.Sports {
		if !sport.Valid(venueSport.Sport) || venueSport.Courts == "" || seen[venueSport.Sport] {
			return fmt.Errorf("%w: invalid or duplicate sport %q", ErrInvalidVenue, venueSport.Sport)
		}
		seen[venueSport.Sport] = true
		if venueSport.PlayersPerCourt != nil && (*venueSport.PlayersPerCourt < 1 || *venueSport.PlayersPerCourt > sport.Get(sport.Sport(venueSport.Sport)).MaxPlayersPerCourt) {
			return fmt.Errorf("%w: players_per_court for %s must be between 1 and %d", ErrInvalidVenue, venueSport.Sport, sport.Get(sport.Sport(venueSport.Sport)).MaxPlayersPerCourt)
		}
	}
	venue.Courts = venue.CourtsFor(string(sport.Default))
	if venue.AutoBookingEnabled && !seen[string(sport.Default)] {
		return fmt.Errorf("%w: auto-booking requires squash", ErrInvalidVenue)
	}
	if err := validatePreferredGameTimes(venue.PreferredGameTimes, venue.TimeSlots); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidVenue, err)
	}
	if err := validateAutoBookingCourts(venue.AutoBookingCourts); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidVenue, err)
	}
	return nil
}

func (s *VenueService) CreateVenue(ctx context.Context, venue *models.Venue) (*models.Venue, error) {
	if err := validatePreventiveCancellationFraction(venue.PreventiveCancellationFraction); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidVenue, err)
	}
	if err := validateVenue(venue); err != nil {
		return nil, err
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

func (s *VenueService) GetVenueByIDAndGroupID(ctx context.Context, id, groupID int64) (*models.Venue, error) {
	return s.repo.GetByIDAndGroupID(ctx, id, groupID)
}

func (s *VenueService) UpdateVenue(ctx context.Context, venue *models.Venue, preventiveCancellationFraction *string) (*models.Venue, error) {
	venue.PreventiveCancellationFraction = ""
	if preventiveCancellationFraction != nil {
		if err := validatePreventiveCancellationFraction(*preventiveCancellationFraction); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidVenue, err)
		}
		venue.PreventiveCancellationFraction = *preventiveCancellationFraction
	}
	if err := validateVenue(venue); err != nil {
		return nil, err
	}
	updated, err := s.repo.Update(ctx, venue)
	if err != nil {
		return nil, fmt.Errorf("update venue: %w", err)
	}
	return updated, nil
}

func (s *VenueService) DeleteVenue(ctx context.Context, id, groupID int64) error {
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

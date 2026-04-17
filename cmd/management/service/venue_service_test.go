package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hutoroff/squash-bot/internal/models"
)

// ── validatePreferredGameTime ─────────────────────────────────────────────────

func TestValidatePreferredGameTime_Empty_AlwaysValid(t *testing.T) {
	// An empty preferred time means "no preference" — always valid regardless of time slots.
	if err := validatePreferredGameTime("", "18:00,19:00,20:00"); err != nil {
		t.Errorf("empty preferred time with slots: got error %v, want nil", err)
	}
	if err := validatePreferredGameTime("", ""); err != nil {
		t.Errorf("empty preferred time without slots: got error %v, want nil", err)
	}
}

func TestValidatePreferredGameTime_SlotPresentInList(t *testing.T) {
	if err := validatePreferredGameTime("19:00", "18:00,19:00,20:00"); err != nil {
		t.Errorf("preferred time in list: got error %v, want nil", err)
	}
}

func TestValidatePreferredGameTime_SlotNotInList(t *testing.T) {
	err := validatePreferredGameTime("21:00", "18:00,19:00,20:00")
	if err == nil {
		t.Error("preferred time not in list: got nil, want error")
	}
}

func TestValidatePreferredGameTime_SingleSlotList_Match(t *testing.T) {
	if err := validatePreferredGameTime("18:00", "18:00"); err != nil {
		t.Errorf("single-slot list match: got error %v, want nil", err)
	}
}

func TestValidatePreferredGameTime_SingleSlotList_NoMatch(t *testing.T) {
	err := validatePreferredGameTime("19:00", "18:00")
	if err == nil {
		t.Error("single-slot list no match: got nil, want error")
	}
}

// ── validateAutoBookingCourts ─────────────────────────────────────────────────

func TestValidateAutoBookingCourts_Empty_AlwaysValid(t *testing.T) {
	if err := validateAutoBookingCourts(""); err != nil {
		t.Errorf("empty auto-booking courts: got error %v, want nil", err)
	}
}

func TestValidateAutoBookingCourts_ValidIntegers(t *testing.T) {
	// Accepts any comma-separated integers, including large Eversports facility IDs.
	if err := validateAutoBookingCourts("77385,77386,77387"); err != nil {
		t.Errorf("valid Eversports IDs: got error %v, want nil", err)
	}
}

func TestValidateAutoBookingCourts_SmallLabels(t *testing.T) {
	// Small sequential labels (legacy configuration) are also accepted.
	if err := validateAutoBookingCourts("4,2,1"); err != nil {
		t.Errorf("small sequential labels: got error %v, want nil", err)
	}
}

func TestValidateAutoBookingCourts_NonInteger(t *testing.T) {
	err := validateAutoBookingCourts("court-a,2")
	if err == nil {
		t.Error("non-integer value: got nil, want error")
	}
}

func TestValidateAutoBookingCourts_DuplicateCourt(t *testing.T) {
	err := validateAutoBookingCourts("77385,77386,77385")
	if err == nil {
		t.Error("duplicate court in auto-booking list: got nil, want error")
	}
}

func TestValidateAutoBookingCourts_LargeIDNotSubsetConstraint(t *testing.T) {
	// Eversports IDs (77385) are no longer required to be in venue.Courts (1,2,3).
	// This was the root cause of the auto-booking failure: the old validator
	// rejected Eversports IDs because they weren't in the venue labels list.
	if err := validateAutoBookingCourts("77385,77386"); err != nil {
		t.Errorf("Eversports IDs should not be rejected: got error %v, want nil", err)
	}
}

// ── DeleteVenue ───────────────────────────────────────────────────────────────

func TestVenueService_DeleteVenue_ActiveBookings_Blocked(t *testing.T) {
	cbRepo := &stubCourtBookingRepo{hasActiveByVenue: true}
	svc := NewVenueService(&stubVenueRepo{venue: &models.Venue{ID: 10, GroupID: 20}}, cbRepo)

	err := svc.DeleteVenue(context.Background(), 10, 20)
	if !errors.Is(err, ErrVenueHasActiveBookings) {
		t.Errorf("want ErrVenueHasActiveBookings, got %v", err)
	}
}

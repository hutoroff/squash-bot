package service

import "testing"

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
	// Empty auto_booking_courts means "any available court" — always valid.
	if err := validateAutoBookingCourts("", "1,2,3,4"); err != nil {
		t.Errorf("empty auto-booking courts: got error %v, want nil", err)
	}
	if err := validateAutoBookingCourts("", ""); err != nil {
		t.Errorf("empty auto-booking courts with empty courts: got error %v, want nil", err)
	}
}

func TestValidateAutoBookingCourts_ValidSubset(t *testing.T) {
	if err := validateAutoBookingCourts("2,4", "1,2,3,4,5,6"); err != nil {
		t.Errorf("valid subset: got error %v, want nil", err)
	}
}

func TestValidateAutoBookingCourts_FullList(t *testing.T) {
	if err := validateAutoBookingCourts("1,2,3", "1,2,3"); err != nil {
		t.Errorf("auto-booking courts = full courts list: got error %v, want nil", err)
	}
}

func TestValidateAutoBookingCourts_SingleCourt(t *testing.T) {
	if err := validateAutoBookingCourts("5", "1,2,3,4,5,6"); err != nil {
		t.Errorf("single court in list: got error %v, want nil", err)
	}
}

func TestValidateAutoBookingCourts_CourtNotInVenue(t *testing.T) {
	err := validateAutoBookingCourts("7", "1,2,3,4,5,6")
	if err == nil {
		t.Error("court not in venue list: got nil, want error")
	}
}

func TestValidateAutoBookingCourts_OneCourtNotInVenue(t *testing.T) {
	// "2,7" — court 2 is valid but court 7 is not.
	err := validateAutoBookingCourts("2,7", "1,2,3,4,5,6")
	if err == nil {
		t.Error("partially invalid auto-booking courts: got nil, want error")
	}
}

func TestValidateAutoBookingCourts_DuplicateCourt(t *testing.T) {
	err := validateAutoBookingCourts("2,3,2", "1,2,3,4,5,6")
	if err == nil {
		t.Error("duplicate court in auto-booking list: got nil, want error")
	}
}

func TestValidateAutoBookingCourts_OrderPreserved(t *testing.T) {
	// Validation must not reorder courts: "4,2,1" is a valid (reversed) priority.
	if err := validateAutoBookingCourts("4,2,1", "1,2,3,4"); err != nil {
		t.Errorf("reversed priority order: got error %v, want nil", err)
	}
}

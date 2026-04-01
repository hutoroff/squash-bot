package service

import (
	"testing"
	"time"
)

// ── parsePreferredTime ────────────────────────────────────────────────────────

func TestParsePreferredTime_Valid(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Berlin")
	dt, err := parsePreferredTime("2026-04-01", "18:00", loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dt.Hour() != 18 || dt.Minute() != 0 {
		t.Errorf("expected 18:00, got %02d:%02d", dt.Hour(), dt.Minute())
	}
	if dt.Location().String() != loc.String() {
		t.Errorf("expected location %v, got %v", loc, dt.Location())
	}
}

func TestParsePreferredTime_UTCConversion(t *testing.T) {
	// Berlin is UTC+2 in summer; 18:00 local should be 16:00 UTC.
	berlin, _ := time.LoadLocation("Europe/Berlin")
	dt, err := parsePreferredTime("2026-06-15", "18:00", berlin)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	utc := dt.UTC()
	if utc.Hour() != 16 || utc.Minute() != 0 {
		t.Errorf("expected UTC 16:00, got %02d:%02d", utc.Hour(), utc.Minute())
	}
}

func TestParsePreferredTime_InvalidFormat_MissingColon(t *testing.T) {
	_, err := parsePreferredTime("2026-04-01", "1800", time.UTC)
	if err == nil {
		t.Fatal("expected error for missing colon, got nil")
	}
}

func TestParsePreferredTime_InvalidFormat_ShortHour(t *testing.T) {
	_, err := parsePreferredTime("2026-04-01", "8:00", time.UTC)
	if err == nil {
		t.Fatal("expected error for single-digit hour, got nil")
	}
}

func TestParsePreferredTime_InvalidTime_OutOfRange(t *testing.T) {
	_, err := parsePreferredTime("2026-04-01", "25:00", time.UTC)
	if err == nil {
		t.Fatal("expected error for hour 25, got nil")
	}
}

func TestParsePreferredTime_InvalidDate(t *testing.T) {
	_, err := parsePreferredTime("not-a-date", "18:00", time.UTC)
	if err == nil {
		t.Fatal("expected error for invalid date, got nil")
	}
}

// ── filterAvailableCourts ─────────────────────────────────────────────────────

func intPtrAB(v int) *int { return &v }

func TestFilterAvailableCourts_AllAvailable(t *testing.T) {
	venueCourts := map[int]bool{1: true, 2: true}
	slots := []BookingSlot{
		{Court: 1, CourtUUID: "uuid-1", Booking: nil},
		{Court: 2, CourtUUID: "uuid-2", Booking: nil},
	}
	got := filterAvailableCourts(slots, venueCourts, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 available courts, got %d: %v", len(got), got)
	}
	if got[0] != "uuid-1" || got[1] != "uuid-2" {
		t.Errorf("expected [uuid-1 uuid-2], got %v", got)
	}
}

func TestFilterAvailableCourts_DeduplicatesSameCourt(t *testing.T) {
	// Two slots for the same court UUID (e.g. two 5-min windows within the 10-min query)
	// must collapse to a single booking attempt.
	venueCourts := map[int]bool{1: true, 2: true}
	slots := []BookingSlot{
		{Court: 1, CourtUUID: "uuid-1", Booking: nil},
		{Court: 1, CourtUUID: "uuid-1", Booking: nil}, // duplicate
		{Court: 2, CourtUUID: "uuid-2", Booking: nil},
	}
	got := filterAvailableCourts(slots, venueCourts, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 distinct UUIDs after dedup, got %d: %v", len(got), got)
	}
	if got[0] != "uuid-1" || got[1] != "uuid-2" {
		t.Errorf("expected [uuid-1 uuid-2], got %v", got)
	}
}

func TestFilterAvailableCourts_ExcludesBooked(t *testing.T) {
	venueCourts := map[int]bool{1: true, 2: true}
	slots := []BookingSlot{
		{Court: 1, CourtUUID: "uuid-1", Booking: nil},
		{Court: 2, CourtUUID: "uuid-2", Booking: intPtrAB(99)}, // booked
	}
	got := filterAvailableCourts(slots, venueCourts, nil)
	if len(got) != 1 || got[0] != "uuid-1" {
		t.Errorf("expected [uuid-1], got %v", got)
	}
}

func TestFilterAvailableCourts_ExcludesNonVenueCourts(t *testing.T) {
	// Court 3 is in the slots but not in the venue configuration.
	venueCourts := map[int]bool{1: true, 2: true}
	slots := []BookingSlot{
		{Court: 1, CourtUUID: "uuid-1", Booking: nil},
		{Court: 3, CourtUUID: "uuid-3", Booking: nil}, // not in venue
	}
	got := filterAvailableCourts(slots, venueCourts, nil)
	if len(got) != 1 || got[0] != "uuid-1" {
		t.Errorf("expected [uuid-1], got %v", got)
	}
}

func TestFilterAvailableCourts_ExcludesMissingUUID(t *testing.T) {
	venueCourts := map[int]bool{1: true}
	slots := []BookingSlot{
		{Court: 1, CourtUUID: "", Booking: nil}, // no UUID
	}
	got := filterAvailableCourts(slots, venueCourts, nil)
	if len(got) != 0 {
		t.Errorf("expected no results for missing UUID, got %v", got)
	}
}

func TestFilterAvailableCourts_NoneAvailable(t *testing.T) {
	venueCourts := map[int]bool{1: true, 2: true}
	slots := []BookingSlot{
		{Court: 1, CourtUUID: "uuid-1", Booking: intPtrAB(1)},
		{Court: 2, CourtUUID: "uuid-2", Booking: intPtrAB(2)},
	}
	got := filterAvailableCourts(slots, venueCourts, nil)
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func TestFilterAvailableCourts_EmptySlots(t *testing.T) {
	venueCourts := map[int]bool{1: true}
	got := filterAvailableCourts(nil, venueCourts, nil)
	if len(got) != 0 {
		t.Errorf("expected empty result for nil slots, got %v", got)
	}
}

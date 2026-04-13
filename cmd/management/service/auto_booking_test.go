package service

import (
	"context"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
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

// ── slotQueryWindow ───────────────────────────────────────────────────────────

func TestSlotQueryWindow_DayBoundary(t *testing.T) {
	// Regression: when the code used UTC formatting, a game near midnight in a
	// non-UTC timezone caused slotQueryWindow to return the wrong date and HHMM.
	//
	// Berlin in January is CET (UTC+1).
	// 2026-01-15 23:30 UTC == 2026-01-16 00:30 CET.
	// The correct query window is on "2026-01-16" starting at "0030" (local time).
	// Using UTC would produce "2026-01-15" / "2330" — a different date entirely.
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	gameUTC := time.Date(2026, 1, 15, 23, 30, 0, 0, time.UTC)
	gameStart := gameUTC.In(berlin)

	date, startHHMM, endHHMM := slotQueryWindow(gameStart)

	if date != "2026-01-16" {
		t.Errorf("date: got %q, want %q (local Berlin date)", date, "2026-01-16")
	}
	if startHHMM != "0030" {
		t.Errorf("startHHMM: got %q, want %q (local Berlin time)", startHHMM, "0030")
	}
	if endHHMM != "0040" {
		t.Errorf("endHHMM: got %q, want %q (local Berlin time +10 min)", endHHMM, "0040")
	}

	// Sanity-check: UTC formatting would have produced a different date, confirming
	// the test would catch a regression that uses .UTC() instead of the local time.
	if gameUTC.Format("2006-01-02") != "2026-01-15" {
		t.Error("UTC date sanity check failed — test setup is wrong")
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

func TestFilterAvailableCourts_FallbackWhenVenueIDsMismatch(t *testing.T) {
	// Venue stores sequential labels 1–9; Eversports returns facility-specific IDs
	// like 77385. When no slot matches venueCourts, all available courts are returned.
	venueCourts := map[int]bool{1: true, 2: true, 3: true}
	slots := []BookingSlot{
		{Court: 77385, CourtUUID: "uuid-a", Booking: nil},
		{Court: 77386, CourtUUID: "uuid-b", Booking: nil},
		{Court: 77387, CourtUUID: "uuid-c", Booking: intPtrAB(1)}, // booked — excluded
	}
	got := filterAvailableCourts(slots, venueCourts, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 courts via fallback, got %d: %v", len(got), got)
	}
	if got[0] != "uuid-a" || got[1] != "uuid-b" {
		t.Errorf("expected [uuid-a uuid-b], got %v", got)
	}
}

func TestFilterAvailableCourts_FallbackWhenPreferredIDsMismatch(t *testing.T) {
	// Preferred courts are labels (1, 2) that don't match Eversports IDs (77385, 77386).
	// When no preferred court is found, all available courts are returned in API order.
	venueCourts := map[int]bool{77385: true, 77386: true}
	slots := []BookingSlot{
		{Court: 77385, CourtUUID: "uuid-a", Booking: nil},
		{Court: 77386, CourtUUID: "uuid-b", Booking: nil},
	}
	orderedPreferred := []int{1, 2} // labels, not Eversports IDs
	got := filterAvailableCourts(slots, venueCourts, orderedPreferred)
	if len(got) != 2 {
		t.Fatalf("expected 2 courts via fallback, got %d: %v", len(got), got)
	}
	if got[0] != "uuid-a" || got[1] != "uuid-b" {
		t.Errorf("expected [uuid-a uuid-b], got %v", got)
	}
}

// ── processAutoBookingForVenue ────────────────────────────────────────────────

func TestProcessAutoBookingForVenue_Disabled_DoesNotCallListMatches(t *testing.T) {
	client := &mockBookingClient{}
	s := &AutoBookingJob{
		bookingClient: client,
		logger:        noopLogger(),
	}
	venue := &models.Venue{
		ID:                 1,
		AutoBookingEnabled: false,
		Courts:             "1,2",
		PreferredGameTime:  "18:00",
		BookingOpensDays:   14,
	}
	lz := i18n.New(i18n.En)
	got := s.processAutoBookingForVenue(context.Background(), -1001, venue, time.Now().UTC(), time.UTC, lz)
	if got {
		t.Error("expected false when AutoBookingEnabled is false")
	}
	if client.listCalls != 0 {
		t.Errorf("ListMatches should not be called, got %d calls", client.listCalls)
	}
}

func TestProcessAutoBookingForVenue_InvalidPreferredTime_DoesNotCallListMatches(t *testing.T) {
	client := &mockBookingClient{}
	s := &AutoBookingJob{
		bookingClient: client,
		logger:        noopLogger(),
	}
	venue := &models.Venue{
		ID:                1,
		Courts:            "1,2",
		PreferredGameTime: "not-valid-time", // parsePreferredTime will fail
		BookingOpensDays:  14,
	}
	lz := i18n.New(i18n.En)
	got := s.processAutoBookingForVenue(context.Background(), -1001, venue, time.Now().UTC(), time.UTC, lz)
	if got {
		t.Error("expected false when PreferredGameTime is invalid")
	}
	if client.listCalls != 0 {
		t.Errorf("ListMatches should not be called, got %d calls", client.listCalls)
	}
}

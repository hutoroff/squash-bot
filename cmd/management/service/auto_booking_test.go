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

// ── filterFreeCourts ──────────────────────────────────────────────────────────

func TestFilterFreeCourts_AllFree(t *testing.T) {
	allCourts := []BookingCourt{
		{ID: "1", UUID: "uuid-1", Name: "Court 1"},
		{ID: "2", UUID: "uuid-2", Name: "Court 2"},
	}
	venueCourts := map[int]bool{1: true, 2: true}
	got := filterFreeCourts(allCourts, map[int]bool{}, venueCourts, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 free courts, got %d: %v", len(got), got)
	}
	if got[0] != "uuid-1" || got[1] != "uuid-2" {
		t.Errorf("expected [uuid-1 uuid-2], got %v", got)
	}
}

func TestFilterFreeCourts_ExcludesOccupied(t *testing.T) {
	allCourts := []BookingCourt{
		{ID: "1", UUID: "uuid-1"},
		{ID: "2", UUID: "uuid-2"},
	}
	venueCourts := map[int]bool{1: true, 2: true}
	occupied := map[int]bool{2: true}
	got := filterFreeCourts(allCourts, occupied, venueCourts, nil)
	if len(got) != 1 || got[0] != "uuid-1" {
		t.Errorf("expected [uuid-1], got %v", got)
	}
}

func TestFilterFreeCourts_ExcludesNonVenueCourts(t *testing.T) {
	// Court 3 is in allCourts but not in the venue configuration.
	allCourts := []BookingCourt{
		{ID: "1", UUID: "uuid-1"},
		{ID: "3", UUID: "uuid-3"},
	}
	venueCourts := map[int]bool{1: true, 2: true}
	got := filterFreeCourts(allCourts, map[int]bool{}, venueCourts, nil)
	if len(got) != 1 || got[0] != "uuid-1" {
		t.Errorf("expected [uuid-1], got %v", got)
	}
}

func TestFilterFreeCourts_ExcludesMissingUUID(t *testing.T) {
	allCourts := []BookingCourt{
		{ID: "1", UUID: ""}, // no UUID
	}
	venueCourts := map[int]bool{1: true}
	got := filterFreeCourts(allCourts, map[int]bool{}, venueCourts, nil)
	if len(got) != 0 {
		t.Errorf("expected no results for missing UUID, got %v", got)
	}
}

func TestFilterFreeCourts_NoneAvailable(t *testing.T) {
	allCourts := []BookingCourt{
		{ID: "1", UUID: "uuid-1"},
		{ID: "2", UUID: "uuid-2"},
	}
	venueCourts := map[int]bool{1: true, 2: true}
	occupied := map[int]bool{1: true, 2: true}
	got := filterFreeCourts(allCourts, occupied, venueCourts, nil)
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func TestFilterFreeCourts_EmptyCourts(t *testing.T) {
	venueCourts := map[int]bool{1: true}
	got := filterFreeCourts(nil, map[int]bool{}, venueCourts, nil)
	if len(got) != 0 {
		t.Errorf("expected empty result for nil courts, got %v", got)
	}
}

func TestFilterFreeCourts_FallbackWhenVenueIDsMismatch(t *testing.T) {
	// Venue stores sequential labels 1–3; Eversports returns facility-specific IDs.
	// When no court matches venueCourts, all free courts are returned.
	allCourts := []BookingCourt{
		{ID: "77385", UUID: "uuid-a"},
		{ID: "77386", UUID: "uuid-b"},
		{ID: "77387", UUID: "uuid-c"},
	}
	venueCourts := map[int]bool{1: true, 2: true, 3: true}
	occupied := map[int]bool{77387: true} // court c is occupied
	got := filterFreeCourts(allCourts, occupied, venueCourts, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 courts via fallback, got %d: %v", len(got), got)
	}
	if got[0] != "uuid-a" || got[1] != "uuid-b" {
		t.Errorf("expected [uuid-a uuid-b], got %v", got)
	}
}

func TestFilterFreeCourts_OrderedPreferred(t *testing.T) {
	// orderedPreferred = [3, 2, 1] → emit in that priority order.
	allCourts := []BookingCourt{
		{ID: "1", UUID: "uuid-1"},
		{ID: "2", UUID: "uuid-2"},
		{ID: "3", UUID: "uuid-3"},
	}
	venueCourts := map[int]bool{1: true, 2: true, 3: true}
	orderedPreferred := []int{3, 2, 1}
	got := filterFreeCourts(allCourts, map[int]bool{}, venueCourts, orderedPreferred)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d: %v", len(got), got)
	}
	if got[0] != "uuid-3" || got[1] != "uuid-2" || got[2] != "uuid-1" {
		t.Errorf("expected [uuid-3 uuid-2 uuid-1], got %v", got)
	}
}

func TestFilterFreeCourts_FallbackWhenPreferredIDsMismatch(t *testing.T) {
	// Preferred courts are labels (1, 2) that don't match Eversports IDs.
	// When no preferred court is found, all eligible courts are returned in API order.
	allCourts := []BookingCourt{
		{ID: "77385", UUID: "uuid-a"},
		{ID: "77386", UUID: "uuid-b"},
	}
	venueCourts := map[int]bool{77385: true, 77386: true}
	orderedPreferred := []int{1, 2} // labels, not Eversports IDs
	got := filterFreeCourts(allCourts, map[int]bool{}, venueCourts, orderedPreferred)
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

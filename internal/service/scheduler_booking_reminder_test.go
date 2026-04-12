package service

import (
	"testing"
	"time"
)

// ── bookingTargetWindow ───────────────────────────────────────────────────────

// TestBookingTargetWindow_BasicRange verifies that the window covers the correct
// calendar day when BookingOpensDays days are added to localNow.
func TestBookingTargetWindow_BasicRange(t *testing.T) {
	// Today (in group TZ) is 2026-04-12; booking opens 14 days from now.
	// Target day should be 2026-04-26 [00:00, 00:00 next day).
	localNow := time.Date(2026, 4, 12, 10, 2, 0, 0, time.UTC)
	start, end := bookingTargetWindow(localNow, 14)

	wantStart := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)

	if !start.Equal(wantStart) {
		t.Errorf("start: got %v, want %v", start, wantStart)
	}
	if !end.Equal(wantEnd) {
		t.Errorf("end: got %v, want %v", end, wantEnd)
	}
}

// TestBookingTargetWindow_CrossMonthBoundary verifies AddDate handles month overflow.
func TestBookingTargetWindow_CrossMonthBoundary(t *testing.T) {
	// Apr 25 + 14 days = May 9
	localNow := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	start, end := bookingTargetWindow(localNow, 14)

	wantStart := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)

	if !start.Equal(wantStart) {
		t.Errorf("start: got %v, want %v", start, wantStart)
	}
	if !end.Equal(wantEnd) {
		t.Errorf("end: got %v, want %v", end, wantEnd)
	}
}

// TestBookingTargetWindow_UsesGroupTimezone is a regression test for a hypothetical
// bug where the window might be computed in UTC rather than the group's local
// timezone. A game near midnight in UTC+3 (late evening UTC the day before) must
// produce a window anchored to the LOCAL day, not the UTC day.
//
//	localNow = 2026-06-01 10:00 UTC+3  →  target = 2026-06-08 (UTC+3)
//	If UTC were used: 2026-06-01 07:00 UTC → target = 2026-06-08 UTC; same day by
//	accident, so we pick a case where UTC and local diverge clearly:
//
//	localNow = 2026-06-01 01:00 UTC+3  →  UTC equivalent = 2026-05-31 22:00 UTC
//	days = 1 →  local target = 2026-06-02 (UTC+3), NOT 2026-06-01 (UTC).
func TestBookingTargetWindow_UsesGroupTimezone(t *testing.T) {
	loc := time.FixedZone("UTC+3", 3*60*60)
	// 01:00 in UTC+3 = 22:00 the previous day in UTC.
	localNow := time.Date(2026, 6, 1, 1, 0, 0, 0, loc)

	start, end := bookingTargetWindow(localNow, 1)

	// Local target: 2026-06-02 in UTC+3
	wantStart := time.Date(2026, 6, 2, 0, 0, 0, 0, loc)
	wantEnd := time.Date(2026, 6, 3, 0, 0, 0, 0, loc)

	if !start.Equal(wantStart) {
		t.Errorf("start: got %v, want %v (window must use group TZ, not UTC)", start, wantStart)
	}
	if !end.Equal(wantEnd) {
		t.Errorf("end: got %v, want %v", end, wantEnd)
	}

	// Sanity: confirm a UTC-based calculation would have landed on a different date.
	utcTarget := localNow.UTC().AddDate(0, 0, 1)
	utcStart := time.Date(utcTarget.Year(), utcTarget.Month(), utcTarget.Day(), 0, 0, 0, 0, time.UTC)
	if utcStart.Equal(wantStart) {
		t.Error("sanity check failed: UTC-based window accidentally equals local-TZ window — pick a different test case")
	}
}

// TestBookingTargetWindow_WindowIsExclusive verifies the window is a half-open
// [start, end) range covering exactly 24 hours.
func TestBookingTargetWindow_WindowIsExclusive(t *testing.T) {
	localNow := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	start, end := bookingTargetWindow(localNow, 7)

	duration := end.Sub(start)
	if duration != 24*time.Hour {
		t.Errorf("window duration: got %v, want 24h", duration)
	}
	if !start.Before(end) {
		t.Errorf("start %v is not before end %v", start, end)
	}
}

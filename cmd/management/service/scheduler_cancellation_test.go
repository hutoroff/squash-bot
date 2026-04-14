package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ── mock BookingServiceClient ─────────────────────────────────────────────────

type mockBookingClient struct {
	slots       []BookingSlot
	courts      []BookingCourt // returned by ListCourts (nil = no court name mapping)
	listErr     error
	cancelErr   error
	cancelCalls []string // UUIDs passed to CancelMatch
	listCalls   int      // number of times ListMatches was called
}

func (m *mockBookingClient) ListMatches(_ context.Context, _, _, _ string, _ bool) ([]BookingSlot, error) {
	m.listCalls++
	return m.slots, m.listErr
}

func (m *mockBookingClient) CancelMatch(_ context.Context, uuid string) error {
	m.cancelCalls = append(m.cancelCalls, uuid)
	return m.cancelErr
}

func (m *mockBookingClient) ListCourts(_ context.Context, _ string) ([]BookingCourt, error) {
	return m.courts, nil
}

func (m *mockBookingClient) BookMatch(_ context.Context, _, _, _ string) (*BookMatchResult, error) {
	return nil, nil
}

// ── test helpers ──────────────────────────────────────────────────────────────

func makeGame(courts string, courtsCount int, gameDate time.Time) *models.Game {
	return &models.Game{
		ID:          1,
		Courts:      courts,
		CourtsCount: courtsCount,
		GameDate:    gameDate,
	}
}

func makeGameWithVenue(courts string, courtsCount int, gameDate time.Time, autoBookingCourts string) *models.Game {
	g := makeGame(courts, courtsCount, gameDate)
	g.Venue = &models.Venue{AutoBookingCourts: autoBookingCourts}
	return g
}

func matchPtr(uuid string) *SlotMatchID { return &SlotMatchID{UUID: uuid} }

// ── cancelUnusedCourtsLogicOnly tests ────────────────────────────────────────

func TestCancelUnusedCourts_NilClient(t *testing.T) {
	s := &CancellationReminderJob{bookingClient: nil, logger: noopLogger()}
	game := makeGame("1,2,3", 3, time.Now().Add(time.Hour))

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 2, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 0 {
		t.Errorf("expected no cancellations, got %v", result.canceledCourts)
	}
	if result.remainingCount != 3 {
		t.Errorf("remaining count: got %d, want 3", result.remainingCount)
	}
}

func TestCancelUnusedCourts_ZeroCourtsToCancel(t *testing.T) {
	client := &mockBookingClient{}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 0, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.cancelCalls) != 0 {
		t.Errorf("expected no API calls, got %v", client.cancelCalls)
	}
	if result.remainingCount != 2 {
		t.Errorf("remaining count: got %d, want 2", result.remainingCount)
	}
}

func TestCancelUnusedCourts_ListError(t *testing.T) {
	client := &mockBookingClient{listErr: errors.New("network error")}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGame("1,2,3", 3, time.Now().Add(time.Hour))

	_, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err == nil {
		t.Fatal("expected error from ListMatches, got nil")
	}
}

func TestCancelUnusedCourts_NoOwnedSlots(t *testing.T) {
	client := &mockBookingClient{
		slots: []BookingSlot{
			{Court: 1, IsUserBookingOwner: false, Match: matchPtr("uuid-1")},
		},
	}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGame("1", 1, time.Now().Add(time.Hour))

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 0 {
		t.Errorf("expected no cancellations, got %v", result.canceledCourts)
	}
}

func TestCancelUnusedCourts_CancelsCorrectCourts(t *testing.T) {
	// Booked courts: 5, 6, 8, 9 → groups {5,6}, {8,9}; cancel 1 → should pick 6 (end of {5,6})
	client := &mockBookingClient{
		slots: []BookingSlot{
			{Court: 5, IsUserBookingOwner: true, Match: matchPtr("uuid-5")},
			{Court: 6, IsUserBookingOwner: true, Match: matchPtr("uuid-6")},
			{Court: 8, IsUserBookingOwner: true, Match: matchPtr("uuid-8")},
			{Court: 9, IsUserBookingOwner: true, Match: matchPtr("uuid-9")},
		},
	}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGame("5,6,8,9", 4, time.Now().Add(time.Hour))

	var updatedCourts string
	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC,
		func(_ context.Context, _ int64, courts string, _ int) error {
			updatedCourts = courts
			return nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 1 {
		t.Fatalf("expected 1 cancellation, got %d: %v", len(result.canceledCourts), result.canceledCourts)
	}
	if result.canceledCourts[0] != 6 {
		t.Errorf("expected court 6 to be canceled, got %d", result.canceledCourts[0])
	}
	if len(client.cancelCalls) != 1 || client.cancelCalls[0] != "uuid-6" {
		t.Errorf("cancel calls: got %v, want [uuid-6]", client.cancelCalls)
	}
	if updatedCourts != "5,8,9" {
		t.Errorf("updatedCourts: got %q, want %q", updatedCourts, "5,8,9")
	}
}

func TestCancelUnusedCourts_Cancel2Courts_SpecExample(t *testing.T) {
	// Booked: 1, 7, 8, 9 → groups {1}, {7,8,9}; cancel 2:
	//   round 1: {1} is smaller (len=1) → cancel 1 (uuid-1)
	//   round 2: now only {7,8,9} → cancel 9 (uuid-9)
	client := &mockBookingClient{
		slots: []BookingSlot{
			{Court: 1, IsUserBookingOwner: true, Match: matchPtr("uuid-1")},
			{Court: 7, IsUserBookingOwner: true, Match: matchPtr("uuid-7")},
			{Court: 8, IsUserBookingOwner: true, Match: matchPtr("uuid-8")},
			{Court: 9, IsUserBookingOwner: true, Match: matchPtr("uuid-9")},
		},
	}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGame("1,7,8,9", 4, time.Now().Add(time.Hour))

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 2, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 2 {
		t.Fatalf("expected 2 cancellations, got %v", result.canceledCourts)
	}

	canceled := make(map[int]bool)
	for _, c := range result.canceledCourts {
		canceled[c] = true
	}
	if !canceled[1] {
		t.Errorf("expected court 1 canceled; got %v", result.canceledCourts)
	}
	if !canceled[9] {
		t.Errorf("expected court 9 canceled; got %v", result.canceledCourts)
	}
}

func TestCancelUnusedCourts_DBWriteFailure_ReturnsError(t *testing.T) {
	// Courts are successfully canceled at the booking service, but the DB write fails.
	// The function must return an error so the caller can fall back to no-op and keep
	// the notification consistent with the persisted state.
	client := &mockBookingClient{
		slots: []BookingSlot{
			{Court: 1, IsUserBookingOwner: true, Match: matchPtr("uuid-1")},
			{Court: 2, IsUserBookingOwner: true, Match: matchPtr("uuid-2")},
		},
	}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))

	dbErr := errors.New("connection reset")
	_, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 2, time.UTC,
		func(_ context.Context, _ int64, _ string, _ int) error {
			return dbErr
		})
	if err == nil {
		t.Fatal("expected error from DB write failure, got nil")
	}
}

func TestCancelUnusedCourts_MismatchedLengths_ReturnsError(t *testing.T) {
	// Booking service returns 3 slots but game only has 2 courts.
	// The positional mapping would be ambiguous, so the function must return an error.
	client := &mockBookingClient{
		slots: []BookingSlot{
			{Court: 1, IsUserBookingOwner: true, Match: matchPtr("uuid-1")},
			{Court: 2, IsUserBookingOwner: true, Match: matchPtr("uuid-2")},
			{Court: 3, IsUserBookingOwner: true, Match: matchPtr("uuid-3")},
		},
	}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	// Game says 2 courts but Eversports has 3 bookings.
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))

	_, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err == nil {
		t.Fatal("expected error for mismatched court counts, got nil")
	}
}

func TestCancelUnusedCourts_CancelError_PartialSuccess(t *testing.T) {
	// CancelMatch returns error for the first call only.
	callCount := 0
	client := &mockBookingClientCustomCancel{
		slots: []BookingSlot{
			{Court: 1, IsUserBookingOwner: true, Match: matchPtr("uuid-1")},
			{Court: 2, IsUserBookingOwner: true, Match: matchPtr("uuid-2")},
		},
		cancelFn: func(uuid string) error {
			callCount++
			if callCount == 1 {
				return errors.New("cancel failed")
			}
			return nil
		},
	}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 2, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 1 cancellation succeeded despite first failure
	if len(result.canceledCourts) != 1 {
		t.Errorf("expected 1 successful cancellation, got %v", result.canceledCourts)
	}
}

// mockBookingClientCustomCancel lets each CancelMatch call use a custom function.
type mockBookingClientCustomCancel struct {
	slots    []BookingSlot
	cancelFn func(uuid string) error
}

func (m *mockBookingClientCustomCancel) ListMatches(_ context.Context, _, _, _ string, _ bool) ([]BookingSlot, error) {
	return m.slots, nil
}
func (m *mockBookingClientCustomCancel) CancelMatch(_ context.Context, uuid string) error {
	return m.cancelFn(uuid)
}
func (m *mockBookingClientCustomCancel) ListCourts(_ context.Context, _ string) ([]BookingCourt, error) {
	return nil, nil
}

func (m *mockBookingClientCustomCancel) BookMatch(_ context.Context, _, _, _ string) (*BookMatchResult, error) {
	return nil, nil
}

func TestCancelUnusedCourts_AutoBookingCourts_ReverseOrder(t *testing.T) {
	// auto_booking_courts = "5,7,8,9" (priority: 5 highest, 9 lowest)
	// All 4 are booked; cancel 2 → should cancel 9 first, then 8 (lowest priority first).
	// Courts list maps Eversports IDs to name-numbers so Phase 1 priority matching works.
	client := &mockBookingClient{
		slots: []BookingSlot{
			{Court: 5, IsUserBookingOwner: true, Match: matchPtr("uuid-5")},
			{Court: 7, IsUserBookingOwner: true, Match: matchPtr("uuid-7")},
			{Court: 8, IsUserBookingOwner: true, Match: matchPtr("uuid-8")},
			{Court: 9, IsUserBookingOwner: true, Match: matchPtr("uuid-9")},
		},
		courts: []BookingCourt{
			{ID: "5", UUID: "u5", Name: "Court 5"},
			{ID: "7", UUID: "u7", Name: "Court 7"},
			{ID: "8", UUID: "u8", Name: "Court 8"},
			{ID: "9", UUID: "u9", Name: "Court 9"},
		},
	}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGameWithVenue("5,7,8,9", 4, time.Now().Add(time.Hour), "5,7,8,9")

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 2, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 2 {
		t.Fatalf("expected 2 cancellations, got %v", result.canceledCourts)
	}
	canceledSet := make(map[int]bool)
	for _, c := range result.canceledCourts {
		canceledSet[c] = true
	}
	if !canceledSet[8] || !canceledSet[9] {
		t.Errorf("expected courts 8 and 9 canceled (lowest priority), got %v", result.canceledCourts)
	}
	// Cancel calls must be in reverse-priority order: uuid-9 first, then uuid-8.
	if len(client.cancelCalls) != 2 {
		t.Fatalf("expected 2 cancel API calls, got %v", client.cancelCalls)
	}
	if client.cancelCalls[0] != "uuid-9" || client.cancelCalls[1] != "uuid-8" {
		t.Errorf("cancel call order: got %v, want [uuid-9, uuid-8]", client.cancelCalls)
	}
}

func TestCancelUnusedCourts_AutoBookingCourts_FallbackForUnlistedCourts(t *testing.T) {
	// auto_booking_courts = "7"; courts 8 and 9 are booked but not in the priority list.
	// Cancel 2 → priority gives court 7; grouping fallback on {8,9} gives court 9.
	client := &mockBookingClient{
		slots: []BookingSlot{
			{Court: 7, IsUserBookingOwner: true, Match: matchPtr("uuid-7")},
			{Court: 8, IsUserBookingOwner: true, Match: matchPtr("uuid-8")},
			{Court: 9, IsUserBookingOwner: true, Match: matchPtr("uuid-9")},
		},
		courts: []BookingCourt{
			{ID: "7", UUID: "u7", Name: "Court 7"},
			{ID: "8", UUID: "u8", Name: "Court 8"},
			{ID: "9", UUID: "u9", Name: "Court 9"},
		},
	}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGameWithVenue("7,8,9", 3, time.Now().Add(time.Hour), "7")

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 2, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 2 {
		t.Fatalf("expected 2 cancellations, got %v", result.canceledCourts)
	}
	// Court 7 is canceled first (from priority list).
	if client.cancelCalls[0] != "uuid-7" {
		t.Errorf("expected uuid-7 canceled first (priority), got %s", client.cancelCalls[0])
	}
	// Court 9 is canceled second (grouping fallback: {8,9} → end = 9).
	if client.cancelCalls[1] != "uuid-9" {
		t.Errorf("expected uuid-9 canceled second (grouping fallback), got %s", client.cancelCalls[1])
	}
	canceledSet := make(map[int]bool)
	for _, c := range result.canceledCourts {
		canceledSet[c] = true
	}
	if !canceledSet[7] || !canceledSet[9] {
		t.Errorf("expected courts 7 and 9 canceled, got %v", result.canceledCourts)
	}
}

func TestCancelUnusedCourts_AutoBookingCourts_SomeMissing(t *testing.T) {
	// auto_booking_courts = "5,7,8,9"; only courts 5 and 9 are booked.
	// Cancel 1 → should cancel 9 (lowest priority that is actually booked).
	client := &mockBookingClient{
		slots: []BookingSlot{
			{Court: 5, IsUserBookingOwner: true, Match: matchPtr("uuid-5")},
			{Court: 9, IsUserBookingOwner: true, Match: matchPtr("uuid-9")},
		},
		courts: []BookingCourt{
			{ID: "5", UUID: "u5", Name: "Court 5"},
			{ID: "9", UUID: "u9", Name: "Court 9"},
		},
	}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGameWithVenue("5,9", 2, time.Now().Add(time.Hour), "5,7,8,9")

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 1 || result.canceledCourts[0] != 9 {
		t.Errorf("expected court 9 canceled, got %v", result.canceledCourts)
	}
	if len(client.cancelCalls) != 1 || client.cancelCalls[0] != "uuid-9" {
		t.Errorf("cancel calls: got %v, want [uuid-9]", client.cancelCalls)
	}
}

// ── determineScenario tests ───────────────────────────────────────────────────

func TestDetermineScenario(t *testing.T) {
	tests := []struct {
		name           string
		count          int
		newCourtsCount int
		canceledCourts []int
		want           string
	}{
		// all_good: at or over capacity
		{"all_good exact capacity", 4, 2, nil, "all_good"},
		{"all_good over capacity", 5, 2, nil, "all_good"},
		// canceled_balanced: courts canceled, now exactly at capacity
		{"all_good even after cancel", 4, 2, []int{9}, "canceled_balanced"},
		{"canceled_balanced", 6, 3, []int{9}, "canceled_balanced"},
		// odd_no_cancel: odd player count, nothing canceled
		{"odd_no_cancel", 3, 2, nil, "odd_no_cancel"},
		{"odd_no_cancel 1 player", 1, 1, nil, "odd_no_cancel"},
		// odd_canceled: odd player count, some courts canceled
		{"odd_canceled", 3, 2, []int{8}, "odd_canceled"},
		// all_canceled: all courts gone
		{"all_canceled", 0, 0, []int{1, 2}, "all_canceled"},
		{"all_canceled odd count", 1, 0, []int{1}, "all_canceled"},
		// even_no_cancel: even free spots, nothing canceled (booking service absent or no owned bookings)
		{"even_no_cancel zero players", 0, 3, nil, "even_no_cancel"},
		{"even_no_cancel 2 players 3 courts", 2, 3, nil, "even_no_cancel"},
		{"even_no_cancel empty canceled slice", 0, 3, []int{}, "even_no_cancel"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := determineScenario(tc.count, tc.newCourtsCount, tc.canceledCourts)
			if got != tc.want {
				t.Errorf("determineScenario(%d, %d, %v) = %q, want %q",
					tc.count, tc.newCourtsCount, tc.canceledCourts, got, tc.want)
			}
		})
	}
}

// ── courtsToCancel calculation ───────────────────────────────────────────────

func TestCourtsToCancel_Calculation(t *testing.T) {
	tests := []struct {
		count    int
		capacity int
		want     int
	}{
		{count: 4, capacity: 4, want: 0}, // exact match
		{count: 6, capacity: 4, want: 0}, // over capacity
		{count: 3, capacity: 4, want: 0}, // 1 empty spot — not a full court
		{count: 2, capacity: 4, want: 1}, // 2 empty → 1 court
		{count: 0, capacity: 4, want: 2}, // 4 empty → 2 courts
		{count: 4, capacity: 8, want: 2}, // 4 empty → 2 courts
		{count: 5, capacity: 8, want: 1}, // 3 empty → 1 court (floor(3/2)=1)
	}

	for _, tc := range tests {
		courtsToCancel := 0
		if tc.count < tc.capacity {
			courtsToCancel = (tc.capacity - tc.count) / 2
		}
		if courtsToCancel != tc.want {
			t.Errorf("count=%d capacity=%d: got courtsToCancel=%d, want %d",
				tc.count, tc.capacity, courtsToCancel, tc.want)
		}
	}
}

// noopUpdate is a courtsUpdater that does nothing, for tests that don't need DB writes.
func noopUpdate(_ context.Context, _ int64, _ string, _ int) error { return nil }

// ── recordingBookingClient ─────────────────────────────────────────────────────

// recordingBookingClient records the arguments passed to ListMatches so tests can
// assert that the caller passed the correct date and time parameters.
type recordingBookingClient struct {
	listedDate      string
	listedStartHHMM string
	listedEndHHMM   string
	slots           []BookingSlot
}

func (r *recordingBookingClient) ListMatches(_ context.Context, date, startHHMM, endHHMM string, _ bool) ([]BookingSlot, error) {
	r.listedDate = date
	r.listedStartHHMM = startHHMM
	r.listedEndHHMM = endHHMM
	return r.slots, nil
}
func (r *recordingBookingClient) CancelMatch(_ context.Context, _ string) error { return nil }
func (r *recordingBookingClient) ListCourts(_ context.Context, _ string) ([]BookingCourt, error) {
	return nil, nil
}
func (r *recordingBookingClient) BookMatch(_ context.Context, _, _, _ string) (*BookMatchResult, error) {
	return nil, nil
}

// ── timezone regression test ─────────────────────────────────────────────────

func TestCancelUnusedCourts_UsesLocalTimeForSlotQuery(t *testing.T) {
	// Regression: the original code called game.GameDate.UTC().Format(...) when
	// building the ListMatches parameters. For a game stored at 23:30 UTC (which is
	// 00:30 CET / Berlin the NEXT calendar day), that produced the wrong date and HHMM.
	//
	// Berlin in January is CET (UTC+1).
	// GameDate = 2026-01-15 23:30 UTC == 2026-01-16 00:30 CET.
	//
	// Correct (local Berlin): date "2026-01-16", startHHMM "0030", endHHMM "0040".
	// Wrong (UTC):            date "2026-01-15", startHHMM "2330", endHHMM "2340".
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	gameDate := time.Date(2026, 1, 15, 23, 30, 0, 0, time.UTC)
	recorder := &recordingBookingClient{slots: nil} // empty → no-op, but ListMatches is still called
	s := &CancellationReminderJob{bookingClient: recorder, logger: noopLogger()}
	game := makeGame("1,2", 2, gameDate)

	_, _ = s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, berlin, noopUpdate)

	if recorder.listedDate != "2026-01-16" {
		t.Errorf("date: got %q, want %q (local Berlin date, not UTC)", recorder.listedDate, "2026-01-16")
	}
	if recorder.listedStartHHMM != "0030" {
		t.Errorf("startHHMM: got %q, want %q (local Berlin time)", recorder.listedStartHHMM, "0030")
	}
	if recorder.listedEndHHMM != "0040" {
		t.Errorf("endHHMM: got %q, want %q (local Berlin time +10 min)", recorder.listedEndHHMM, "0040")
	}
}

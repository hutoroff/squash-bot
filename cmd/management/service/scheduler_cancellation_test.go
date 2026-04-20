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
	slots             []BookingSlot
	courts            []BookingCourt // returned by ListCourts (nil = no court name mapping)
	listErr           error
	cancelErr         error
	cancelCalls       []string // UUIDs passed to CancelMatch
	cancelLoginCalls  []string // logins passed to CancelMatch (parallel slice)
	listCalls         int      // number of times ListMatches was called
	bookResult        *BookMatchResult
	bookResultsByCall []*BookMatchResult // if non-empty, returned in round-robin; takes priority over bookResult
	bookCallCount     int
	bookErr           error
}

func (m *mockBookingClient) ListMatches(_ context.Context, _, _, _ string, _ bool, _, _ string) ([]BookingSlot, error) {
	m.listCalls++
	return m.slots, m.listErr
}

func (m *mockBookingClient) CancelMatch(_ context.Context, uuid, login, _ string) error {
	m.cancelCalls = append(m.cancelCalls, uuid)
	m.cancelLoginCalls = append(m.cancelLoginCalls, login)
	return m.cancelErr
}

func (m *mockBookingClient) ListCourts(_ context.Context, _, _, _ string) ([]BookingCourt, error) {
	return m.courts, nil
}

func (m *mockBookingClient) BookMatch(_ context.Context, _, _, _, _, _ string) (*BookMatchResult, error) {
	if len(m.bookResultsByCall) > 0 {
		idx := m.bookCallCount % len(m.bookResultsByCall)
		m.bookCallCount++
		return m.bookResultsByCall[idx], m.bookErr
	}
	return m.bookResult, m.bookErr
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

func (m *mockBookingClientCustomCancel) ListMatches(_ context.Context, _, _, _ string, _ bool, _, _ string) ([]BookingSlot, error) {
	return m.slots, nil
}
func (m *mockBookingClientCustomCancel) CancelMatch(_ context.Context, uuid, _, _ string) error {
	return m.cancelFn(uuid)
}
func (m *mockBookingClientCustomCancel) ListCourts(_ context.Context, _, _, _ string) ([]BookingCourt, error) {
	return nil, nil
}

func (m *mockBookingClientCustomCancel) BookMatch(_ context.Context, _, _, _, _, _ string) (*BookMatchResult, error) {
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

// ── cancelUsingListMatches court-name translation ─────────────────────────────

func TestCancelUnusedCourts_TranslatesEversportsIDToNameNumber(t *testing.T) {
	// Eversports court ID (500) is intentionally different from the venue name-number (3)
	// to verify the translation path. AutoBookingCourts is NOT set — only Phase 2 runs.
	client := &mockBookingClient{
		slots: []BookingSlot{
			{Court: 500, IsUserBookingOwner: true, Match: matchPtr("uuid-500")},
		},
		courts: []BookingCourt{
			{ID: "500", UUID: "u500", Name: "Court 3"},
		},
	}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGame("3", 1, time.Now().Add(time.Hour))

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 1 {
		t.Fatalf("expected 1 cancellation, got %d: %v", len(result.canceledCourts), result.canceledCourts)
	}
	// Must be the venue name-number (3), not the raw Eversports ID (500).
	if result.canceledCourts[0] != 3 {
		t.Errorf("expected court name-number 3, got %d (raw Eversports ID would be 500)", result.canceledCourts[0])
	}
}

func TestCancelUnusedCourts_FallsBackToEversportsID_WhenNoNameMapping(t *testing.T) {
	// ListCourts returns a court with no parseable number in the name.
	// The raw Eversports ID must be used as a fallback for display.
	client := &mockBookingClient{
		slots: []BookingSlot{
			{Court: 500, IsUserBookingOwner: true, Match: matchPtr("uuid-500")},
		},
		courts: []BookingCourt{
			{ID: "500", UUID: "u500", Name: "Squash"}, // no number → no mapping
		},
	}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGame("1", 1, time.Now().Add(time.Hour))

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 1 {
		t.Fatalf("expected 1 cancellation, got %d: %v", len(result.canceledCourts), result.canceledCourts)
	}
	// No name mapping → falls back to raw Eversports ID 500.
	if result.canceledCourts[0] != 500 {
		t.Errorf("expected fallback to raw Eversports ID 500, got %d", result.canceledCourts[0])
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

func (r *recordingBookingClient) ListMatches(_ context.Context, date, startHHMM, endHHMM string, _ bool, _, _ string) ([]BookingSlot, error) {
	r.listedDate = date
	r.listedStartHHMM = startHHMM
	r.listedEndHHMM = endHHMM
	return r.slots, nil
}
func (r *recordingBookingClient) CancelMatch(_ context.Context, _, _, _ string) error { return nil }
func (r *recordingBookingClient) ListCourts(_ context.Context, _, _, _ string) ([]BookingCourt, error) {
	return nil, nil
}
func (r *recordingBookingClient) BookMatch(_ context.Context, _, _, _, _, _ string) (*BookMatchResult, error) {
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

// ── stubCourtBookingRepo ──────────────────────────────────────────────────────

type stubCourtBookingRepo struct {
	entries               []*models.CourtBooking
	getErr                error
	marked                []string // matchIDs passed to MarkCanceled
	markErr               error
	saved                 []*models.CourtBooking // entries passed to Save
	hasActiveByCredential bool
	hasActiveByVenue      bool
}

func (r *stubCourtBookingRepo) Save(_ context.Context, cb *models.CourtBooking) error {
	r.saved = append(r.saved, cb)
	return nil
}

func (r *stubCourtBookingRepo) GetByVenueAndDate(_ context.Context, _ int64, _ time.Time) ([]*models.CourtBooking, error) {
	return r.entries, r.getErr
}

func (r *stubCourtBookingRepo) MarkCanceled(_ context.Context, matchID string) error {
	r.marked = append(r.marked, matchID)
	return r.markErr
}

func (r *stubCourtBookingRepo) HasActiveByCredentialID(_ context.Context, _ int64) (bool, error) {
	return r.hasActiveByCredential, nil
}

func (r *stubCourtBookingRepo) HasActiveByVenueID(_ context.Context, _ int64) (bool, error) {
	return r.hasActiveByVenue, nil
}

func (r *stubCourtBookingRepo) GetByVenueAndDateAndTime(_ context.Context, _ int64, _ time.Time, _ string) ([]*models.CourtBooking, error) {
	return r.entries, r.getErr
}

func (r *stubCourtBookingRepo) MarkCanceledByVenueAndDate(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

// courtBookingEntry is a convenience constructor for test CourtBooking entries.
func courtBookingEntry(label, matchID string, credID *int64) *models.CourtBooking {
	return &models.CourtBooking{CourtLabel: label, MatchID: matchID, CredentialID: credID}
}

// ── cancelUnusedCourtsLogicOnly dispatch tests ────────────────────────────────

func TestCancelUnusedCourts_NewFlow_EntriesFound_SkipsListMatches(t *testing.T) {
	// When court_bookings entries exist, the new flow must be used and
	// ListMatches must NOT be called.
	cbRepo := &stubCourtBookingRepo{entries: []*models.CourtBooking{
		courtBookingEntry("1", "match-1", nil),
		courtBookingEntry("2", "match-2", nil),
	}}
	client := &mockBookingClient{}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{bookingClient: client, courtBookingRepo: cbRepo, logger: noopLogger()}
	_, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.listCalls != 0 {
		t.Errorf("ListMatches should not be called when booking entries exist, got %d calls", client.listCalls)
	}
}

func TestCancelUnusedCourts_NewFlow_EmptyEntries_FallsBackToListMatches(t *testing.T) {
	// Empty booking entries must fall through to the legacy ListMatches path.
	cbRepo := &stubCourtBookingRepo{entries: nil}
	client := &mockBookingClient{}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{bookingClient: client, courtBookingRepo: cbRepo, logger: noopLogger()}
	_, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.listCalls != 1 {
		t.Errorf("expected ListMatches to be called once on fallback, got %d", client.listCalls)
	}
}

func TestCancelUnusedCourts_NewFlow_RepoError_FallsBackToListMatches(t *testing.T) {
	// A repo error must be logged and the legacy path must be used.
	cbRepo := &stubCourtBookingRepo{getErr: errors.New("db timeout")}
	client := &mockBookingClient{}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{bookingClient: client, courtBookingRepo: cbRepo, logger: noopLogger()}
	_, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.listCalls != 1 {
		t.Errorf("expected ListMatches fallback on repo error, got %d calls", client.listCalls)
	}
}

// ── cancelUsingBookingEntries tests ──────────────────────────────────────────

func TestCancelUsingBookingEntries_Phase2ConsecutiveGrouping(t *testing.T) {
	// No AutoBookingCourts → Phase 1 skipped, Phase 2 picks from the smallest group.
	// Courts 1,2,3 form one group; cancel 1 → picks 3 (end of the group).
	entries := []*models.CourtBooking{
		courtBookingEntry("1", "match-1", nil),
		courtBookingEntry("2", "match-2", nil),
		courtBookingEntry("3", "match-3", nil),
	}
	client := &mockBookingClient{}
	game := makeGame("1,2,3", 3, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{bookingClient: client, courtBookingRepo: &stubCourtBookingRepo{}, logger: noopLogger()}
	result, err := s.cancelUsingBookingEntries(context.Background(), game, entries, 1, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 1 || result.canceledCourts[0] != 3 {
		t.Errorf("expected court 3 canceled, got %v", result.canceledCourts)
	}
	if len(client.cancelCalls) != 1 || client.cancelCalls[0] != "match-3" {
		t.Errorf("expected CancelMatch called with match-3, got %v", client.cancelCalls)
	}
	if result.remainingCourts != "1,2" {
		t.Errorf("remaining courts: got %q, want %q", result.remainingCourts, "1,2")
	}
}

func TestCancelUsingBookingEntries_Phase1PrioritySelection(t *testing.T) {
	// auto_booking_courts = "5,7,8" (5 = highest priority, 8 = lowest).
	// Cancel 2: Phase 1 iterates in reverse → picks 8 first, then 7.
	credID := int64(1)
	entries := []*models.CourtBooking{
		courtBookingEntry("5", "match-5", &credID),
		courtBookingEntry("7", "match-7", &credID),
		courtBookingEntry("8", "match-8", &credID),
	}
	client := &mockBookingClient{}
	game := makeGame("5,7,8", 3, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10, AutoBookingCourts: "5,7,8"}

	s := &CancellationReminderJob{bookingClient: client, courtBookingRepo: &stubCourtBookingRepo{}, logger: noopLogger()}
	result, err := s.cancelUsingBookingEntries(context.Background(), game, entries, 2, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 2 {
		t.Fatalf("expected 2 canceled, got %d: %v", len(result.canceledCourts), result.canceledCourts)
	}
	// Phase 1 picks 8 then 7 (reverse priority order).
	if result.canceledCourts[0] != 8 || result.canceledCourts[1] != 7 {
		t.Errorf("expected [8 7] canceled in priority order, got %v", result.canceledCourts)
	}
}

func TestCancelUsingBookingEntries_NilCredService_UsesEmptyCredentials(t *testing.T) {
	// credService == nil: cancel must still proceed with empty login/password.
	credID := int64(99)
	entries := []*models.CourtBooking{
		courtBookingEntry("1", "match-1", &credID),
	}
	client := &mockBookingClient{}
	game := makeGame("1", 1, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{bookingClient: client, courtBookingRepo: &stubCourtBookingRepo{}, credService: nil, logger: noopLogger()}
	result, err := s.cancelUsingBookingEntries(context.Background(), game, entries, 1, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 1 {
		t.Fatalf("expected 1 canceled, got %d", len(result.canceledCourts))
	}
	if client.cancelLoginCalls[0] != "" {
		t.Errorf("expected empty login (env-var fallback), got %q", client.cancelLoginCalls[0])
	}
}

func TestCancelUsingBookingEntries_MarkCanceledCalledOnSuccess(t *testing.T) {
	cbRepo := &stubCourtBookingRepo{}
	entries := []*models.CourtBooking{
		courtBookingEntry("1", "match-1", nil),
		courtBookingEntry("2", "match-2", nil),
	}
	client := &mockBookingClient{}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{bookingClient: client, courtBookingRepo: cbRepo, logger: noopLogger()}
	result, err := s.cancelUsingBookingEntries(context.Background(), game, entries, 2, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 2 {
		t.Fatalf("expected 2 canceled, got %d", len(result.canceledCourts))
	}
	if len(cbRepo.marked) != 2 {
		t.Errorf("expected MarkCanceled called twice, got %d times: %v", len(cbRepo.marked), cbRepo.marked)
	}
}

func TestCancelUsingBookingEntries_PartialCancelFailure(t *testing.T) {
	// First cancel attempt fails; second succeeds.
	// canceledCourts must contain only the success; cancelErrors must contain the failure.
	entries := []*models.CourtBooking{
		courtBookingEntry("1", "match-1", nil),
		courtBookingEntry("2", "match-2", nil),
	}
	callNum := 0
	cancelErr := errors.New("cancel failed")
	client := &mockBookingClientCustomCancel{
		slots: nil,
		cancelFn: func(uuid string) error {
			callNum++
			if callNum == 1 {
				return cancelErr
			}
			return nil
		},
	}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{bookingClient: client, courtBookingRepo: &stubCourtBookingRepo{}, logger: noopLogger()}
	result, err := s.cancelUsingBookingEntries(context.Background(), game, entries, 2, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 1 {
		t.Errorf("expected 1 canceled (partial success), got %d: %v", len(result.canceledCourts), result.canceledCourts)
	}
	if len(result.cancelErrors) != 1 || !errors.Is(result.cancelErrors[0], cancelErr) {
		t.Errorf("expected 1 cancelError with the booking-service error, got %v", result.cancelErrors)
	}
}

func TestCancelUsingBookingEntries_AllCancelsFail_PopulatesCancelErrors(t *testing.T) {
	// All CancelMatch calls fail → canceledCourts empty, cancelErrors contains each failure.
	entries := []*models.CourtBooking{
		courtBookingEntry("1", "match-1", nil),
		courtBookingEntry("2", "match-2", nil),
	}
	cancelErr := errors.New("eversports: 403 forbidden")
	client := &mockBookingClient{cancelErr: cancelErr}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{bookingClient: client, courtBookingRepo: &stubCourtBookingRepo{}, logger: noopLogger()}
	result, err := s.cancelUsingBookingEntries(context.Background(), game, entries, 2, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 0 {
		t.Errorf("expected no canceled courts, got %v", result.canceledCourts)
	}
	if len(result.cancelErrors) != 2 {
		t.Errorf("expected 2 cancelErrors, got %d: %v", len(result.cancelErrors), result.cancelErrors)
	}
	for i, e := range result.cancelErrors {
		if !errors.Is(e, cancelErr) {
			t.Errorf("cancelErrors[%d] = %v, want wrapping %v", i, e, cancelErr)
		}
	}
}

func TestCancelUnusedCourts_ListMatchesPath_CancelFail_PopulatesCancelErrors(t *testing.T) {
	// Legacy ListMatches path: CancelMatch returns an error.
	// cancelErrors must be populated; no infrastructure error returned.
	cancelErr := errors.New("eversports: session expired")
	client := &mockBookingClient{
		slots: []BookingSlot{
			{Court: 1, IsUserBookingOwner: true, Match: matchPtr("uuid-1")},
		},
		cancelErr: cancelErr,
	}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGame("1", 1, time.Now().Add(time.Hour))

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected infrastructure error: %v", err)
	}
	if len(result.canceledCourts) != 0 {
		t.Errorf("expected no canceled courts, got %v", result.canceledCourts)
	}
	if len(result.cancelErrors) != 1 || !errors.Is(result.cancelErrors[0], cancelErr) {
		t.Errorf("expected cancelErrors to contain the booking-service error, got %v", result.cancelErrors)
	}
}

func TestCancelUsingBookingEntries_WithCredentials_PassesLoginPassword(t *testing.T) {
	// Credential is found in the service → login/password forwarded to CancelMatch.
	credID := int64(42)
	enc, _ := NewEncryptor(testHexKey)
	encPw, _ := enc.Encrypt("secret-pass")
	credRepo := &stubCredRepo{creds: []*models.VenueCredential{
		{ID: credID, VenueID: 1, Login: "user@example.com", EncryptedPassword: encPw},
	}}
	credSvc := NewVenueCredentialService(credRepo, &stubVenueRepo{}, nil, enc)

	entries := []*models.CourtBooking{
		courtBookingEntry("5", "match-5", &credID),
	}
	client := &mockBookingClient{}
	game := makeGame("5", 1, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 1}

	s := &CancellationReminderJob{bookingClient: client, courtBookingRepo: &stubCourtBookingRepo{}, credService: credSvc, logger: noopLogger()}
	result, err := s.cancelUsingBookingEntries(context.Background(), game, entries, 1, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 1 {
		t.Fatalf("expected 1 canceled, got %d", len(result.canceledCourts))
	}
	if len(client.cancelLoginCalls) == 0 || client.cancelLoginCalls[0] != "user@example.com" {
		t.Errorf("expected login %q forwarded to CancelMatch, got %v", "user@example.com", client.cancelLoginCalls)
	}
}

// ── per-slot cancellation routing stubs ───────────────────────────────────────

// routingCourtBookingRepo distinguishes between GetByVenueAndDate and
// GetByVenueAndDateAndTime so tests can verify which path was taken.
type routingCourtBookingRepo struct {
	byDateEntries    []*models.CourtBooking
	byTimeEntries    map[string][]*models.CourtBooking // keyed by game_time
	byTimeErr        error                             // if set, GetByVenueAndDateAndTime returns this error
	byDateCalled     bool
	byTimeCalled     bool
	byTimeCalledWith string
	marked           []string
}

func (r *routingCourtBookingRepo) Save(_ context.Context, _ *models.CourtBooking) error { return nil }

func (r *routingCourtBookingRepo) GetByVenueAndDate(_ context.Context, _ int64, _ time.Time) ([]*models.CourtBooking, error) {
	r.byDateCalled = true
	return r.byDateEntries, nil
}

func (r *routingCourtBookingRepo) GetByVenueAndDateAndTime(_ context.Context, _ int64, _ time.Time, gameTime string) ([]*models.CourtBooking, error) {
	r.byTimeCalled = true
	r.byTimeCalledWith = gameTime
	if r.byTimeErr != nil {
		return nil, r.byTimeErr
	}
	if r.byTimeEntries != nil {
		return r.byTimeEntries[gameTime], nil
	}
	return nil, nil
}

func (r *routingCourtBookingRepo) MarkCanceled(_ context.Context, matchID string) error {
	r.marked = append(r.marked, matchID)
	return nil
}

func (r *routingCourtBookingRepo) MarkCanceledByVenueAndDate(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

func (r *routingCourtBookingRepo) HasActiveByCredentialID(_ context.Context, _ int64) (bool, error) {
	return false, nil
}

func (r *routingCourtBookingRepo) HasActiveByVenueID(_ context.Context, _ int64) (bool, error) {
	return false, nil
}

// perGameAutoBookingResultRepo returns a specific AutoBookingResult per game ID,
// allowing tests to verify that cancellation routing uses the correct time slot.
type perGameAutoBookingResultRepo struct {
	resultByGameID map[int64]*models.AutoBookingResult
	errGetByGameID error // if set, GetByGameID returns this error
}

func (r *perGameAutoBookingResultRepo) Save(_ context.Context, _ int64, _ time.Time, _, _ string, _ int) error {
	return nil
}

func (r *perGameAutoBookingResultRepo) GetByVenueAndDate(_ context.Context, _ int64, _ time.Time) ([]*models.AutoBookingResult, error) {
	return nil, nil
}

func (r *perGameAutoBookingResultRepo) GetByVenueAndDateAndTime(_ context.Context, _ int64, _ time.Time, _ string) (*models.AutoBookingResult, error) {
	return nil, nil
}

func (r *perGameAutoBookingResultRepo) GetByGameID(_ context.Context, gameID int64) (*models.AutoBookingResult, error) {
	if r.errGetByGameID != nil {
		return nil, r.errGetByGameID
	}
	if r.resultByGameID != nil {
		return r.resultByGameID[gameID], nil
	}
	return nil, nil
}

func (r *perGameAutoBookingResultRepo) SetGameID(_ context.Context, _, _ int64) error { return nil }

// ── per-slot cancellation routing tests ───────────────────────────────────────

// TestCancelUnusedCourts_RoutesToSlotEntries_WhenGameLinkedToResult verifies that
// when a game has a linked auto_booking_result with a specific game_time, cancellation
// calls GetByVenueAndDateAndTime (not GetByVenueAndDate) so only that slot's
// court_bookings are touched — not courts from a different same-day session.
func TestCancelUnusedCourts_RoutesToSlotEntries_WhenGameLinkedToResult(t *testing.T) {
	gameDate := time.Date(2026, 6, 15, 18, 0, 0, 0, time.UTC)
	game := makeGame("1", 1, gameDate)
	game.ID = 42
	game.Venue = &models.Venue{ID: 10}

	entries18 := []*models.CourtBooking{courtBookingEntry("1", "match-18", nil)}
	cbRepo := &routingCourtBookingRepo{
		byTimeEntries: map[string][]*models.CourtBooking{
			"18:00": entries18,
		},
	}
	abrRepo := &perGameAutoBookingResultRepo{
		resultByGameID: map[int64]*models.AutoBookingResult{
			42: {ID: 1, GameTime: "18:00"},
		},
	}

	s := &CancellationReminderJob{
		bookingClient:         &mockBookingClient{},
		courtBookingRepo:      cbRepo,
		autoBookingResultRepo: abrRepo,
		logger:                noopLogger(),
	}

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cbRepo.byTimeCalled {
		t.Error("expected GetByVenueAndDateAndTime to be called for slot routing")
	}
	if cbRepo.byTimeCalledWith != "18:00" {
		t.Errorf("GetByVenueAndDateAndTime called with game_time %q, want %q", cbRepo.byTimeCalledWith, "18:00")
	}
	if cbRepo.byDateCalled {
		t.Error("GetByVenueAndDate must NOT be called when slot routing succeeded")
	}
	// ListMatches must not be called when booking entries are found.
	if len(result.canceledCourts) != 1 || result.canceledCourts[0] != 1 {
		t.Errorf("expected court 1 canceled, got %v", result.canceledCourts)
	}
}

// TestCancelUnusedCourts_FallsBackToAllDate_WhenNoLinkedResult verifies that when
// autoBookingResultRepo returns nil for a game (legacy or manually-created game),
// loadCourtBookingEntries falls back to GetByVenueAndDate.
func TestCancelUnusedCourts_FallsBackToAllDate_WhenNoLinkedResult(t *testing.T) {
	gameDate := time.Date(2026, 6, 15, 18, 0, 0, 0, time.UTC)
	game := makeGame("1", 1, gameDate)
	game.ID = 99
	game.Venue = &models.Venue{ID: 10}

	cbRepo := &routingCourtBookingRepo{
		byDateEntries: []*models.CourtBooking{courtBookingEntry("1", "match-fallback", nil)},
	}
	abrRepo := &perGameAutoBookingResultRepo{} // GetByGameID returns nil

	s := &CancellationReminderJob{
		bookingClient:         &mockBookingClient{},
		courtBookingRepo:      cbRepo,
		autoBookingResultRepo: abrRepo,
		logger:                noopLogger(),
	}

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cbRepo.byTimeCalled {
		t.Error("GetByVenueAndDateAndTime must NOT be called when no linked result")
	}
	if !cbRepo.byDateCalled {
		t.Error("expected GetByVenueAndDate to be called as fallback")
	}
	if len(result.canceledCourts) != 1 || result.canceledCourts[0] != 1 {
		t.Errorf("expected court 1 canceled via fallback, got %v", result.canceledCourts)
	}
}

// TestLoadCourtBookingEntries_NilAutoBookingResultRepo_FallsBackToByDate verifies that
// when autoBookingResultRepo is nil (optional field not wired), loadCourtBookingEntries
// skips the game-ID lookup entirely and falls back to GetByVenueAndDate.
func TestLoadCourtBookingEntries_NilAutoBookingResultRepo_FallsBackToByDate(t *testing.T) {
	gameDate := time.Date(2026, 6, 15, 18, 0, 0, 0, time.UTC)
	game := makeGame("1", 1, gameDate)
	game.ID = 55
	game.Venue = &models.Venue{ID: 10}

	cbRepo := &routingCourtBookingRepo{
		byDateEntries: []*models.CourtBooking{courtBookingEntry("1", "match-nil-repo", nil)},
	}

	s := &CancellationReminderJob{
		bookingClient:         &mockBookingClient{},
		courtBookingRepo:      cbRepo,
		autoBookingResultRepo: nil, // intentionally not set
		logger:                noopLogger(),
	}

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cbRepo.byTimeCalled {
		t.Error("GetByVenueAndDateAndTime must NOT be called when autoBookingResultRepo is nil")
	}
	if !cbRepo.byDateCalled {
		t.Error("expected GetByVenueAndDate to be called when autoBookingResultRepo is nil")
	}
	if len(result.canceledCourts) != 1 {
		t.Errorf("expected 1 court canceled, got %v", result.canceledCourts)
	}
}

// TestLoadCourtBookingEntries_GetByGameIDError_FallsBackToByDate verifies that when
// GetByGameID returns an error (transient DB failure), loadCourtBookingEntries logs a
// warning and falls back to GetByVenueAndDate rather than propagating the error.
func TestLoadCourtBookingEntries_GetByGameIDError_FallsBackToByDate(t *testing.T) {
	gameDate := time.Date(2026, 6, 15, 18, 0, 0, 0, time.UTC)
	game := makeGame("1", 1, gameDate)
	game.ID = 77
	game.Venue = &models.Venue{ID: 10}

	cbRepo := &routingCourtBookingRepo{
		byDateEntries: []*models.CourtBooking{courtBookingEntry("1", "match-err-fallback", nil)},
	}
	abrRepo := &perGameAutoBookingResultRepo{
		errGetByGameID: errors.New("db timeout"),
	}

	s := &CancellationReminderJob{
		bookingClient:         &mockBookingClient{},
		courtBookingRepo:      cbRepo,
		autoBookingResultRepo: abrRepo,
		logger:                noopLogger(),
	}

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cbRepo.byTimeCalled {
		t.Error("GetByVenueAndDateAndTime must NOT be called after GetByGameID error")
	}
	if !cbRepo.byDateCalled {
		t.Error("expected GetByVenueAndDate fallback after GetByGameID error")
	}
	if len(result.canceledCourts) != 1 {
		t.Errorf("expected 1 court canceled via fallback, got %v", result.canceledCourts)
	}
}

// TestLoadCourtBookingEntries_EmptyGameTime_FallsBackToByDate verifies that when
// GetByGameID returns a result with GameTime == "" (legacy row written before
// the game_time column was added), loadCourtBookingEntries falls back to
// GetByVenueAndDate rather than calling GetByVenueAndDateAndTime with an empty string.
func TestLoadCourtBookingEntries_EmptyGameTime_FallsBackToByDate(t *testing.T) {
	gameDate := time.Date(2026, 6, 15, 18, 0, 0, 0, time.UTC)
	game := makeGame("1", 1, gameDate)
	game.ID = 88
	game.Venue = &models.Venue{ID: 10}

	cbRepo := &routingCourtBookingRepo{
		byDateEntries: []*models.CourtBooking{courtBookingEntry("1", "match-legacy-gametime", nil)},
	}
	abrRepo := &perGameAutoBookingResultRepo{
		resultByGameID: map[int64]*models.AutoBookingResult{
			88: {ID: 5, GameTime: ""}, // legacy row: game_time not set
		},
	}

	s := &CancellationReminderJob{
		bookingClient:         &mockBookingClient{},
		courtBookingRepo:      cbRepo,
		autoBookingResultRepo: abrRepo,
		logger:                noopLogger(),
	}

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cbRepo.byTimeCalled {
		t.Error("GetByVenueAndDateAndTime must NOT be called for legacy result with empty GameTime")
	}
	if !cbRepo.byDateCalled {
		t.Error("expected GetByVenueAndDate fallback for legacy result with empty GameTime")
	}
	if len(result.canceledCourts) != 1 {
		t.Errorf("expected 1 court canceled via fallback, got %v", result.canceledCourts)
	}
}

// TestLoadCourtBookingEntries_GetByVenueAndDateAndTimeError_FallsBackToByDate verifies that
// when GetByVenueAndDateAndTime returns a transient error, loadCourtBookingEntries falls back
// to GetByVenueAndDate (credential-aware path) rather than propagating the error to the caller
// (which would fall all the way back to the legacy ListMatches path).
func TestLoadCourtBookingEntries_GetByVenueAndDateAndTimeError_FallsBackToByDate(t *testing.T) {
	gameDate := time.Date(2026, 6, 15, 18, 0, 0, 0, time.UTC)
	game := makeGame("1", 1, gameDate)
	game.ID = 66
	game.Venue = &models.Venue{ID: 10}

	cbRepo := &routingCourtBookingRepo{
		byDateEntries: []*models.CourtBooking{courtBookingEntry("1", "match-date-fallback", nil)},
		byTimeErr:     errors.New("db: connection refused"),
	}
	abrRepo := &perGameAutoBookingResultRepo{
		resultByGameID: map[int64]*models.AutoBookingResult{
			66: {ID: 7, GameTime: "18:00"},
		},
	}

	s := &CancellationReminderJob{
		bookingClient:         &mockBookingClient{},
		courtBookingRepo:      cbRepo,
		autoBookingResultRepo: abrRepo,
		logger:                noopLogger(),
	}

	result, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cbRepo.byTimeCalled {
		t.Error("expected GetByVenueAndDateAndTime to be attempted")
	}
	if !cbRepo.byDateCalled {
		t.Error("expected GetByVenueAndDate to be called as fallback after GetByVenueAndDateAndTime error")
	}
	// Cancellation must still succeed via the credential-aware path using the all-date entries.
	if len(result.canceledCourts) != 1 {
		t.Errorf("expected 1 court canceled via fallback, got %v", result.canceledCourts)
	}
}

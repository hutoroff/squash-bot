package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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

// algorithmCredID is the credential ID used by tests that exercise the court-selection
// algorithm but don't care about the specific login/password values.
var algorithmCredID = int64(1)

// algorithmCourtEntry creates a CourtBooking entry with algorithmCredID for algorithm tests.
func algorithmCourtEntry(label, matchID string) *models.CourtBooking {
	id := algorithmCredID
	return &models.CourtBooking{CourtLabel: label, MatchID: matchID, CredentialID: &id}
}

// makeAlgorithmCredService returns a VenueCredentialService that resolves credential ID 1
// to a fixed test account, for use in algorithm tests that need credentials but don't
// care about the specific login/password values.
func makeAlgorithmCredService() *VenueCredentialService {
	enc, _ := NewEncryptor(testHexKey)
	encPw, _ := enc.Encrypt("test-pass")
	credRepo := &stubCredRepo{creds: []*models.VenueCredential{
		{ID: 1, VenueID: 1, Login: "test@test.com", EncryptedPassword: encPw},
	}}
	return NewVenueCredentialService(credRepo, &stubVenueRepo{}, nil, enc)
}

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

// TestCancelUnusedCourts_LegacyPath_ReturnsError verifies that when the legacy ListMatches
// fallback is reached (no booking entries, no venue), an explicit error is returned —
// cancellation cannot proceed without per-credential court_bookings records.
func TestCancelUnusedCourts_LegacyPath_ReturnsError(t *testing.T) {
	client := &mockBookingClient{}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGame("1,2,3", 3, time.Now().Add(time.Hour))

	_, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err == nil {
		t.Fatal("expected error from legacy path, got nil")
	}
	if client.listCalls != 0 {
		t.Errorf("expected ListMatches NOT called, got %d calls", client.listCalls)
	}
}

func TestCancelUnusedCourts_NoCourtBookings_ReturnsError(t *testing.T) {
	client := &mockBookingClient{}
	s := &CancellationReminderJob{bookingClient: client, logger: noopLogger()}
	game := makeGame("1", 1, time.Now().Add(time.Hour))

	_, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err == nil {
		t.Fatal("expected error when no court_bookings records exist, got nil")
	}
}

func TestCancelUnusedCourts_CancelsCorrectCourts(t *testing.T) {
	// Booked courts: 5, 6, 8, 9 → groups {5,6}, {8,9}; cancel 1 → should pick 6 (end of {5,6})
	cbRepo := &stubCourtBookingRepo{entries: []*models.CourtBooking{
		algorithmCourtEntry("5", "uuid-5"),
		algorithmCourtEntry("6", "uuid-6"),
		algorithmCourtEntry("8", "uuid-8"),
		algorithmCourtEntry("9", "uuid-9"),
	}}
	client := &mockBookingClient{}
	game := makeGame("5,6,8,9", 4, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: cbRepo,
		credService:      makeAlgorithmCredService(),
		logger:           noopLogger(),
	}

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
	cbRepo := &stubCourtBookingRepo{entries: []*models.CourtBooking{
		algorithmCourtEntry("1", "uuid-1"),
		algorithmCourtEntry("7", "uuid-7"),
		algorithmCourtEntry("8", "uuid-8"),
		algorithmCourtEntry("9", "uuid-9"),
	}}
	client := &mockBookingClient{}
	game := makeGame("1,7,8,9", 4, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: cbRepo,
		credService:      makeAlgorithmCredService(),
		logger:           noopLogger(),
	}

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
	cbRepo := &stubCourtBookingRepo{entries: []*models.CourtBooking{
		algorithmCourtEntry("1", "uuid-1"),
		algorithmCourtEntry("2", "uuid-2"),
	}}
	client := &mockBookingClient{}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: cbRepo,
		credService:      makeAlgorithmCredService(),
		logger:           noopLogger(),
	}

	dbErr := errors.New("connection reset")
	_, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 2, time.UTC,
		func(_ context.Context, _ int64, _ string, _ int) error {
			return dbErr
		})
	if err == nil {
		t.Fatal("expected error from DB write failure, got nil")
	}
}

func TestCancelUnusedCourts_CancelError_PartialSuccess(t *testing.T) {
	// CancelMatch returns error for the first call only.
	callCount := 0
	client := &mockBookingClientCustomCancel{
		cancelFn: func(uuid string) error {
			callCount++
			if callCount == 1 {
				return errors.New("cancel failed")
			}
			return nil
		},
	}
	cbRepo := &stubCourtBookingRepo{entries: []*models.CourtBooking{
		algorithmCourtEntry("1", "uuid-1"),
		algorithmCourtEntry("2", "uuid-2"),
	}}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: cbRepo,
		credService:      makeAlgorithmCredService(),
		logger:           noopLogger(),
	}

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
	cbRepo := &stubCourtBookingRepo{entries: []*models.CourtBooking{
		algorithmCourtEntry("5", "uuid-5"),
		algorithmCourtEntry("7", "uuid-7"),
		algorithmCourtEntry("8", "uuid-8"),
		algorithmCourtEntry("9", "uuid-9"),
	}}
	client := &mockBookingClient{}
	game := makeGameWithVenue("5,7,8,9", 4, time.Now().Add(time.Hour), "5,7,8,9")
	game.Venue.ID = 10

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: cbRepo,
		credService:      makeAlgorithmCredService(),
		logger:           noopLogger(),
	}

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
	cbRepo := &stubCourtBookingRepo{entries: []*models.CourtBooking{
		algorithmCourtEntry("7", "uuid-7"),
		algorithmCourtEntry("8", "uuid-8"),
		algorithmCourtEntry("9", "uuid-9"),
	}}
	client := &mockBookingClient{}
	game := makeGameWithVenue("7,8,9", 3, time.Now().Add(time.Hour), "7")
	game.Venue.ID = 10

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: cbRepo,
		credService:      makeAlgorithmCredService(),
		logger:           noopLogger(),
	}

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
	cbRepo := &stubCourtBookingRepo{entries: []*models.CourtBooking{
		algorithmCourtEntry("5", "uuid-5"),
		algorithmCourtEntry("9", "uuid-9"),
	}}
	client := &mockBookingClient{}
	game := makeGameWithVenue("5,9", 2, time.Now().Add(time.Hour), "5,7,8,9")
	game.Venue.ID = 10

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: cbRepo,
		credService:      makeAlgorithmCredService(),
		logger:           noopLogger(),
	}

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
		algorithmCourtEntry("1", "match-1"),
		algorithmCourtEntry("2", "match-2"),
	}}
	client := &mockBookingClient{}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: cbRepo,
		credService:      makeAlgorithmCredService(),
		logger:           noopLogger(),
	}
	_, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.listCalls != 0 {
		t.Errorf("ListMatches should not be called when booking entries exist, got %d calls", client.listCalls)
	}
}

// TestCancelUnusedCourts_NewFlow_EmptyEntries_Error verifies that when the court_bookings
// repo returns no entries, the function falls through to the legacy path which now returns
// an explicit error — per-credential records are required for cancellation.
func TestCancelUnusedCourts_NewFlow_EmptyEntries_Error(t *testing.T) {
	cbRepo := &stubCourtBookingRepo{entries: nil}
	client := &mockBookingClient{}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{bookingClient: client, courtBookingRepo: cbRepo, logger: noopLogger()}
	_, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err == nil {
		t.Fatal("expected error when court_bookings returns no entries, got nil")
	}
	if client.listCalls != 0 {
		t.Errorf("expected ListMatches NOT called, got %d", client.listCalls)
	}
}

// TestCancelUnusedCourts_NewFlow_RepoError_Error verifies that a repo error falls through
// to the legacy path which now returns an explicit error — cancellation cannot continue
// without per-credential court_bookings records.
func TestCancelUnusedCourts_NewFlow_RepoError_Error(t *testing.T) {
	cbRepo := &stubCourtBookingRepo{getErr: errors.New("db timeout")}
	client := &mockBookingClient{}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{bookingClient: client, courtBookingRepo: cbRepo, logger: noopLogger()}
	_, err := s.cancelUnusedCourtsLogicOnly(context.Background(), game, 1, time.UTC, noopUpdate)
	if err == nil {
		t.Fatal("expected error when court_bookings repo fails, got nil")
	}
	if client.listCalls != 0 {
		t.Errorf("expected ListMatches NOT called, got %d calls", client.listCalls)
	}
}

// ── cancelUsingBookingEntries tests ──────────────────────────────────────────

func TestCancelUsingBookingEntries_Phase2ConsecutiveGrouping(t *testing.T) {
	// No AutoBookingCourts → Phase 1 skipped, Phase 2 picks from the smallest group.
	// Courts 1,2,3 form one group; cancel 1 → picks 3 (end of the group).
	entries := []*models.CourtBooking{
		algorithmCourtEntry("1", "match-1"),
		algorithmCourtEntry("2", "match-2"),
		algorithmCourtEntry("3", "match-3"),
	}
	client := &mockBookingClient{}
	game := makeGame("1,2,3", 3, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: &stubCourtBookingRepo{},
		credService:      makeAlgorithmCredService(),
		logger:           noopLogger(),
	}
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
	entries := []*models.CourtBooking{
		algorithmCourtEntry("5", "match-5"),
		algorithmCourtEntry("7", "match-7"),
		algorithmCourtEntry("8", "match-8"),
	}
	client := &mockBookingClient{}
	game := makeGame("5,7,8", 3, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10, AutoBookingCourts: "5,7,8"}

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: &stubCourtBookingRepo{},
		credService:      makeAlgorithmCredService(),
		logger:           noopLogger(),
	}
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

// TestCancelUsingBookingEntries_NilCredService_SkipsCourt verifies that when credService
// is nil, courts with a non-nil CredentialID cannot have their credential resolved and
// are skipped — added to cancelErrors rather than canceled.
func TestCancelUsingBookingEntries_NilCredService_SkipsCourt(t *testing.T) {
	credID := int64(99)
	entries := []*models.CourtBooking{
		courtBookingEntry("1", "match-1", &credID),
	}
	client := &mockBookingClient{}
	game := makeGame("1", 1, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: &stubCourtBookingRepo{},
		credService:      nil, // cannot resolve credentials
		logger:           noopLogger(),
	}
	result, err := s.cancelUsingBookingEntries(context.Background(), game, entries, 1, noopUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.canceledCourts) != 0 {
		t.Errorf("expected no canceled courts (credential missing), got %v", result.canceledCourts)
	}
	if len(result.cancelErrors) == 0 {
		t.Errorf("expected cancelErrors to be populated when credential is missing")
	}
	if len(client.cancelCalls) != 0 {
		t.Errorf("expected CancelMatch NOT called, got %v", client.cancelCalls)
	}
}

func TestCancelUsingBookingEntries_MarkCanceledCalledOnSuccess(t *testing.T) {
	cbRepo := &stubCourtBookingRepo{}
	entries := []*models.CourtBooking{
		algorithmCourtEntry("1", "match-1"),
		algorithmCourtEntry("2", "match-2"),
	}
	client := &mockBookingClient{}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: cbRepo,
		credService:      makeAlgorithmCredService(),
		logger:           noopLogger(),
	}
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
		algorithmCourtEntry("1", "match-1"),
		algorithmCourtEntry("2", "match-2"),
	}
	callNum := 0
	cancelErr := errors.New("cancel failed")
	client := &mockBookingClientCustomCancel{
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

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: &stubCourtBookingRepo{},
		credService:      makeAlgorithmCredService(),
		logger:           noopLogger(),
	}
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
		algorithmCourtEntry("1", "match-1"),
		algorithmCourtEntry("2", "match-2"),
	}
	cancelErr := errors.New("eversports: 403 forbidden")
	client := &mockBookingClient{cancelErr: cancelErr}
	game := makeGame("1,2", 2, time.Now().Add(time.Hour))
	game.Venue = &models.Venue{ID: 10}

	s := &CancellationReminderJob{
		bookingClient:    client,
		courtBookingRepo: &stubCourtBookingRepo{},
		credService:      makeAlgorithmCredService(),
		logger:           noopLogger(),
	}
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

	entries18 := []*models.CourtBooking{algorithmCourtEntry("1", "match-18")}
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
		credService:           makeAlgorithmCredService(),
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
		byDateEntries: []*models.CourtBooking{algorithmCourtEntry("1", "match-fallback")},
	}
	abrRepo := &perGameAutoBookingResultRepo{} // GetByGameID returns nil

	s := &CancellationReminderJob{
		bookingClient:         &mockBookingClient{},
		courtBookingRepo:      cbRepo,
		credService:           makeAlgorithmCredService(),
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
		byDateEntries: []*models.CourtBooking{algorithmCourtEntry("1", "match-nil-repo")},
	}

	s := &CancellationReminderJob{
		bookingClient:         &mockBookingClient{},
		courtBookingRepo:      cbRepo,
		credService:           makeAlgorithmCredService(),
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
		byDateEntries: []*models.CourtBooking{algorithmCourtEntry("1", "match-err-fallback")},
	}
	abrRepo := &perGameAutoBookingResultRepo{
		errGetByGameID: errors.New("db timeout"),
	}

	s := &CancellationReminderJob{
		bookingClient:         &mockBookingClient{},
		courtBookingRepo:      cbRepo,
		credService:           makeAlgorithmCredService(),
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
		byDateEntries: []*models.CourtBooking{algorithmCourtEntry("1", "match-legacy-gametime")},
	}
	abrRepo := &perGameAutoBookingResultRepo{
		resultByGameID: map[int64]*models.AutoBookingResult{
			88: {ID: 5, GameTime: ""}, // legacy row: game_time not set
		},
	}

	s := &CancellationReminderJob{
		bookingClient:         &mockBookingClient{},
		courtBookingRepo:      cbRepo,
		credService:           makeAlgorithmCredService(),
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
		byDateEntries: []*models.CourtBooking{algorithmCourtEntry("1", "match-date-fallback")},
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
		credService:           makeAlgorithmCredService(),
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
	if len(result.canceledCourts) != 1 {
		t.Errorf("expected 1 court canceled via fallback, got %v", result.canceledCourts)
	}
}

// ── stubs for processCancellationReminder tests ───────────────────────────────

type stubPartRepoPC struct{ count int }

func (r *stubPartRepoPC) Upsert(_ context.Context, _, _ int64, _ models.ParticipationStatus) error {
	return nil
}
func (r *stubPartRepoPC) GetByGame(_ context.Context, _ int64) ([]*models.GameParticipation, error) {
	return nil, nil
}
func (r *stubPartRepoPC) DeleteByGameAndPlayer(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}
func (r *stubPartRepoPC) GetRegisteredCount(_ context.Context, _ int64) (int, error) {
	return r.count, nil
}

type stubGuestRepoPC struct{ count int }

func (r *stubGuestRepoPC) AddGuest(_ context.Context, _, _ int64) (bool, error) { return false, nil }
func (r *stubGuestRepoPC) RemoveLatestGuest(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}
func (r *stubGuestRepoPC) GetByGame(_ context.Context, _ int64) ([]*models.GuestParticipation, error) {
	return nil, nil
}
func (r *stubGuestRepoPC) DeleteByID(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}
func (r *stubGuestRepoPC) GetCountByGame(_ context.Context, _ int64) (int, error) {
	return r.count, nil
}

type stubGroupRepoPC struct{ group *models.Group }

func (r *stubGroupRepoPC) Upsert(_ context.Context, _ int64, _ string, _ bool) error { return nil }
func (r *stubGroupRepoPC) SetLanguage(_ context.Context, _ int64, _ string) error    { return nil }
func (r *stubGroupRepoPC) SetTimezone(_ context.Context, _ int64, _ string) error    { return nil }
func (r *stubGroupRepoPC) Remove(_ context.Context, _ int64) error                    { return nil }
func (r *stubGroupRepoPC) Exists(_ context.Context, _ int64) (bool, error)            { return true, nil }
func (r *stubGroupRepoPC) GetByID(_ context.Context, _ int64) (*models.Group, error) {
	return r.group, nil
}
func (r *stubGroupRepoPC) GetAll(_ context.Context) ([]models.Group, error) { return nil, nil }

type stubGameRepoPC struct{}

func (r *stubGameRepoPC) Create(_ context.Context, _ *models.Game) (*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoPC) GetByID(_ context.Context, _ int64) (*models.Game, error) { return nil, nil }
func (r *stubGameRepoPC) GetUpcomingGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoPC) GetUpcomingGamesByChatIDs(_ context.Context, _ []int64) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoPC) UpdateMessageID(_ context.Context, _, _ int64) error { return nil }
func (r *stubGameRepoPC) UpdateCourts(_ context.Context, _ int64, _ string, _ int) error {
	return nil
}
func (r *stubGameRepoPC) GetNextGameForTelegramUser(_ context.Context, _ int64) (*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoPC) GetGamesForPlayer(_ context.Context, _ int64) ([]models.PlayerGame, error) {
	return nil, nil
}
func (r *stubGameRepoPC) GetUpcomingUnnotifiedGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoPC) GetUncompletedGamesByGroupAndDay(_ context.Context, _ int64, _, _ time.Time) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoPC) MarkNotifiedDayBefore(_ context.Context, _ int64) error { return nil }
func (r *stubGameRepoPC) MarkCompleted(_ context.Context, _ int64) error          { return nil }

type captureSendAPI struct{ msgs []tgbotapi.MessageConfig }

func (a *captureSendAPI) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	if msg, ok := c.(tgbotapi.MessageConfig); ok {
		a.msgs = append(a.msgs, msg)
	}
	return tgbotapi.Message{}, nil
}
func (a *captureSendAPI) Request(_ tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{Ok: true}, nil
}
func (a *captureSendAPI) GetChatAdministrators(_ tgbotapi.ChatAdministratorsConfig) ([]tgbotapi.ChatMember, error) {
	return nil, nil
}

type spyNotifier struct{ calledGameIDs []int64 }

func (n *spyNotifier) EditGameMessage(_ context.Context, gameID int64) {
	n.calledGameIDs = append(n.calledGameIDs, gameID)
}

// makeJobForPC builds a CancellationReminderJob wired with the stubs
// needed by processCancellationReminder.
func makeJobForPC(
	api TelegramAPI,
	gameRepo GameRepository,
	partCount, guestCount int,
	group *models.Group,
	notifier Notifier,
	courtBookingRepo CourtBookingRepository,
	bookingClient BookingServiceClient,
) *CancellationReminderJob {
	return &CancellationReminderJob{
		api:              api,
		gameRepo:         gameRepo,
		partRepo:         &stubPartRepoPC{count: partCount},
		guestRepo:        &stubGuestRepoPC{count: guestCount},
		groupRepo:        &stubGroupRepoPC{group: group},
		notifier:         notifier,
		bookingClient:    bookingClient,
		courtBookingRepo: courtBookingRepo,
		loc:              time.UTC,
		logger:           noopLogger(),
		pollWindow:       5 * time.Minute,
	}
}

// ── processCancellationReminder behaviour tests ───────────────────────────────

// TestProcessCancellationReminder_NotifierCalledWhenCourtsAreCanceled verifies that
// when courts are successfully canceled, notifier.EditGameMessage is called so the
// pinned game announcement is updated to reflect the reduced court count.
func TestProcessCancellationReminder_NotifierCalledWhenCourtsAreCanceled(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	// 0 players, 2 courts → capacity=4, courtsToCancel=2 → both courts canceled.
	cbRepo := &stubCourtBookingRepo{entries: []*models.CourtBooking{
		algorithmCourtEntry("1", "match-1"),
		algorithmCourtEntry("2", "match-2"),
	}}
	notifier := &spyNotifier{}

	game := makeGame("1,2", 2, time.Now().Add(24*time.Hour))
	game.ID = 10
	game.Venue = &models.Venue{ID: 5}

	job := makeJobForPC(&captureSendAPI{}, &stubGameRepoPC{}, 0, 0, group, notifier, cbRepo, &mockBookingClient{})
	job.credService = makeAlgorithmCredService()
	job.processCancellationReminder(context.Background(), game)

	if len(notifier.calledGameIDs) != 1 || notifier.calledGameIDs[0] != 10 {
		t.Errorf("expected EditGameMessage called for game 10, got %v", notifier.calledGameIDs)
	}
}

// TestProcessCancellationReminder_NotifierNotCalledWhenNoCancellation verifies that
// when no courts are canceled (game is at full capacity), EditGameMessage is NOT called
// — there is nothing to update in the game announcement.
func TestProcessCancellationReminder_NotifierNotCalledWhenNoCancellation(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	// 4 players, 2 courts → capacity=4, courtsToCancel=0 → no cancellation.
	notifier := &spyNotifier{}

	game := makeGame("1,2", 2, time.Now().Add(24*time.Hour))
	game.ID = 11

	job := makeJobForPC(&captureSendAPI{}, &stubGameRepoPC{}, 4, 0, group, notifier, nil, nil)
	job.processCancellationReminder(context.Background(), game)

	if len(notifier.calledGameIDs) != 0 {
		t.Errorf("expected EditGameMessage NOT called, got calls for %v", notifier.calledGameIDs)
	}
}

// TestProcessCancellationReminder_ReminderIsReplyToGameMessage verifies that
// the reminder notification is sent as a reply to the original game announcement
// when game.MessageID is set.
func TestProcessCancellationReminder_ReminderIsReplyToGameMessage(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	// Full capacity → no cancellation, clean scenario for inspecting the sent message.
	api := &captureSendAPI{}

	msgID := int64(42)
	game := makeGame("1,2", 2, time.Now().Add(24*time.Hour))
	game.ID = 12
	game.MessageID = &msgID

	job := makeJobForPC(api, &stubGameRepoPC{}, 4, 0, group, nil, nil, nil)
	job.processCancellationReminder(context.Background(), game)

	if len(api.msgs) == 0 {
		t.Fatal("expected at least one message sent, got none")
	}
	if api.msgs[0].ReplyToMessageID != 42 {
		t.Errorf("ReplyToMessageID: got %d, want 42", api.msgs[0].ReplyToMessageID)
	}
}

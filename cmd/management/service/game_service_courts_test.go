package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// ── mockCourtBookingRepo ──────────────────────────────────────────────────────

type mockCourtBookingRepo struct {
	// labelEntries are returned by GetActiveByVenueDateAndLabels.
	labelEntries []*models.CourtBooking
	// timeEntries are returned by GetByVenueAndDateAndTime.
	timeEntries    []*models.CourtBooking
	getErr         error
	markedCanceled []string
}

func (r *mockCourtBookingRepo) Save(_ context.Context, _ *models.CourtBooking) error { return nil }
func (r *mockCourtBookingRepo) GetByVenueAndDate(_ context.Context, _ int64, _ time.Time) ([]*models.CourtBooking, error) {
	return nil, nil
}
func (r *mockCourtBookingRepo) GetByVenueAndDateAndTime(_ context.Context, _ int64, _ time.Time, _ string) ([]*models.CourtBooking, error) {
	return r.timeEntries, r.getErr
}
func (r *mockCourtBookingRepo) MarkCanceled(_ context.Context, matchID string) error {
	r.markedCanceled = append(r.markedCanceled, matchID)
	return nil
}
func (r *mockCourtBookingRepo) MarkCanceledByVenueAndDate(_ context.Context, _ int64, _ time.Time) error {
	return nil
}
func (r *mockCourtBookingRepo) HasActiveByCredentialID(_ context.Context, _ int64) (bool, error) {
	return false, nil
}
func (r *mockCourtBookingRepo) HasActiveByVenueID(_ context.Context, _ int64) (bool, error) {
	return false, nil
}
func (r *mockCourtBookingRepo) GetActiveByVenueDateAndLabels(_ context.Context, _ int64, _ time.Time, _ []string) ([]*models.CourtBooking, error) {
	return r.labelEntries, r.getErr
}

// ── helpers ───────────────────────────────────────────────────────────────────

func gameWithVenueAndCourts(courts string, venueID int64) *models.Game {
	return &models.Game{
		ID:       10,
		Courts:   courts,
		GameDate: time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC),
		Venue:    &models.Venue{ID: venueID},
	}
}

func newCourtsGameSvc(
	gameRepo GameRepository,
	cbRepo CourtBookingRepository,
	bc BookingServiceClient,
	abrRepo AutoBookingResultRepository,
	credSvc *VenueCredentialService,
) *GameService {
	return &GameService{
		gameRepo:              gameRepo,
		participationRepo:     &stubParticipationRepo{},
		guestRepo:             &stubGuestParticipationRepo{},
		groupRepo:             &stubGroupRepoForDayAfter{},
		defaultLoc:            time.UTC,
		logger:                noopLogger(),
		courtBookingRepo:      cbRepo,
		bookingClient:         bc,
		autoBookingResultRepo: abrRepo,
		credService:           credSvc,
	}
}

// ── RemoveCourtsAndCancelBookings ─────────────────────────────────────────────

func TestRemoveCourtsAndCancelBookings_NoCourtsRemoved(t *testing.T) {
	game := gameWithVenueAndCourts("Court 1,Court 2", 1)
	gameRepo := &mockGameRepo{getByIDResult: game}
	bc := &mockBookingClient{}
	svc := newCourtsGameSvc(gameRepo, &mockCourtBookingRepo{}, bc, nil, nil)

	_, cancelErrs, err := svc.RemoveCourtsAndCancelBookings(context.Background(), game.ID, "Court 1,Court 2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cancelErrs) > 0 {
		t.Errorf("expected no cancel errors, got %v", cancelErrs)
	}
	if len(bc.cancelCalls) > 0 {
		t.Errorf("expected no CancelMatch, got %v", bc.cancelCalls)
	}
}

func TestRemoveCourtsAndCancelBookings_NoVenue(t *testing.T) {
	game := &models.Game{ID: 10, Courts: "Court 1,Court 2", GameDate: time.Now()}
	gameRepo := &mockGameRepo{getByIDResult: game}
	bc := &mockBookingClient{}
	svc := newCourtsGameSvc(gameRepo, &mockCourtBookingRepo{}, bc, nil, nil)

	_, _, err := svc.RemoveCourtsAndCancelBookings(context.Background(), game.ID, "Court 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bc.cancelCalls) > 0 {
		t.Errorf("expected no CancelMatch when no venue, got %v", bc.cancelCalls)
	}
}

func TestRemoveCourtsAndCancelBookings_NoBookingInfra(t *testing.T) {
	game := gameWithVenueAndCourts("Court 1,Court 2", 1)
	gameRepo := &mockGameRepo{getByIDResult: game}
	svc := newCourtsGameSvc(gameRepo, nil, nil, nil, nil)

	_, _, err := svc.RemoveCourtsAndCancelBookings(context.Background(), game.ID, "Court 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemoveCourtsAndCancelBookings_NoActiveBookings(t *testing.T) {
	game := gameWithVenueAndCourts("Court 1,Court 2", 1)
	gameRepo := &mockGameRepo{getByIDResult: game}
	cbRepo := &mockCourtBookingRepo{} // returns nil entries
	bc := &mockBookingClient{}
	svc := newCourtsGameSvc(gameRepo, cbRepo, bc, nil, nil)

	_, cancelErrs, err := svc.RemoveCourtsAndCancelBookings(context.Background(), game.ID, "Court 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cancelErrs) > 0 {
		t.Errorf("expected no cancel errors, got %v", cancelErrs)
	}
	if len(bc.cancelCalls) > 0 {
		t.Errorf("expected no CancelMatch when no bookings, got %v", bc.cancelCalls)
	}
}

func TestRemoveCourtsAndCancelBookings_HappyPath(t *testing.T) {
	credID := int64(1)
	game := gameWithVenueAndCourts("Court 1,Court 2", 1)
	gameRepo := &mockGameRepo{getByIDResult: game}
	cbRepo := &mockCourtBookingRepo{
		labelEntries: []*models.CourtBooking{
			{CourtLabel: "Court 2", MatchID: "match-uuid-2", CredentialID: &credID},
		},
	}
	bc := &mockBookingClient{}
	credSvc := makeAlgorithmCredService()
	svc := newCourtsGameSvc(gameRepo, cbRepo, bc, nil, credSvc)

	canceled, cancelErrs, err := svc.RemoveCourtsAndCancelBookings(context.Background(), game.ID, "Court 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cancelErrs) > 0 {
		t.Errorf("expected no cancel errors, got %v", cancelErrs)
	}
	if len(canceled) != 1 || canceled[0] != "Court 2" {
		t.Errorf("expected [Court 2] canceled, got %v", canceled)
	}
	if len(bc.cancelCalls) != 1 || bc.cancelCalls[0] != "match-uuid-2" {
		t.Errorf("expected CancelMatch(match-uuid-2), got %v", bc.cancelCalls)
	}
	if len(cbRepo.markedCanceled) != 1 || cbRepo.markedCanceled[0] != "match-uuid-2" {
		t.Errorf("expected MarkCanceled(match-uuid-2), got %v", cbRepo.markedCanceled)
	}
}

func TestRemoveCourtsAndCancelBookings_CancelFails_DBStillUpdated(t *testing.T) {
	credID := int64(1)
	game := gameWithVenueAndCourts("Court 1,Court 2", 1)
	gameRepo := &mockGameRepo{getByIDResult: game}
	cbRepo := &mockCourtBookingRepo{
		labelEntries: []*models.CourtBooking{
			{CourtLabel: "Court 2", MatchID: "match-uuid-2", CredentialID: &credID},
		},
	}
	bc := &mockBookingClient{cancelErr: errors.New("eversports 503")}
	credSvc := makeAlgorithmCredService()
	svc := newCourtsGameSvc(gameRepo, cbRepo, bc, nil, credSvc)

	canceled, cancelErrs, err := svc.RemoveCourtsAndCancelBookings(context.Background(), game.ID, "Court 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(canceled) > 0 {
		t.Errorf("expected no canceled labels on failure, got %v", canceled)
	}
	if len(cancelErrs) != 1 || cancelErrs[0].CourtLabel != "Court 2" {
		t.Errorf("expected 1 cancel error for Court 2, got %v", cancelErrs)
	}
	// UpdateCourts must be called even when cancellation fails.
	if gameRepo.updateCourtsArg == "" {
		t.Error("expected UpdateCourts to be called even on cancel failure")
	}
}

func TestRemoveCourtsAndCancelBookings_NoCredentials(t *testing.T) {
	// Entry has nil CredentialID — no credentials path.
	game := gameWithVenueAndCourts("Court 1,Court 2", 1)
	gameRepo := &mockGameRepo{getByIDResult: game}
	cbRepo := &mockCourtBookingRepo{
		labelEntries: []*models.CourtBooking{
			{CourtLabel: "Court 2", MatchID: "match-uuid-2", CredentialID: nil},
		},
	}
	bc := &mockBookingClient{}
	svc := newCourtsGameSvc(gameRepo, cbRepo, bc, nil, nil)

	_, cancelErrs, err := svc.RemoveCourtsAndCancelBookings(context.Background(), game.ID, "Court 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cancelErrs) != 1 || cancelErrs[0].CourtLabel != "Court 2" {
		t.Errorf("expected 1 cancel error for Court 2, got %v", cancelErrs)
	}
	if len(bc.cancelCalls) > 0 {
		t.Errorf("expected no CancelMatch when no credentials, got %v", bc.cancelCalls)
	}
}

func TestRemoveCourtsAndCancelBookings_MultisetDiff(t *testing.T) {
	// Game has "Court 1,Court 1,Court 2". New courts are "Court 1,Court 2".
	// Multiset diff: one "Court 1" is removed. The repo returns TWO active bookings for
	// "Court 1" (one per original slot). Only ONE CancelMatch should happen because the
	// quota for "Court 1" in the diff is 1 — the second booking must be left intact.
	credID := int64(1)
	game := &models.Game{
		ID:       10,
		Courts:   "Court 1,Court 1,Court 2",
		GameDate: time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC),
		Venue:    &models.Venue{ID: 1},
	}
	gameRepo := &mockGameRepo{getByIDResult: game}
	cbRepo := &mockCourtBookingRepo{
		labelEntries: []*models.CourtBooking{
			{CourtLabel: "Court 1", MatchID: "match-1a", CredentialID: &credID},
			{CourtLabel: "Court 1", MatchID: "match-1b", CredentialID: &credID},
		},
	}
	bc := &mockBookingClient{}
	credSvc := makeAlgorithmCredService()
	svc := newCourtsGameSvc(gameRepo, cbRepo, bc, nil, credSvc)

	canceled, cancelErrs, err := svc.RemoveCourtsAndCancelBookings(context.Background(), game.ID, "Court 1,Court 2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cancelErrs) > 0 {
		t.Errorf("unexpected cancel errors: %v", cancelErrs)
	}
	if len(canceled) != 1 || canceled[0] != "Court 1" {
		t.Errorf("expected exactly [Court 1] canceled, got %v", canceled)
	}
	if len(bc.cancelCalls) != 1 {
		t.Errorf("expected exactly 1 CancelMatch (quota=1), got %v", bc.cancelCalls)
	}
}

// ── activeBookingsByLabels routing ────────────────────────────────────────────

func TestActiveBookingsByLabels_NoResultRepo_UsesLabelQuery(t *testing.T) {
	game := gameWithVenueAndCourts("Court 1", 1)
	expected := []*models.CourtBooking{{CourtLabel: "Court 1", MatchID: "m1"}}
	cbRepo := &mockCourtBookingRepo{labelEntries: expected}
	svc := newCourtsGameSvc(&mockGameRepo{}, cbRepo, nil, nil, nil)

	got, err := svc.activeBookingsByLabels(context.Background(), game, []string{"Court 1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].MatchID != "m1" {
		t.Errorf("expected label-based result, got %v", got)
	}
}

func TestActiveBookingsByLabels_WithGameTime_FiltersLabel(t *testing.T) {
	// autoBookingResultRepo returns a result with game_time → uses GetByVenueAndDateAndTime
	// and filters to only the requested label.
	game := gameWithVenueAndCourts("Court 1,Court 2", 1)
	abrRepo := &mockAutoBookingResultRepo{
		gameIDResult: &models.AutoBookingResult{GameTime: "10:00"},
	}
	cbRepo := &mockCourtBookingRepo{
		timeEntries: []*models.CourtBooking{
			{CourtLabel: "Court 1", MatchID: "m1"},
			{CourtLabel: "Court 2", MatchID: "m2"},
		},
	}
	svc := newCourtsGameSvc(&mockGameRepo{}, cbRepo, nil, abrRepo, nil)

	// Request only Court 2 — must exclude Court 1 despite it being in timeEntries.
	got, err := svc.activeBookingsByLabels(context.Background(), game, []string{"Court 2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].MatchID != "m2" {
		t.Errorf("expected only Court 2 (m2), got %v", got)
	}
}

func TestActiveBookingsByLabels_NilAutoBookingResult_FallsBack(t *testing.T) {
	// autoBookingResultRepo.GetByGameID returns nil → fall back to label query.
	game := gameWithVenueAndCourts("Court 1", 1)
	expected := []*models.CourtBooking{{CourtLabel: "Court 1", MatchID: "m-fallback"}}
	abrRepo := &mockAutoBookingResultRepo{gameIDResult: nil}
	cbRepo := &mockCourtBookingRepo{labelEntries: expected}
	svc := newCourtsGameSvc(&mockGameRepo{}, cbRepo, nil, abrRepo, nil)

	got, err := svc.activeBookingsByLabels(context.Background(), game, []string{"Court 1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].MatchID != "m-fallback" {
		t.Errorf("expected fallback result, got %v", got)
	}
}

// ── ListActiveCourtBookings ───────────────────────────────────────────────────

func TestListActiveCourtBookings_EmptyCourts(t *testing.T) {
	svc := newCourtsGameSvc(&mockGameRepo{}, &mockCourtBookingRepo{}, nil, nil, nil)
	result, err := svc.ListActiveCourtBookings(context.Background(), 1, nil)
	if err != nil || result != nil {
		t.Errorf("expected (nil, nil) for empty courts, got (%v, %v)", result, err)
	}
}

func TestListActiveCourtBookings_NilRepo(t *testing.T) {
	svc := newCourtsGameSvc(&mockGameRepo{}, nil, nil, nil, nil)
	result, err := svc.ListActiveCourtBookings(context.Background(), 1, []string{"Court 1"})
	if err != nil || result != nil {
		t.Errorf("expected (nil, nil) when no repo, got (%v, %v)", result, err)
	}
}

func TestListActiveCourtBookings_GameNotFound(t *testing.T) {
	gameRepo := &mockGameRepo{getByIDErr: errors.New("not found")}
	svc := newCourtsGameSvc(gameRepo, &mockCourtBookingRepo{}, nil, nil, nil)
	_, err := svc.ListActiveCourtBookings(context.Background(), 99, []string{"Court 1"})
	if err == nil {
		t.Error("expected error when game not found")
	}
}

func TestListActiveCourtBookings_NoVenue(t *testing.T) {
	gameRepo := &mockGameRepo{getByIDResult: &models.Game{ID: 1}}
	svc := newCourtsGameSvc(gameRepo, &mockCourtBookingRepo{}, nil, nil, nil)
	result, err := svc.ListActiveCourtBookings(context.Background(), 1, []string{"Court 1"})
	if err != nil || result != nil {
		t.Errorf("expected (nil, nil) when no venue, got (%v, %v)", result, err)
	}
}

func TestListActiveCourtBookings_HappyPath(t *testing.T) {
	game := gameWithVenueAndCourts("Court 1,Court 2", 1)
	gameRepo := &mockGameRepo{getByIDResult: game}
	cbRepo := &mockCourtBookingRepo{
		labelEntries: []*models.CourtBooking{
			{CourtLabel: "Court 1", GameTime: "10:00", MatchID: "m1"},
		},
	}
	svc := newCourtsGameSvc(gameRepo, cbRepo, nil, nil, nil)

	result, err := svc.ListActiveCourtBookings(context.Background(), game.ID, []string{"Court 1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	r := result[0]
	if r.CourtLabel != "Court 1" || r.MatchID != "m1" || r.GameTime != "10:00" {
		t.Errorf("unexpected result fields: %+v", r)
	}
}

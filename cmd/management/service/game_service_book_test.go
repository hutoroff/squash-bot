package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/hutoroff/squash-bot/internal/models"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func gameWithAutoBookingVenue(gameTime time.Time) *models.Game {
	return &models.Game{
		ID:       55,
		ChatID:   -100,
		Courts:   "2",
		GameDate: gameTime,
		Venue: &models.Venue{
			ID:                 7,
			Name:               "Test Venue",
			AutoBookingEnabled: true,
			AutoBookingCourts:  "2",
			Courts:             "2",
			TimeSlots:          "18:00",
		},
	}
}

// minimalBookSvc builds a GameService configured for BookGameCourts unit tests.
func minimalBookSvc(
	gameRepo GameRepository,
	bc BookingServiceClient,
	credSvc *VenueCredentialService,
	cbRepo CourtBookingRepository,
	abrRepo AutoBookingResultRepository,
) *GameService {
	return &GameService{
		gameRepo:              gameRepo,
		participationRepo:     &stubParticipationRepo{},
		guestRepo:             &stubGuestParticipationRepo{},
		groupRepo:             &stubGroupRepoForDayAfter{},
		defaultLoc:            time.UTC,
		logger:                noopLogger(),
		bookingClient:         bc,
		credService:           credSvc,
		courtBookingRepo:      cbRepo,
		autoBookingResultRepo: abrRepo,
	}
}

// buildCredSvc creates a VenueCredentialService with one encrypted credential for venueID 7.
func buildCredSvc(t *testing.T) (*VenueCredentialService, *stubCredRepo) {
	t.Helper()
	enc, err := NewEncryptor("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	encPw, err := enc.Encrypt("password")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	credRepo := &stubCredRepo{creds: []*models.VenueCredential{
		{ID: 1, VenueID: 7, Login: "admin@test.com", EncryptedPassword: encPw, Priority: 1, MaxCourts: 3},
	}}
	svc := NewVenueCredentialService(credRepo, &stubVenueRepo{venue: &models.Venue{ID: 7}}, &mockCourtBookingRepo{}, enc)
	return svc, credRepo
}

// ── ErrGameNotFound cases ─────────────────────────────────────────────────────

func TestBookGameCourts_GameNotFound_NoRows(t *testing.T) {
	repo := &mockGameRepo{getByIDErr: fmt.Errorf("scan: %w", pgx.ErrNoRows)}
	svc := minimalBookSvc(repo, nil, nil, nil, nil)

	_, err := svc.BookGameCourts(context.Background(), 1, 1, 0, "", time.Hour)
	if !errors.Is(err, ErrGameNotFound) {
		t.Errorf("want ErrGameNotFound, got %v", err)
	}
}

func TestBookGameCourts_GameNotFound_DBError(t *testing.T) {
	repo := &mockGameRepo{getByIDErr: errors.New("connection refused")}
	svc := minimalBookSvc(repo, nil, nil, nil, nil)

	_, err := svc.BookGameCourts(context.Background(), 1, 1, 0, "", time.Hour)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrGameNotFound) {
		t.Error("transient DB error must not be reported as ErrGameNotFound")
	}
}

// ── ErrAutoBookingNotAvailable cases ─────────────────────────────────────────

func TestBookGameCourts_NoVenue(t *testing.T) {
	game := &models.Game{ID: 1, GameDate: time.Now()}
	svc := minimalBookSvc(&mockGameRepo{getByIDResult: game}, nil, nil, nil, nil)

	_, err := svc.BookGameCourts(context.Background(), 1, 1, 0, "", time.Hour)
	if !errors.Is(err, ErrAutoBookingNotAvailable) {
		t.Errorf("want ErrAutoBookingNotAvailable (no venue), got %v", err)
	}
}

func TestBookGameCourts_AutoBookingDisabled(t *testing.T) {
	game := &models.Game{
		ID:       1,
		GameDate: time.Now(),
		Venue:    &models.Venue{ID: 1, AutoBookingEnabled: false},
	}
	svc := minimalBookSvc(&mockGameRepo{getByIDResult: game}, nil, nil, nil, nil)

	_, err := svc.BookGameCourts(context.Background(), 1, 1, 0, "", time.Hour)
	if !errors.Is(err, ErrAutoBookingNotAvailable) {
		t.Errorf("want ErrAutoBookingNotAvailable (disabled), got %v", err)
	}
}

func TestBookGameCourts_NoBookingClient(t *testing.T) {
	game := gameWithAutoBookingVenue(time.Date(2026, 5, 10, 18, 0, 0, 0, time.UTC))
	// credSvc is non-nil but bookingClient is nil → should return ErrAutoBookingNotAvailable.
	credSvc, _ := buildCredSvc(t)
	svc := minimalBookSvc(&mockGameRepo{getByIDResult: game}, nil, credSvc, nil, nil)

	_, err := svc.BookGameCourts(context.Background(), game.ID, 1, 0, "", time.Hour)
	if !errors.Is(err, ErrAutoBookingNotAvailable) {
		t.Errorf("want ErrAutoBookingNotAvailable (no client), got %v", err)
	}
}

// ── Sanity check: 00:00 game time ─────────────────────────────────────────────

func TestBookGameCourts_GameTimeZero(t *testing.T) {
	game := gameWithAutoBookingVenue(time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC))
	credSvc, _ := buildCredSvc(t)
	svc := minimalBookSvc(&mockGameRepo{getByIDResult: game}, &mockBookingClient{}, credSvc, &mockCourtBookingRepo{}, nil)

	_, err := svc.BookGameCourts(context.Background(), game.ID, 1, 0, "", time.Hour)
	if err == nil {
		t.Fatal("expected error for 00:00 game time, got nil")
	}
	if errors.Is(err, ErrAutoBookingNotAvailable) {
		t.Error("00:00 sanity check should return a plain error, not ErrAutoBookingNotAvailable")
	}
}

// ── Successful booking ────────────────────────────────────────────────────────

// TestBookGameCourts_CourtsAppended verifies that booked court labels are appended
// to the existing game courts via UpdateCourts.
func TestBookGameCourts_CourtsAppended(t *testing.T) {
	game := gameWithAutoBookingVenue(time.Date(2026, 5, 10, 18, 0, 0, 0, time.UTC))
	credSvc, _ := buildCredSvc(t)

	bc := &mockBookingClient{
		courts: []BookingCourt{
			{ID: "77385", UUID: "uuid-court-2", Name: "Court 2"},
		},
		slots: []BookingSlot{}, // empty = no occupied courts
		bookResult: &BookMatchResult{
			MatchID:     "match-uuid-1",
			BookingUUID: "booking-uuid-1",
		},
	}

	gameRepo := &mockGameRepo{getByIDResult: game}
	abrRepo := &mockAutoBookingResultRepo{}

	svc := minimalBookSvc(gameRepo, bc, credSvc, &mockCourtBookingRepo{}, abrRepo)

	result, err := svc.BookGameCourts(context.Background(), game.ID, 1, 0, "", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Requested != 1 {
		t.Errorf("Requested: want 1, got %d", result.Requested)
	}
	if len(result.BookedLabels) != 1 {
		t.Errorf("expected 1 booked label, got %d: %v", len(result.BookedLabels), result.BookedLabels)
	}
	if gameRepo.updateCourtsArg == "" {
		t.Error("UpdateCourts was not called")
	}
}

// TestBookGameCourts_NoBookings_NoCourtsUpdate verifies that when all courts are
// occupied, UpdateCourts is NOT called.
func TestBookGameCourts_NoBookings_NoCourtsUpdate(t *testing.T) {
	game := gameWithAutoBookingVenue(time.Date(2026, 5, 10, 18, 0, 0, 0, time.UTC))
	credSvc, _ := buildCredSvc(t)

	// Court 2 occupied: ListMatches returns a slot whose Court ID matches "77385".
	bc := &mockBookingClient{
		courts: []BookingCourt{
			{ID: "77385", UUID: "uuid-court-2", Name: "Court 2"},
		},
		slots: []BookingSlot{
			{Court: 77385}, // Eversports numeric ID matches BookingCourt.ID "77385"
		},
	}

	gameRepo := &mockGameRepo{getByIDResult: game}
	svc := minimalBookSvc(gameRepo, bc, credSvc, &mockCourtBookingRepo{}, nil)

	result, err := svc.BookGameCourts(context.Background(), game.ID, 1, 0, "", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.BookedLabels) != 0 {
		t.Errorf("expected no booked labels, got %v", result.BookedLabels)
	}
	if gameRepo.updateCourtsArg != "" {
		t.Error("UpdateCourts should not be called when nothing was booked")
	}
}

// TestBookGameCourts_AutoBookingResultInserted verifies that a new auto_booking_results
// row is created when none exists yet for the venue+date+time slot.
func TestBookGameCourts_AutoBookingResultInserted(t *testing.T) {
	game := gameWithAutoBookingVenue(time.Date(2026, 5, 10, 18, 0, 0, 0, time.UTC))
	credSvc, _ := buildCredSvc(t)

	bc := &mockBookingClient{
		courts: []BookingCourt{
			{ID: "77385", UUID: "uuid-court-2", Name: "Court 2"},
		},
		slots: []BookingSlot{},
		bookResult: &BookMatchResult{
			MatchID: "match-uuid-1", BookingUUID: "booking-uuid-1",
		},
	}

	abrRepo := &mockAutoBookingResultRepo{} // GetByVenueAndDateAndTime returns nil → Save expected
	svc := minimalBookSvc(&mockGameRepo{getByIDResult: game}, bc, credSvc, &mockCourtBookingRepo{}, abrRepo)

	_, err := svc.BookGameCourts(context.Background(), game.ID, 1, 0, "", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if abrRepo.saveCalls != 1 {
		t.Errorf("expected 1 Save call, got %d", abrRepo.saveCalls)
	}
}

// TestBookGameCourts_AutoBookingResultNotDuplicated verifies that Save is NOT called
// when a row already exists for this venue+date+time slot.
func TestBookGameCourts_AutoBookingResultNotDuplicated(t *testing.T) {
	game := gameWithAutoBookingVenue(time.Date(2026, 5, 10, 18, 0, 0, 0, time.UTC))
	credSvc, _ := buildCredSvc(t)

	bc := &mockBookingClient{
		courts: []BookingCourt{
			{ID: "77385", UUID: "uuid-court-2", Name: "Court 2"},
		},
		slots: []BookingSlot{},
		bookResult: &BookMatchResult{
			MatchID: "match-uuid-1", BookingUUID: "booking-uuid-1",
		},
	}

	base := &mockAutoBookingResultRepo{}
	abrRepo := &mockAutoBookingResultRepoWithExisting{
		mockAutoBookingResultRepo: base,
		existing:                  &models.AutoBookingResult{ID: 99, VenueID: 7},
	}
	svc := minimalBookSvc(&mockGameRepo{getByIDResult: game}, bc, credSvc, &mockCourtBookingRepo{}, abrRepo)

	_, err := svc.BookGameCourts(context.Background(), game.ID, 1, 0, "", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if base.saveCalls != 0 {
		t.Errorf("Save should not be called when result already exists, got %d calls", base.saveCalls)
	}
}

// mockAutoBookingResultRepoWithExisting overrides GetByVenueAndDateAndTime to simulate
// a pre-existing auto_booking_results row for the same slot.
type mockAutoBookingResultRepoWithExisting struct {
	*mockAutoBookingResultRepo
	existing *models.AutoBookingResult
}

func (r *mockAutoBookingResultRepoWithExisting) GetByVenueAndDateAndTime(_ context.Context, _ int64, _ time.Time, _ string) (*models.AutoBookingResult, error) {
	return r.existing, nil
}

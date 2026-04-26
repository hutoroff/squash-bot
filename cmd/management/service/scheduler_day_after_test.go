package service

import (
	"context"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// TestProcessDayAfter_NilMessageID is a regression test for the panic that occurred
// when processDayAfter unconditionally dereferenced game.MessageID without a nil
// check. The function must return early without panicking when MessageID is nil.
// Passing a nil s.api proves that the Telegram API is never reached.
func TestProcessDayAfter_NilMessageID(t *testing.T) {
	// s.api is intentionally nil — if the nil-guard is missing the function would
	// panic on *game.MessageID before ever reaching the API, but having api=nil
	// ensures any accidental API call also panics, making the test self-validating.
	s := &DayAfterCleanupJob{logger: noopLogger()}

	game := &models.Game{
		ID:        42,
		ChatID:    -1001,
		MessageID: nil, // the problematic case
	}

	// Must not panic.
	s.processDayAfter(context.Background(), game, time.UTC)
}

// spyCourtBookingRepo records calls to MarkCanceledByVenueAndDate.
type spyCourtBookingRepo struct {
	stubCourtBookingRepo
	canceledVenueID  int64
	canceledGameDate time.Time
	cancelCalled     bool
}

func (r *spyCourtBookingRepo) MarkCanceledByVenueAndDate(_ context.Context, venueID int64, gameDate time.Time) error {
	r.cancelCalled = true
	r.canceledVenueID = venueID
	r.canceledGameDate = gameDate
	return nil
}

// TestProcessDayAfter_MarksCourtBookingsCanceled verifies that processDayAfter
// calls MarkCanceledByVenueAndDate when the game has a venue, closing out any
// active court_bookings rows that were not explicitly canceled during the session.
func TestProcessDayAfter_MarksCourtBookingsCanceled(t *testing.T) {
	spy := &spyCourtBookingRepo{}

	msgID := int64(99)
	venueID := int64(7)
	gameDate := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	game := &models.Game{
		ID:        42,
		ChatID:    -1001,
		MessageID: &msgID,
		VenueID:   &venueID,
		GameDate:  gameDate,
	}

	partRepo := &stubParticipationRepo{}
	guestRepo := &stubGuestParticipationRepo{}
	gameRepo := &stubGameRepoForDayAfter{}
	groupRepo := &stubGroupRepoForDayAfter{}
	api := &mockTelegramAPI{}

	s := &DayAfterCleanupJob{
		api:              api,
		gameRepo:         gameRepo,
		partRepo:         partRepo,
		guestRepo:        guestRepo,
		groupRepo:        groupRepo,
		courtBookingRepo: spy,
		logger:           noopLogger(),
	}

	s.processDayAfter(context.Background(), game, time.UTC)

	if !spy.cancelCalled {
		t.Error("MarkCanceledByVenueAndDate was not called")
	}
	if spy.canceledVenueID != venueID {
		t.Errorf("venueID: want %d, got %d", venueID, spy.canceledVenueID)
	}
	if !spy.canceledGameDate.Equal(gameDate) {
		t.Errorf("gameDate: want %v, got %v", gameDate, spy.canceledGameDate)
	}
}

// ── minimal stubs for processDayAfter ────────────────────────────────────────

type stubParticipationRepo struct{}

func (r *stubParticipationRepo) GetByGame(_ context.Context, _ int64) ([]*models.GameParticipation, error) {
	return nil, nil
}
func (r *stubParticipationRepo) Upsert(_ context.Context, _, _ int64, _ models.ParticipationStatus) error {
	return nil
}
func (r *stubParticipationRepo) DeleteByGameAndPlayer(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}
func (r *stubParticipationRepo) GetRegisteredCount(_ context.Context, _ int64) (int, error) {
	return 0, nil
}

type stubGuestParticipationRepo struct{}

func (r *stubGuestParticipationRepo) GetByGame(_ context.Context, _ int64) ([]*models.GuestParticipation, error) {
	return nil, nil
}
func (r *stubGuestParticipationRepo) AddGuest(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}
func (r *stubGuestParticipationRepo) RemoveLatestGuest(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}
func (r *stubGuestParticipationRepo) DeleteByID(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}
func (r *stubGuestParticipationRepo) GetCountByGame(_ context.Context, _ int64) (int, error) {
	return 0, nil
}

type stubGameRepoForDayAfter struct{}

func (r *stubGameRepoForDayAfter) Create(_ context.Context, _ *models.Game) (*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForDayAfter) GetByID(_ context.Context, id int64) (*models.Game, error) {
	return &models.Game{ID: id}, nil
}
func (r *stubGameRepoForDayAfter) UpdateMessageID(_ context.Context, _, _ int64) error { return nil }
func (r *stubGameRepoForDayAfter) GetUncompletedGamesByGroupAndDay(_ context.Context, _ int64, _, _ time.Time) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForDayAfter) GetUpcomingGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForDayAfter) GetUpcomingGamesByChatIDs(_ context.Context, _ []int64) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForDayAfter) UpdateCourts(_ context.Context, _ int64, _ string, _ int) error {
	return nil
}
func (r *stubGameRepoForDayAfter) GetNextGameForTelegramUser(_ context.Context, _ int64) (*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForDayAfter) GetGamesForPlayer(_ context.Context, _ int64) ([]models.PlayerGame, error) {
	return nil, nil
}
func (r *stubGameRepoForDayAfter) GetUpcomingUnnotifiedGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForDayAfter) MarkNotifiedDayBefore(_ context.Context, _ int64) error { return nil }
func (r *stubGameRepoForDayAfter) MarkCompleted(_ context.Context, _ int64) error         { return nil }

type stubGroupRepoForDayAfter struct{}

func (r *stubGroupRepoForDayAfter) GetAll(_ context.Context) ([]models.Group, error) {
	return nil, nil
}
func (r *stubGroupRepoForDayAfter) GetByID(_ context.Context, _ int64) (*models.Group, error) {
	return &models.Group{Language: "en", Timezone: "UTC"}, nil
}
func (r *stubGroupRepoForDayAfter) Upsert(_ context.Context, _ int64, _ string, _ bool) error {
	return nil
}
func (r *stubGroupRepoForDayAfter) SetTimezone(_ context.Context, _ int64, _ string) error {
	return nil
}
func (r *stubGroupRepoForDayAfter) SetLanguage(_ context.Context, _ int64, _ string) error {
	return nil
}
func (r *stubGroupRepoForDayAfter) SetChangelogEnabled(_ context.Context, _ int64, _ bool) error {
	return nil
}
func (r *stubGroupRepoForDayAfter) Remove(_ context.Context, _ int64) error { return nil }
func (r *stubGroupRepoForDayAfter) Exists(_ context.Context, _ int64) (bool, error) {
	return false, nil
}

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// ── stubGameRepoHCC ───────────────────────────────────────────────────────────

// stubGameRepoHCC is a minimal GameRepository for HalfwayCourtCheckJob tests.
type stubGameRepoHCC struct {
	games     []*models.Game
	queryErr  error
	markedIDs []int64
	markErr   error
}

func (r *stubGameRepoHCC) Create(_ context.Context, _ *models.Game) (*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoHCC) GetByID(_ context.Context, _ int64) (*models.Game, error) { return nil, nil }
func (r *stubGameRepoHCC) GetUpcomingGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoHCC) GetUpcomingGamesByChatIDs(_ context.Context, _ []int64) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoHCC) UpdateMessageID(_ context.Context, _, _ int64) error { return nil }
func (r *stubGameRepoHCC) UpdateCourts(_ context.Context, _ int64, _ string, _ int) error {
	return nil
}
func (r *stubGameRepoHCC) GetNextGameForTelegramUser(_ context.Context, _ int64) (*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoHCC) GetGamesForPlayer(_ context.Context, _ int64) ([]models.PlayerGame, error) {
	return nil, nil
}
func (r *stubGameRepoHCC) GetRecentCompletedGamesForPlayer(_ context.Context, _, _ int64, _ int) ([]models.PlayerGame, error) {
	return nil, nil
}
func (r *stubGameRepoHCC) GameInResultWindow(_ context.Context, _ int64, _ int) (bool, error) {
	return true, nil
}
func (r *stubGameRepoHCC) GetUpcomingUnnotifiedGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoHCC) GetUncompletedGamesByGroupAndDay(_ context.Context, _ int64, _, _ time.Time) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoHCC) GetCompletedGamesByGroupAndDay(_ context.Context, _ int64, _, _ time.Time) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoHCC) MarkNotifiedDayBefore(_ context.Context, _ int64) error { return nil }
func (r *stubGameRepoHCC) MarkCompleted(_ context.Context, _ int64) error         { return nil }
func (r *stubGameRepoHCC) ListGroupIDsForPlayer(_ context.Context, _ int64) ([]int64, error) {
	return nil, nil
}
func (r *stubGameRepoHCC) GetUpcomingGamesForFinalCheck(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoHCC) MarkFinalCourtCheckDone(_ context.Context, _ int64) error { return nil }
func (r *stubGameRepoHCC) GetUpcomingGamesForHalfwayCheck(_ context.Context) ([]*models.Game, error) {
	return r.games, r.queryErr
}
func (r *stubGameRepoHCC) MarkHalfwayCourtCheckDone(_ context.Context, gameID int64) error {
	r.markedIDs = append(r.markedIDs, gameID)
	return r.markErr
}
func (r *stubGameRepoHCC) PlayerCanAccessGame(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}

// ── mockHalfwayCanceler ────────────────────────────────────────────────────────

type mockHalfwayCanceler struct {
	entries     []*models.CourtBooking
	entriesErr  error
	result      *courtCancellationResult
	err         error
	cancelCalls int
	loadCalls   int
}

func (m *mockHalfwayCanceler) cancelUnusedCourts(_ context.Context, _ *models.Game, _ int, _ *time.Location) (*courtCancellationResult, error) {
	m.cancelCalls++
	return m.result, m.err
}

func (m *mockHalfwayCanceler) loadCourtBookingEntries(_ context.Context, _ *models.Game) ([]*models.CourtBooking, error) {
	m.loadCalls++
	return m.entries, m.entriesErr
}

// ── helpers ───────────────────────────────────────────────────────────────────

// makeHCCJob builds a HalfwayCourtCheckJob wired with the provided stubs.
func makeHCCJob(
	api TelegramAPI,
	gameRepo GameRepository,
	partCount, guestCount int,
	group *models.Group,
	notifier Notifier,
	canceler halfwayCourtCanceler,
) *HalfwayCourtCheckJob {
	return &HalfwayCourtCheckJob{
		api:       api,
		gameRepo:  gameRepo,
		partRepo:  &stubPartRepoPC{count: partCount},
		guestRepo: &stubGuestRepoPC{count: guestCount},
		groupRepo: &stubGroupRepoPC{group: group},
		notifier:  notifier,
		canceler:  canceler,
		loc:       time.UTC,
		logger:    noopLogger(),
	}
}

// ── processHalfwayCheck tests ──────────────────────────────────────────────────

// TestHalfwayCheck_UnneededOdd_CancelsFloor verifies the example from the plan:
// 4 courts booked, 2 players → capacity 8, unneeded 3 → cancel floor(3/2)=1.
func TestHalfwayCheck_UnneededOdd_CancelsFloor(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	gameRepo := &stubGameRepoHCC{}
	api := &adminCaptureSendAPI{}
	notifier := &spyNotifier{}
	canceler := &mockHalfwayCanceler{
		result: &courtCancellationResult{
			canceledCourts: []int{4},
			remainingCount: 3,
		},
	}

	game := makeGame("1,2,3,4", 4, time.Now().Add(24*time.Hour))
	game.ID = 30
	game.ChatID = 100

	job := makeHCCJob(api, gameRepo, 2, 0, group, notifier, canceler)
	job.processHalfwayCheck(context.Background(), game)

	if canceler.cancelCalls != 1 {
		t.Fatalf("expected cancelUnusedCourts called once, got %d", canceler.cancelCalls)
	}
	if len(notifier.calledGameIDs) != 1 || notifier.calledGameIDs[0] != 30 {
		t.Errorf("expected EditGameMessage called for game 30, got %v", notifier.calledGameIDs)
	}
	if len(api.msgs) != 1 {
		t.Fatalf("expected 1 group message sent, got %d", len(api.msgs))
	}
	if len(gameRepo.markedIDs) != 1 || gameRepo.markedIDs[0] != 30 {
		t.Errorf("expected game 30 marked done, got %v", gameRepo.markedIDs)
	}
}

// TestHalfwayCheck_UnneededOne_NoCancelMarksDone verifies floor(1/2)=0: the job
// marks done and sends nothing, without ever calling the canceler.
func TestHalfwayCheck_UnneededOne_NoCancelMarksDone(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	gameRepo := &stubGameRepoHCC{}
	api := &adminCaptureSendAPI{}
	canceler := &mockHalfwayCanceler{}

	// 2 courts, 2 players → capacity 4, unneeded (4-2)/2=1 → cancel floor(1/2)=0.
	game := makeGame("1,2", 2, time.Now().Add(24*time.Hour))
	game.ID = 31

	job := makeHCCJob(api, gameRepo, 2, 0, group, nil, canceler)
	job.processHalfwayCheck(context.Background(), game)

	if canceler.cancelCalls != 0 {
		t.Errorf("expected cancelUnusedCourts NOT called, got %d calls", canceler.cancelCalls)
	}
	if len(api.msgs) != 0 {
		t.Errorf("expected no message sent, got %d", len(api.msgs))
	}
	if len(gameRepo.markedIDs) != 1 || gameRepo.markedIDs[0] != 31 {
		t.Errorf("expected game 31 marked done, got %v", gameRepo.markedIDs)
	}
}

// TestHalfwayCheck_CancelErrors_NoMessage verifies that when nothing was
// actually canceled (all attempts failed), no group message is sent but the
// game is still marked done and admins are DM'd.
func TestHalfwayCheck_CancelErrors_NoMessage(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	gameRepo := &stubGameRepoHCC{}
	api := &adminCaptureSendAPI{}
	canceler := &mockHalfwayCanceler{
		result: &courtCancellationResult{
			canceledCourts: []int{},
			cancelErrors:   []error{errors.New("eversports: forbidden")},
			remainingCount: 4,
		},
	}

	game := makeGame("1,2,3,4", 4, time.Now().Add(24*time.Hour))
	game.ID = 32
	game.ChatID = 100

	job := makeHCCJob(api, gameRepo, 2, 0, group, nil, canceler)
	job.processHalfwayCheck(context.Background(), game)

	if !api.getAdminsCalled {
		t.Error("expected GetChatAdministrators called for admin error DM")
	}
	if len(api.msgs) != 0 {
		t.Errorf("expected no group chat message when nothing canceled, got %d", len(api.msgs))
	}
	if len(gameRepo.markedIDs) != 1 || gameRepo.markedIDs[0] != 32 {
		t.Errorf("expected game 32 marked done, got %v", gameRepo.markedIDs)
	}
}

// ── runHalfwayCourtCheck gating tests ──────────────────────────────────────────

// TestHalfwayCheck_NoEntries_SkipsWithoutMarking verifies that a game with no
// active court_bookings entries is skipped (auto-booking may not have run yet)
// without marking it done, and without ever calling cancelUnusedCourts.
func TestHalfwayCheck_NoEntries_SkipsWithoutMarking(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	game := &models.Game{
		ID:          40,
		ChatID:      100,
		MessageID:   int64Ptr(555),
		GameDate:    time.Now().Add(48 * time.Hour),
		Courts:      "1,2",
		CourtsCount: 2,
		Venue:       &models.Venue{ID: 1, GracePeriodHours: 24},
	}
	gameRepo := &stubGameRepoHCC{games: []*models.Game{game}}
	api := &adminCaptureSendAPI{}
	canceler := &mockHalfwayCanceler{entries: nil}

	job := &HalfwayCourtCheckJob{
		api:       api,
		gameRepo:  gameRepo,
		partRepo:  &stubPartRepoPC{count: 0},
		guestRepo: &stubGuestRepoPC{count: 0},
		groupRepo: &stubGroupRepoPC{group: group},
		canceler:  canceler,
		loc:       time.UTC,
		logger:    noopLogger(),
	}
	job.runHalfwayCourtCheck(true)

	if canceler.loadCalls != 1 {
		t.Errorf("expected loadCourtBookingEntries called once, got %d", canceler.loadCalls)
	}
	if canceler.cancelCalls != 0 {
		t.Errorf("expected cancelUnusedCourts NOT called, got %d", canceler.cancelCalls)
	}
	if len(gameRepo.markedIDs) != 0 {
		t.Errorf("expected game NOT marked done (no entries yet), got %v", gameRepo.markedIDs)
	}
}

// TestHalfwayCheck_PastDeadline_MarksDoneWithoutLoadingEntries verifies that a
// game whose deadline (game_date - grace) has already passed is marked done
// immediately, ceding cleanup to the later jobs, without querying bookings.
func TestHalfwayCheck_PastDeadline_MarksDoneWithoutLoadingEntries(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	// gameDate = now + 1h, grace = 24h → deadline = now - 23h, already passed.
	game := &models.Game{
		ID:          41,
		ChatID:      100,
		MessageID:   int64Ptr(555),
		GameDate:    time.Now().Add(1 * time.Hour),
		Courts:      "1,2",
		CourtsCount: 2,
		Venue:       &models.Venue{ID: 1, GracePeriodHours: 24},
	}
	gameRepo := &stubGameRepoHCC{games: []*models.Game{game}}
	api := &adminCaptureSendAPI{}
	canceler := &mockHalfwayCanceler{}

	job := &HalfwayCourtCheckJob{
		api:       api,
		gameRepo:  gameRepo,
		partRepo:  &stubPartRepoPC{count: 0},
		guestRepo: &stubGuestRepoPC{count: 0},
		groupRepo: &stubGroupRepoPC{group: group},
		canceler:  canceler,
		loc:       time.UTC,
		logger:    noopLogger(),
	}
	job.runHalfwayCourtCheck(false)

	if canceler.loadCalls != 0 {
		t.Errorf("expected loadCourtBookingEntries NOT called past the deadline, got %d", canceler.loadCalls)
	}
	if len(gameRepo.markedIDs) != 1 || gameRepo.markedIDs[0] != 41 {
		t.Errorf("expected game 41 marked done (past deadline), got %v", gameRepo.markedIDs)
	}
}

// TestHalfwayCheck_MidpointGate_InWindow verifies that a game is processed
// once now has reached the computed midpoint between the earliest
// court_bookings.created_at and the grace-period deadline.
func TestHalfwayCheck_MidpointGate_InWindow(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	now := time.Now()
	// deadline = now + 1h, bookedAt = now - 1h → halfwayAt = now, already reached.
	gameDate := now.Add(1*time.Hour + 24*time.Hour)
	game := &models.Game{
		ID:          42,
		ChatID:      100,
		MessageID:   int64Ptr(555),
		GameDate:    gameDate,
		Courts:      "1,2",
		CourtsCount: 2,
		Venue:       &models.Venue{ID: 1, GracePeriodHours: 24},
	}
	gameRepo := &stubGameRepoHCC{games: []*models.Game{game}}
	api := &adminCaptureSendAPI{}
	canceler := &mockHalfwayCanceler{
		entries: []*models.CourtBooking{
			{CourtLabel: "1", MatchID: "m1", CreatedAt: now.Add(-1 * time.Hour)},
		},
		result: &courtCancellationResult{},
	}

	job := &HalfwayCourtCheckJob{
		api:       api,
		gameRepo:  gameRepo,
		partRepo:  &stubPartRepoPC{count: 0}, // capacity 4, count 0 → unneeded 2 → cancel 1
		guestRepo: &stubGuestRepoPC{count: 0},
		groupRepo: &stubGroupRepoPC{group: group},
		canceler:  canceler,
		loc:       time.UTC,
		logger:    noopLogger(),
	}
	job.runHalfwayCourtCheck(false)

	if canceler.cancelCalls != 1 {
		t.Errorf("expected cancelUnusedCourts called once (in window), got %d", canceler.cancelCalls)
	}
}

// TestHalfwayCheck_MidpointGate_OutOfWindow verifies that a game whose midpoint
// is still ahead is skipped without marking done or calling the canceler.
func TestHalfwayCheck_MidpointGate_OutOfWindow(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	now := time.Now()
	gameDate := now.Add(10*time.Hour + 24*time.Hour)
	game := &models.Game{
		ID:          43,
		ChatID:      100,
		MessageID:   int64Ptr(555),
		GameDate:    gameDate,
		Courts:      "1,2",
		CourtsCount: 2,
		Venue:       &models.Venue{ID: 1, GracePeriodHours: 24},
	}
	gameRepo := &stubGameRepoHCC{games: []*models.Game{game}}
	api := &adminCaptureSendAPI{}
	canceler := &mockHalfwayCanceler{
		entries: []*models.CourtBooking{
			// bookedAt = now - 4h → deadline (now+10h) - bookedAt (now-4h) = 14h; halfway = bookedAt + 7h = now+3h.
			{CourtLabel: "1", MatchID: "m1", CreatedAt: now.Add(-4 * time.Hour)},
		},
	}

	job := &HalfwayCourtCheckJob{
		api:       api,
		gameRepo:  gameRepo,
		partRepo:  &stubPartRepoPC{count: 0},
		guestRepo: &stubGuestRepoPC{count: 0},
		groupRepo: &stubGroupRepoPC{group: group},
		canceler:  canceler,
		loc:       time.UTC,
		logger:    noopLogger(),
	}
	job.runHalfwayCourtCheck(false)

	if canceler.cancelCalls != 0 {
		t.Errorf("expected cancelUnusedCourts NOT called (out of window), got %d", canceler.cancelCalls)
	}
	if len(gameRepo.markedIDs) != 0 {
		t.Errorf("expected game NOT marked done (out of window), got %v", gameRepo.markedIDs)
	}
}

// TestHalfwayCheck_Unpublished_SkipsWithoutMarking verifies that a game whose
// announcement has not been posted yet (message_id NULL) is skipped: nobody
// could have joined, so acting on a zero player count would cancel courts
// prematurely and send an unthreaded notification.
func TestHalfwayCheck_Unpublished_SkipsWithoutMarking(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	now := time.Now()
	game := &models.Game{
		ID:          45,
		ChatID:      100,
		GameDate:    now.Add(1*time.Hour + 24*time.Hour),
		Courts:      "1,2",
		CourtsCount: 2,
		Venue:       &models.Venue{ID: 1, GracePeriodHours: 24},
	}
	gameRepo := &stubGameRepoHCC{games: []*models.Game{game}}
	canceler := &mockHalfwayCanceler{
		entries: []*models.CourtBooking{
			{CourtLabel: "1", MatchID: "m1", CreatedAt: now.Add(-1 * time.Hour)},
		},
	}

	job := &HalfwayCourtCheckJob{
		api:       &adminCaptureSendAPI{},
		gameRepo:  gameRepo,
		partRepo:  &stubPartRepoPC{count: 0},
		guestRepo: &stubGuestRepoPC{count: 0},
		groupRepo: &stubGroupRepoPC{group: group},
		canceler:  canceler,
		loc:       time.UTC,
		logger:    noopLogger(),
	}
	job.runHalfwayCourtCheck(false)

	if canceler.cancelCalls != 0 {
		t.Errorf("expected cancelUnusedCourts NOT called for unpublished game, got %d", canceler.cancelCalls)
	}
	if len(gameRepo.markedIDs) != 0 {
		t.Errorf("expected game NOT marked done (retry after publication), got %v", gameRepo.markedIDs)
	}
}

// TestHalfwayCheck_PastMidpoint_CatchesUp verifies that a game whose midpoint
// passed long ago — e.g. it was published only after the midpoint — is still
// processed on the next poll instead of being skipped forever.
func TestHalfwayCheck_PastMidpoint_CatchesUp(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	now := time.Now()
	// deadline = now + 1h, bookedAt = now - 9h → halfwayAt = now - 4h, long past.
	game := &models.Game{
		ID:          46,
		ChatID:      100,
		MessageID:   int64Ptr(555),
		GameDate:    now.Add(1*time.Hour + 24*time.Hour),
		Courts:      "1,2",
		CourtsCount: 2,
		Venue:       &models.Venue{ID: 1, GracePeriodHours: 24},
	}
	gameRepo := &stubGameRepoHCC{games: []*models.Game{game}}
	canceler := &mockHalfwayCanceler{
		entries: []*models.CourtBooking{
			{CourtLabel: "1", MatchID: "m1", CreatedAt: now.Add(-9 * time.Hour)},
		},
		result: &courtCancellationResult{},
	}

	job := &HalfwayCourtCheckJob{
		api:       &adminCaptureSendAPI{},
		gameRepo:  gameRepo,
		partRepo:  &stubPartRepoPC{count: 0}, // capacity 4, count 0 → cancel 1
		guestRepo: &stubGuestRepoPC{count: 0},
		groupRepo: &stubGroupRepoPC{group: group},
		canceler:  canceler,
		loc:       time.UTC,
		logger:    noopLogger(),
	}
	job.runHalfwayCourtCheck(false)

	if canceler.cancelCalls != 1 {
		t.Errorf("expected cancelUnusedCourts called once (catch-up), got %d", canceler.cancelCalls)
	}
}

// TestHalfwayCheck_Force_IgnoresWindow verifies that force=true processes a
// game regardless of the midpoint timing gate.
func TestHalfwayCheck_Force_IgnoresWindow(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	now := time.Now()
	gameDate := now.Add(10*time.Hour + 24*time.Hour)
	game := &models.Game{
		ID:          44,
		ChatID:      100,
		MessageID:   int64Ptr(555),
		GameDate:    gameDate,
		Courts:      "1",
		CourtsCount: 1,
		Venue:       &models.Venue{ID: 1, GracePeriodHours: 24},
	}
	gameRepo := &stubGameRepoHCC{games: []*models.Game{game}}
	api := &adminCaptureSendAPI{}
	canceler := &mockHalfwayCanceler{
		entries: []*models.CourtBooking{
			{CourtLabel: "1", MatchID: "m1", CreatedAt: now.Add(-4 * time.Hour)},
		},
	}

	job := &HalfwayCourtCheckJob{
		api:       api,
		gameRepo:  gameRepo,
		partRepo:  &stubPartRepoPC{count: 2}, // at capacity for 1 court → unneeded=0 → cancel=0
		guestRepo: &stubGuestRepoPC{count: 0},
		groupRepo: &stubGroupRepoPC{group: group},
		canceler:  canceler,
		loc:       time.UTC,
		logger:    noopLogger(),
	}
	job.runHalfwayCourtCheck(true)

	if len(gameRepo.markedIDs) != 1 || gameRepo.markedIDs[0] != 44 {
		t.Errorf("expected game 44 marked done (force=true), got %v", gameRepo.markedIDs)
	}
}

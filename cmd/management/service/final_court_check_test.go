package service

import (
	"context"
	"errors"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/models"
)

// ── stubGameRepoFCC ───────────────────────────────────────────────────────────

// stubGameRepoFCC is a minimal GameRepository for FinalCourtCheckJob tests.
type stubGameRepoFCC struct {
	games     []*models.Game
	queryErr  error
	markedIDs []int64
	markErr   error
}

func (r *stubGameRepoFCC) Create(_ context.Context, _ *models.Game) (*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoFCC) GetByID(_ context.Context, _ int64) (*models.Game, error) { return nil, nil }
func (r *stubGameRepoFCC) GetUpcomingGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoFCC) GetUpcomingGamesByChatIDs(_ context.Context, _ []int64) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoFCC) UpdateMessageID(_ context.Context, _, _ int64) error { return nil }
func (r *stubGameRepoFCC) UpdateCourts(_ context.Context, _ int64, _ string, _ int) error {
	return nil
}
func (r *stubGameRepoFCC) GetNextGameForUser(_ context.Context, _ int64) (*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoFCC) GetGamesForPlayer(_ context.Context, _ int64) ([]models.PlayerGame, error) {
	return nil, nil
}
func (r *stubGameRepoFCC) GetRecentCompletedGamesForPlayer(_ context.Context, _, _ int64, _ int) ([]models.PlayerGame, error) {
	return nil, nil
}
func (r *stubGameRepoFCC) GameInResultWindow(_ context.Context, _ int64, _ int) (bool, error) {
	return true, nil
}
func (r *stubGameRepoFCC) GetUpcomingUnnotifiedGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoFCC) GetUncompletedGamesByGroupAndDay(_ context.Context, _ int64, _, _ time.Time) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoFCC) GetCompletedGamesByGroupAndDay(_ context.Context, _ int64, _, _ time.Time) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoFCC) MarkNotifiedDayBefore(_ context.Context, _ int64) error { return nil }
func (r *stubGameRepoFCC) MarkCompleted(_ context.Context, _ int64) error         { return nil }
func (r *stubGameRepoFCC) ListGroupIDsForPlayer(_ context.Context, _ int64) ([]int64, error) {
	return nil, nil
}
func (r *stubGameRepoFCC) GetUpcomingGamesForFinalCheck(_ context.Context) ([]*models.Game, error) {
	return r.games, r.queryErr
}
func (r *stubGameRepoFCC) MarkFinalCourtCheckDone(_ context.Context, gameID int64) error {
	r.markedIDs = append(r.markedIDs, gameID)
	return r.markErr
}
func (r *stubGameRepoFCC) GetUpcomingGamesForHalfwayCheck(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoFCC) MarkHalfwayCourtCheckDone(_ context.Context, _ int64) error { return nil }
func (r *stubGameRepoFCC) PlayerCanAccessGame(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}

// ── mockCanceler ──────────────────────────────────────────────────────────────

type mockCanceler struct {
	result *courtCancellationResult
	err    error
	calls  int
}

func (m *mockCanceler) cancelUnusedCourts(_ context.Context, _ *models.Game, _ int, _ *time.Location) (*courtCancellationResult, error) {
	m.calls++
	return m.result, m.err
}

// ── adminCaptureSendAPI ───────────────────────────────────────────────────────

// adminCaptureSendAPI records all sent messages and returns a configurable
// admin list for GetChatAdministrators calls.
type adminCaptureSendAPI struct {
	msgs                []tgbotapi.MessageConfig
	admins              []tgbotapi.ChatMember
	getAdminsCalled     bool
	getAdminsCallChatID int64
}

func (a *adminCaptureSendAPI) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	if msg, ok := c.(tgbotapi.MessageConfig); ok {
		a.msgs = append(a.msgs, msg)
	}
	return tgbotapi.Message{}, nil
}
func (a *adminCaptureSendAPI) Request(_ tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{Ok: true}, nil
}
func (a *adminCaptureSendAPI) GetChatAdministrators(cfg tgbotapi.ChatAdministratorsConfig) ([]tgbotapi.ChatMember, error) {
	a.getAdminsCalled = true
	a.getAdminsCallChatID = cfg.ChatID
	return a.admins, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// makeFCCJob builds a FinalCourtCheckJob wired with the provided stubs.
func makeFCCJob(
	api TelegramAPI,
	gameRepo GameRepository,
	partCount, guestCount int,
	group *models.Group,
	notifier Notifier,
	canceler unusedCourtCanceler,
) *FinalCourtCheckJob {
	return &FinalCourtCheckJob{
		api:        api,
		gameRepo:   gameRepo,
		partRepo:   &stubPartRepoPC{count: partCount},
		guestRepo:  &stubGuestRepoPC{count: guestCount},
		groupRepo:  &stubGroupRepoPC{group: group},
		notifier:   notifier,
		canceler:   canceler,
		loc:        time.UTC,
		logger:     noopLogger(),
		pollWindow: 5 * time.Minute,
	}
}

// ── processFinalCheck tests ───────────────────────────────────────────────────

// TestFinalCheck_NoExcess_MarksAndSilent verifies that when there are enough
// players for all courts, processFinalCheck marks done without canceling or
// sending any message.
func TestFinalCheck_NoExcess_MarksAndSilent(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	gameRepo := &stubGameRepoFCC{}
	api := &adminCaptureSendAPI{}
	canceler := &mockCanceler{}

	// 4 players, 2 courts → capacity=4, courtsToCancel=0
	game := makeGame("1,2", 2, time.Now().Add(24*time.Hour))
	game.ID = 10

	job := makeFCCJob(api, gameRepo, 4, 0, group, nil, canceler)
	job.processFinalCheck(context.Background(), game)

	if canceler.calls != 0 {
		t.Errorf("expected cancelUnusedCourts NOT called, got %d calls", canceler.calls)
	}
	if len(api.msgs) != 0 {
		t.Errorf("expected no message sent, got %d", len(api.msgs))
	}
	if len(gameRepo.markedIDs) != 1 || gameRepo.markedIDs[0] != 10 {
		t.Errorf("expected game 10 marked done, got %v", gameRepo.markedIDs)
	}
}

// TestFinalCheck_Excess_CancelsAndSendsMessage verifies that when a court can
// be freed, processFinalCheck calls cancelUnusedCourts, edits the game message,
// sends a chat notification, and marks the game done.
func TestFinalCheck_Excess_CancelsAndSendsMessage(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	gameRepo := &stubGameRepoFCC{}
	api := &adminCaptureSendAPI{}
	notifier := &spyNotifier{}
	canceler := &mockCanceler{
		result: &courtCancellationResult{
			canceledCourts:  []int{2},
			remainingCourts: "1",
			remainingCount:  1,
		},
	}

	// 2 players, 2 courts → capacity=4, courtsToCancel=1
	game := makeGame("1,2", 2, time.Now().Add(24*time.Hour))
	game.ID = 11
	game.ChatID = 100

	job := makeFCCJob(api, gameRepo, 2, 0, group, notifier, canceler)
	job.processFinalCheck(context.Background(), game)

	if canceler.calls != 1 {
		t.Errorf("expected cancelUnusedCourts called once, got %d", canceler.calls)
	}
	if len(notifier.calledGameIDs) != 1 || notifier.calledGameIDs[0] != 11 {
		t.Errorf("expected EditGameMessage called for game 11, got %v", notifier.calledGameIDs)
	}
	if len(api.msgs) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(api.msgs))
	}
	if api.msgs[0].ChatID != 100 {
		t.Errorf("message ChatID: got %d, want 100", api.msgs[0].ChatID)
	}
	if len(gameRepo.markedIDs) != 1 || gameRepo.markedIDs[0] != 11 {
		t.Errorf("expected game 11 marked done, got %v", gameRepo.markedIDs)
	}
}

// TestFinalCheck_Excess_ReplyToMessageID verifies that when game.MessageID is
// set, the notification is sent as a reply to the pinned announcement.
func TestFinalCheck_Excess_ReplyToMessageID(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	gameRepo := &stubGameRepoFCC{}
	api := &adminCaptureSendAPI{}
	canceler := &mockCanceler{
		result: &courtCancellationResult{
			canceledCourts: []int{2},
			remainingCount: 1,
		},
	}

	msgID := int64(77)
	game := makeGame("1,2", 2, time.Now().Add(24*time.Hour))
	game.ID = 12
	game.MessageID = &msgID

	job := makeFCCJob(api, gameRepo, 2, 0, group, nil, canceler)
	job.processFinalCheck(context.Background(), game)

	if len(api.msgs) == 0 {
		t.Fatal("expected at least one message sent")
	}
	if api.msgs[0].ReplyToMessageID != 77 {
		t.Errorf("ReplyToMessageID: got %d, want 77", api.msgs[0].ReplyToMessageID)
	}
}

// TestFinalCheck_CancelErrors_AdminDMAttempted verifies that when cancelUnusedCourts
// returns cancelErrors but no canceledCourts (all attempts failed), the job
// tries to notify admins (GetChatAdministrators called), sends no group chat
// message, and still marks the game done.
func TestFinalCheck_CancelErrors_AdminDMAttempted(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	gameRepo := &stubGameRepoFCC{}
	api := &adminCaptureSendAPI{}
	canceler := &mockCanceler{
		result: &courtCancellationResult{
			canceledCourts: []int{},
			cancelErrors:   []error{errors.New("eversports: forbidden")},
			remainingCount: 2,
		},
	}

	// 0 players, 2 courts → courtsToCancel=2; all cancel attempts fail
	game := makeGame("1,2", 2, time.Now().Add(24*time.Hour))
	game.ID = 13
	game.ChatID = 100

	job := makeFCCJob(api, gameRepo, 0, 0, group, nil, canceler)
	job.processFinalCheck(context.Background(), game)

	if !api.getAdminsCalled {
		t.Error("expected GetChatAdministrators called for admin error DM, got no call")
	}
	if api.getAdminsCallChatID != 100 {
		t.Errorf("GetChatAdministrators called with chatID %d, want 100", api.getAdminsCallChatID)
	}
	// No group chat message because nothing was actually canceled.
	if len(api.msgs) != 0 {
		t.Errorf("expected no group chat message when no courts canceled, got %d", len(api.msgs))
	}
	if len(gameRepo.markedIDs) != 1 || gameRepo.markedIDs[0] != 13 {
		t.Errorf("expected game 13 marked done, got %v", gameRepo.markedIDs)
	}
}

// TestFinalCheck_CancelError_AdminDMAndMarkDone verifies that when
// cancelUnusedCourts returns any non-nil error (structural "no court_bookings"
// or "persist updated courts" after physical cancel), the job DMs admins,
// sends no group chat message, and still marks the game done.
func TestFinalCheck_CancelError_AdminDMAndMarkDone(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	gameRepo := &stubGameRepoFCC{}
	api := &adminCaptureSendAPI{}
	canceler := &mockCanceler{err: errors.New("no court_bookings records")}

	// 0 players, 2 courts → courtsToCancel=2; error from canceler
	game := makeGame("1,2", 2, time.Now().Add(24*time.Hour))
	game.ID = 14
	game.ChatID = 100

	job := makeFCCJob(api, gameRepo, 0, 0, group, nil, canceler)
	job.processFinalCheck(context.Background(), game)

	if !api.getAdminsCalled {
		t.Error("expected GetChatAdministrators called on canceler error for admin DM")
	}
	// No group chat message — nothing was confirmed canceled.
	if len(api.msgs) != 0 {
		t.Errorf("expected no group chat message on canceler error, got %d", len(api.msgs))
	}
	if len(gameRepo.markedIDs) != 1 || gameRepo.markedIDs[0] != 14 {
		t.Errorf("expected game 14 marked done, got %v", gameRepo.markedIDs)
	}
}

// TestFinalCheck_GateExcludesOutOfWindowGame verifies that runFinalCourtCheck
// skips games whose finalCheckAt is far outside the pollWindow.
func TestFinalCheck_GateExcludesOutOfWindowGame(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}

	// Game with grace=24h; finalCheckAt = gameDate - 24h - 15m.
	// gameDate = now + 2h → finalCheckAt ≈ now - 22h 15m → well outside ±5m window.
	gameDate := time.Now().Add(2 * time.Hour)
	game := &models.Game{
		ID:          20,
		ChatID:      100,
		GameDate:    gameDate,
		Courts:      "1,2",
		CourtsCount: 2,
		Venue:       &models.Venue{GracePeriodHours: 24},
	}

	gameRepo := &stubGameRepoFCC{games: []*models.Game{game}}
	api := &adminCaptureSendAPI{}
	canceler := &mockCanceler{result: &courtCancellationResult{}}

	job := &FinalCourtCheckJob{
		api:        api,
		gameRepo:   gameRepo,
		partRepo:   &stubPartRepoPC{count: 0},
		guestRepo:  &stubGuestRepoPC{count: 0},
		groupRepo:  &stubGroupRepoPC{group: group},
		canceler:   canceler,
		loc:        time.UTC,
		logger:     noopLogger(),
		pollWindow: 5 * time.Minute,
	}
	job.runFinalCourtCheck(false)

	if canceler.calls != 0 {
		t.Errorf("expected canceler NOT called for out-of-window game, got %d calls", canceler.calls)
	}
	if len(gameRepo.markedIDs) != 0 {
		t.Errorf("expected game NOT marked done (out of window), got %v", gameRepo.markedIDs)
	}
}

// TestFinalCheck_Force_IgnoresWindow verifies that runFinalCourtCheck(force=true)
// processes games regardless of timing gate.
func TestFinalCheck_Force_IgnoresWindow(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}

	// GameDate far in the future — would normally be outside pollWindow.
	game := &models.Game{
		ID:          25,
		ChatID:      100,
		GameDate:    time.Now().Add(48 * time.Hour),
		Courts:      "1",
		CourtsCount: 1,
		Venue:       &models.Venue{GracePeriodHours: 24},
	}

	gameRepo := &stubGameRepoFCC{games: []*models.Game{game}}
	api := &adminCaptureSendAPI{}
	canceler := &mockCanceler{result: buildNoOpResult(game)}

	job := &FinalCourtCheckJob{
		api:        api,
		gameRepo:   gameRepo,
		partRepo:   &stubPartRepoPC{count: 2}, // at capacity for 1 court → courtsToCancel=0
		guestRepo:  &stubGuestRepoPC{count: 0},
		groupRepo:  &stubGroupRepoPC{group: group},
		canceler:   canceler,
		loc:        time.UTC,
		logger:     noopLogger(),
		pollWindow: 5 * time.Minute,
	}
	job.runFinalCourtCheck(true)

	// processFinalCheck reached (courtsToCancel=0 → marks done without canceling)
	if len(gameRepo.markedIDs) != 1 || gameRepo.markedIDs[0] != 25 {
		t.Errorf("expected game 25 marked done (force=true), got %v", gameRepo.markedIDs)
	}
}

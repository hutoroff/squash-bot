package service

import (
	"context"
	"errors"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

// ── mockGameRepo ──────────────────────────────────────────────────────────────

type mockGameRepo struct {
	createResult  *models.Game
	createErr     error
	getByIDResult *models.Game
	getByIDErr    error
	updateMsgErr  error
	existingGames []*models.Game
	existingErr   error
}

func (m *mockGameRepo) Create(_ context.Context, game *models.Game) (*models.Game, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	if m.createResult != nil {
		return m.createResult, nil
	}
	cp := *game
	cp.ID = 42
	return &cp, nil
}

func (m *mockGameRepo) GetByID(_ context.Context, id int64) (*models.Game, error) {
	if m.getByIDErr != nil {
		return nil, m.getByIDErr
	}
	if m.getByIDResult != nil {
		return m.getByIDResult, nil
	}
	return &models.Game{ID: id}, nil
}

func (m *mockGameRepo) UpdateMessageID(_ context.Context, _, _ int64) error {
	return m.updateMsgErr
}

func (m *mockGameRepo) GetUncompletedGamesByGroupAndDay(_ context.Context, _ int64, _, _ time.Time) ([]*models.Game, error) {
	return m.existingGames, m.existingErr
}

func (m *mockGameRepo) GetUpcomingGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (m *mockGameRepo) GetUpcomingGamesByChatIDs(_ context.Context, _ []int64) ([]*models.Game, error) {
	return nil, nil
}
func (m *mockGameRepo) UpdateCourts(_ context.Context, _ int64, _ string, _ int) error { return nil }
func (m *mockGameRepo) GetNextGameForTelegramUser(_ context.Context, _ int64) (*models.Game, error) {
	return nil, nil
}
func (m *mockGameRepo) GetGamesForPlayer(_ context.Context, _ int64) ([]models.PlayerGame, error) {
	return nil, nil
}
func (m *mockGameRepo) GetUpcomingUnnotifiedGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (m *mockGameRepo) MarkNotifiedDayBefore(_ context.Context, _ int64) error { return nil }
func (m *mockGameRepo) MarkCompleted(_ context.Context, _ int64) error         { return nil }

// ── mockVenueRepo ─────────────────────────────────────────────────────────────

type mockVenueRepo struct {
	setLastReminderErr error
}

func (m *mockVenueRepo) Create(_ context.Context, v *models.Venue) (*models.Venue, error) {
	return v, nil
}
func (m *mockVenueRepo) GetByID(_ context.Context, _ int64) (*models.Venue, error) { return nil, nil }
func (m *mockVenueRepo) GetByIDAndGroupID(_ context.Context, _, _ int64) (*models.Venue, error) {
	return nil, nil
}
func (m *mockVenueRepo) GetByGroupID(_ context.Context, _ int64) ([]*models.Venue, error) {
	return nil, nil
}
func (m *mockVenueRepo) Update(_ context.Context, v *models.Venue) (*models.Venue, error) {
	return v, nil
}
func (m *mockVenueRepo) Delete(_ context.Context, _, _ int64) error { return nil }
func (m *mockVenueRepo) SetLastBookingReminderAt(_ context.Context, _ int64) error {
	return m.setLastReminderErr
}
func (m *mockVenueRepo) SetLastAutoBookingAt(_ context.Context, _ int64) error { return nil }

// ── mockAutoBookingResultRepo ─────────────────────────────────────────────────

type mockAutoBookingResultRepo struct {
	saveErr    error
	saveCalls  int
	results    []*models.AutoBookingResult
	getByIDErr error
	setGameErr error
}

func (m *mockAutoBookingResultRepo) Save(_ context.Context, _ int64, _ time.Time, _, _ string, _ int) error {
	m.saveCalls++
	return m.saveErr
}

func (m *mockAutoBookingResultRepo) GetByVenueAndDate(_ context.Context, _ int64, _ time.Time) ([]*models.AutoBookingResult, error) {
	return m.results, m.getByIDErr
}

func (m *mockAutoBookingResultRepo) GetByVenueAndDateAndTime(_ context.Context, _ int64, _ time.Time, _ string) (*models.AutoBookingResult, error) {
	return nil, nil
}

func (m *mockAutoBookingResultRepo) GetByGameID(_ context.Context, _ int64) (*models.AutoBookingResult, error) {
	return nil, nil
}

func (m *mockAutoBookingResultRepo) SetGameID(_ context.Context, _, _ int64) error {
	return m.setGameErr
}

// TestHandleManualReminder_DBError_FallsOpen verifies that when
// GetUncompletedGamesByGroupAndDay returns an error, the job proceeds with an
// admin DM rather than silently suppressing the reminder.
func TestHandleManualReminder_DBError_FallsOpen(t *testing.T) {
	const chatID int64 = -1001
	api := &mockTelegramAPI{
		admins: []tgbotapi.ChatMember{makeChatMember(101, false)},
	}
	api.sendResult = tgbotapi.Message{MessageID: 7}

	gameRepo := &mockGameRepo{existingErr: errors.New("db timeout")}
	job := &BookingReminderJob{
		api:      api,
		gameRepo: gameRepo,
		logger:   noopLogger(),
	}

	venue := &models.Venue{ID: 1, Name: "Venue", BookingOpensDays: 14}
	lz := i18n.New(i18n.En)

	ok := job.handleManualReminder(context.Background(), chatID, venue,
		time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC), lz)
	if !ok {
		t.Error("expected true: DB error falls open → admin DM sent")
	}
	if len(api.sendCalls) == 0 {
		t.Error("expected at least one admin DM on DB error, got none")
	}
}

// ── createGameAndAnnounce tests ───────────────────────────────────────────────

func TestCreateGameAndAnnounce_Success(t *testing.T) {
	const chatID int64 = -1001
	sentMsgID := 999

	api := &mockTelegramAPI{
		sendCalls: nil,
		admins:    []tgbotapi.ChatMember{makeChatMember(101, false)},
	}
	// Make Send return a message with a concrete MessageID.
	api.sendResult = tgbotapi.Message{MessageID: sentMsgID}

	gameRepo := &mockGameRepo{}
	job := &BookingReminderJob{
		api:                   api,
		gameRepo:              gameRepo,
		autoBookingResultRepo: &mockAutoBookingResultRepo{},
		logger:                noopLogger(),
	}

	venue := &models.Venue{ID: 1, Name: "Test Venue", PreferredGameTimes: "18:00"}
	gameDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	result := &models.AutoBookingResult{
		VenueID:     1,
		GameDate:    gameDate,
		GameTime:    "18:00",
		Courts:      "1,2",
		CourtsCount: 2,
	}
	lz := i18n.New(i18n.En)

	ok := job.createGameAndAnnounce(context.Background(), chatID, venue, result, time.Now().UTC(), time.UTC, lz)
	if !ok {
		t.Fatal("expected createGameAndAnnounce to return true on success")
	}
	if len(api.sendCalls) != 1 {
		t.Fatalf("expected 1 Send call (game announcement), got %d", len(api.sendCalls))
	}
	// Message should go to the group chat, not to an admin.
	msg, ok2 := api.sendCalls[0].(tgbotapi.MessageConfig)
	if !ok2 {
		t.Fatalf("expected MessageConfig, got %T", api.sendCalls[0])
	}
	if msg.ChatID != chatID {
		t.Errorf("announcement sent to %d, want group chat %d", msg.ChatID, chatID)
	}
}

func TestCreateGameAndAnnounce_GameCreateFails_ReturnsFalse(t *testing.T) {
	api := &mockTelegramAPI{}
	gameRepo := &mockGameRepo{createErr: errors.New("db error")}
	job := &BookingReminderJob{
		api:                   api,
		gameRepo:              gameRepo,
		autoBookingResultRepo: &mockAutoBookingResultRepo{},
		logger:                noopLogger(),
	}

	venue := &models.Venue{ID: 1, Name: "Venue"}
	result := &models.AutoBookingResult{Courts: "1", CourtsCount: 1, GameDate: time.Now()}
	lz := i18n.New(i18n.En)

	ok := job.createGameAndAnnounce(context.Background(), -1001, venue, result, time.Now().UTC(), time.UTC, lz)
	if ok {
		t.Error("expected false when game creation fails")
	}
	if len(api.sendCalls) != 0 {
		t.Errorf("expected no Send calls on game creation failure, got %d", len(api.sendCalls))
	}
}

func TestCreateGameAndAnnounce_SendFails_ReturnsFalse(t *testing.T) {
	api := &mockTelegramAPI{sendErr: errors.New("telegram error")}
	gameRepo := &mockGameRepo{}
	job := &BookingReminderJob{
		api:                   api,
		gameRepo:              gameRepo,
		autoBookingResultRepo: &mockAutoBookingResultRepo{},
		logger:                noopLogger(),
	}

	venue := &models.Venue{ID: 1, Name: "Venue"}
	result := &models.AutoBookingResult{Courts: "1", CourtsCount: 1, GameDate: time.Now()}
	lz := i18n.New(i18n.En)

	ok := job.createGameAndAnnounce(context.Background(), -1001, venue, result, time.Now().UTC(), time.UTC, lz)
	if ok {
		t.Error("expected false when Telegram Send fails")
	}
}

func TestCreateGameAndAnnounce_PreferredTimeApplied(t *testing.T) {
	var capturedGame *models.Game
	api := &mockTelegramAPI{}
	api.sendResult = tgbotapi.Message{MessageID: 1}
	gameRepo := &mockGameRepo{
		createResult: &models.Game{ID: 1},
	}
	// Capture the game passed to Create by wrapping create call tracking.
	captureRepo := &captureCreateRepo{mockGameRepo: gameRepo, captured: &capturedGame}

	job := &BookingReminderJob{
		api:                   api,
		gameRepo:              captureRepo,
		autoBookingResultRepo: &mockAutoBookingResultRepo{},
		logger:                noopLogger(),
	}

	berlin, _ := time.LoadLocation("Europe/Berlin")
	venue := &models.Venue{ID: 1, Name: "Venue", PreferredGameTimes: "18:30"}
	gameDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	result := &models.AutoBookingResult{Courts: "1", CourtsCount: 1, GameDate: gameDate, GameTime: "18:30"}
	lz := i18n.New(i18n.En)

	job.createGameAndAnnounce(context.Background(), -1001, venue, result, time.Now().In(berlin), berlin, lz)

	if capturedGame == nil {
		t.Fatal("Create was not called")
	}
	if capturedGame.GameDate.Hour() != 18 || capturedGame.GameDate.Minute() != 30 {
		t.Errorf("game time: got %02d:%02d, want 18:30",
			capturedGame.GameDate.Hour(), capturedGame.GameDate.Minute())
	}
	if capturedGame.GameDate.Location().String() != berlin.String() {
		t.Errorf("game location: got %v, want %v", capturedGame.GameDate.Location(), berlin)
	}
}

// captureCreateRepo wraps mockGameRepo to capture the game passed to Create.
type captureCreateRepo struct {
	*mockGameRepo
	captured **models.Game
}

func (c *captureCreateRepo) Create(ctx context.Context, game *models.Game) (*models.Game, error) {
	*c.captured = game
	return c.mockGameRepo.Create(ctx, game)
}

// ── handleAutoBookingReminder multi-result tests ──────────────────────────────

// TestHandleAutoBookingReminder_TwoResults_CreatesTwoGames verifies that when
// auto_booking produced two results for 18:00 and 20:00 (both with GameID=nil),
// handleAutoBookingReminder creates two games and posts two group announcements.
func TestHandleAutoBookingReminder_TwoResults_CreatesTwoGames(t *testing.T) {
	const chatID int64 = -1001
	api := &mockTelegramAPI{}
	api.sendResult = tgbotapi.Message{MessageID: 10}

	gameRepo := &mockGameRepo{}
	gameDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	resultRepo := &mockAutoBookingResultRepo{
		results: []*models.AutoBookingResult{
			{ID: 1, GameTime: "18:00", Courts: "1", CourtsCount: 1, GameDate: gameDate},
			{ID: 2, GameTime: "20:00", Courts: "2", CourtsCount: 1, GameDate: gameDate},
		},
	}
	job := &BookingReminderJob{
		api:                   api,
		gameRepo:              gameRepo,
		autoBookingResultRepo: resultRepo,
		logger:                noopLogger(),
	}

	venue := &models.Venue{ID: 1, Name: "Venue", PreferredGameTimes: "18:00,20:00"}
	lz := i18n.New(i18n.En)

	ok := job.handleAutoBookingReminder(context.Background(), chatID, venue, gameDate, time.Now().UTC(), time.UTC, lz)
	if !ok {
		t.Error("expected true when games are created")
	}

	announcements := 0
	for _, call := range api.sendCalls {
		msg, isMsgConfig := call.(tgbotapi.MessageConfig)
		if isMsgConfig && msg.ChatID == chatID {
			announcements++
		}
	}
	if announcements != 2 {
		t.Errorf("expected 2 game announcements to group chat, got %d (total sends: %d)",
			announcements, len(api.sendCalls))
	}
}

// TestHandleAutoBookingReminder_GetByVenueAndDateErrors_FallsBackToAdminDM verifies that
// when GetByVenueAndDate returns an error, the job falls through to sending admin DMs
// rather than silently doing nothing.
func TestHandleAutoBookingReminder_GetByVenueAndDateErrors_FallsBackToAdminDM(t *testing.T) {
	const chatID int64 = -1001
	api := &mockTelegramAPI{
		admins: []tgbotapi.ChatMember{makeChatMember(101, false)},
	}
	api.sendResult = tgbotapi.Message{MessageID: 5}

	resultRepo := &mockAutoBookingResultRepo{
		getByIDErr: errors.New("db timeout"),
	}
	job := &BookingReminderJob{
		api:                   api,
		gameRepo:              &mockGameRepo{},
		autoBookingResultRepo: resultRepo,
		logger:                noopLogger(),
	}

	venue := &models.Venue{ID: 1, Name: "Venue", PreferredGameTimes: "18:00"}
	lz := i18n.New(i18n.En)

	ok := job.handleAutoBookingReminder(context.Background(), chatID, venue,
		time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC), time.Now().UTC(), time.UTC, lz)
	if !ok {
		t.Error("expected true: admin DM was sent as fallback")
	}
	if len(api.sendCalls) == 0 {
		t.Error("expected at least one Send call (admin DM fallback), got none")
	}
}

// TestHandleAutoBookingReminder_BothResultsHaveGameID_IsIdempotent verifies that
// when every auto_booking_result already has a GameID, no new games are created
// and no Telegram messages are sent. The method returns true so that
// last_booking_reminder_at is written and the venue isn't re-entered on every poll.
func TestHandleAutoBookingReminder_BothResultsHaveGameID_IsIdempotent(t *testing.T) {
	const chatID int64 = -1001
	api := &mockTelegramAPI{}
	gameRepo := &mockGameRepo{}

	gameID1, gameID2 := int64(10), int64(11)
	gameDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	resultRepo := &mockAutoBookingResultRepo{
		results: []*models.AutoBookingResult{
			{ID: 1, GameTime: "18:00", Courts: "1", CourtsCount: 1, GameDate: gameDate, GameID: &gameID1},
			{ID: 2, GameTime: "20:00", Courts: "2", CourtsCount: 1, GameDate: gameDate, GameID: &gameID2},
		},
	}
	job := &BookingReminderJob{
		api:                   api,
		gameRepo:              gameRepo,
		autoBookingResultRepo: resultRepo,
		logger:                noopLogger(),
	}

	venue := &models.Venue{ID: 1, Name: "Venue", PreferredGameTimes: "18:00,20:00"}
	lz := i18n.New(i18n.En)

	ok := job.handleAutoBookingReminder(context.Background(), chatID, venue, gameDate, time.Now().UTC(), time.UTC, lz)
	// Returns true so last_booking_reminder_at is written and the venue is not re-entered.
	if !ok {
		t.Error("expected true when all results already have a game (handled in prior run)")
	}
	if len(api.sendCalls) != 0 {
		t.Errorf("expected no Telegram sends on second run, got %d", len(api.sendCalls))
	}
}

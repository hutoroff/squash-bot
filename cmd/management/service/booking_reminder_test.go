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
func (m *mockGameRepo) MarkCompleted(_ context.Context, _ int64) error          { return nil }

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
	result     *models.AutoBookingResult
	getByIDErr error
}

func (m *mockAutoBookingResultRepo) Save(_ context.Context, _ int64, _ time.Time, _ string, _ int) error {
	m.saveCalls++
	return m.saveErr
}

func (m *mockAutoBookingResultRepo) GetByVenueAndDate(_ context.Context, _ int64, _ time.Time) (*models.AutoBookingResult, error) {
	return m.result, m.getByIDErr
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
		api:      api,
		gameRepo: gameRepo,
		logger:   noopLogger(),
	}

	venue := &models.Venue{ID: 1, Name: "Test Venue", PreferredGameTime: "18:00"}
	gameDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	result := &models.AutoBookingResult{
		VenueID:     1,
		GameDate:    gameDate,
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
		api:      api,
		gameRepo: gameRepo,
		logger:   noopLogger(),
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
		api:      api,
		gameRepo: gameRepo,
		logger:   noopLogger(),
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
		api:      api,
		gameRepo: captureRepo,
		logger:   noopLogger(),
	}

	berlin, _ := time.LoadLocation("Europe/Berlin")
	venue := &models.Venue{ID: 1, Name: "Venue", PreferredGameTime: "18:30"}
	gameDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	result := &models.AutoBookingResult{Courts: "1", CourtsCount: 1, GameDate: gameDate}
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

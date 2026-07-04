package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

// ── request-validation tests (no service needed) ──────────────────────────────

func TestSubmitGameResult_MissingGameID_Returns400(t *testing.T) {
	body := `{"author_telegram_id":1,"opponent_player_id":2}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/game-results", strings.NewReader(body))
	w := httptest.NewRecorder()

	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.submitGameResult(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("missing game_id: want 400, got %d", w.Code)
	}
}

func TestSubmitGameResult_MissingAuthorTelegramID_Returns400(t *testing.T) {
	body := `{"game_id":1,"opponent_player_id":2}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/game-results", strings.NewReader(body))
	w := httptest.NewRecorder()

	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.submitGameResult(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("missing author_telegram_id: want 400, got %d", w.Code)
	}
}

func TestSubmitGameResult_MissingOpponentPlayerID_Returns400(t *testing.T) {
	body := `{"game_id":1,"author_telegram_id":42}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/game-results", strings.NewReader(body))
	w := httptest.NewRecorder()

	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.submitGameResult(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("missing opponent_player_id: want 400, got %d", w.Code)
	}
}

func TestSubmitGameResult_InvalidJSON_Returns400(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/game-results", strings.NewReader("{bad json"))
	w := httptest.NewRecorder()

	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.submitGameResult(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("bad json: want 400, got %d", w.Code)
	}
}

// ── window-closed error mapping ───────────────────────────────────────────────

// TestSubmitGameResult_WindowClosed_Returns400 verifies that when the service
// returns ErrGameResultWindowClosed, the handler responds with 400 "window_closed".
// We build a real *service.GameResultService with stub repos so we exercise the
// full handler → service call chain without duplicating handler logic.
func TestSubmitGameResult_WindowClosed_Returns400(t *testing.T) {
	playerRepo := &apiStubPlayerRepo{
		byTgID: map[int64]*models.Player{
			42: {ID: 1, TelegramID: 42},
		},
	}
	gameRepo := &apiStubGameRepo{
		game:        &models.Game{ID: 7, ChatID: -1001},
		notInWindow: true,
	}
	auditSvc := service.NewAuditService(&apiStubAuditRepo{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc := service.NewGameResultService(
		nil,
		&apiStubResultRepo{},
		gameRepo,
		playerRepo,
		&apiStubPartRepo{},
		auditSvc,
		14,
		nil,
	)

	body := `{"game_id":7,"author_telegram_id":42,"opponent_player_id":2}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/game-results", strings.NewReader(body))
	w := httptest.NewRecorder()

	h := &Handler{
		gameResultSvc: svc,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h.submitGameResult(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("window closed: want 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "window_closed") {
		t.Errorf("want body to contain window_closed, got: %s", w.Body.String())
	}
}

// ── stubs ─────────────────────────────────────────────────────────────────────

type apiStubPlayerRepo struct {
	byTgID map[int64]*models.Player
}

func (r *apiStubPlayerRepo) Upsert(_ context.Context, p *models.Player) (*models.Player, error) {
	return p, nil
}
func (r *apiStubPlayerRepo) GetByTelegramID(_ context.Context, tgID int64) (*models.Player, error) {
	p, ok := r.byTgID[tgID]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return p, nil
}
func (r *apiStubPlayerRepo) GetByID(_ context.Context, _ int64) (*models.Player, error) {
	return nil, pgx.ErrNoRows
}

type apiStubGameRepo struct {
	game         *models.Game
	notInWindow  bool
	canAccess    bool
	canAccessErr error
}

func (r *apiStubGameRepo) Create(_ context.Context, g *models.Game) (*models.Game, error) {
	return g, nil
}
func (r *apiStubGameRepo) GetByID(_ context.Context, _ int64) (*models.Game, error) {
	return r.game, nil
}
func (r *apiStubGameRepo) GameInResultWindow(_ context.Context, _ int64, _ int) (bool, error) {
	return !r.notInWindow, nil
}
func (r *apiStubGameRepo) UpdateMessageID(_ context.Context, _, _ int64) error { return nil }
func (r *apiStubGameRepo) GetUncompletedGamesByGroupAndDay(_ context.Context, _ int64, _, _ time.Time) ([]*models.Game, error) {
	return nil, nil
}
func (r *apiStubGameRepo) GetCompletedGamesByGroupAndDay(_ context.Context, _ int64, _, _ time.Time) ([]*models.Game, error) {
	return nil, nil
}
func (r *apiStubGameRepo) GetUpcomingGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *apiStubGameRepo) GetUpcomingGamesByChatIDs(_ context.Context, _ []int64) ([]*models.Game, error) {
	return nil, nil
}
func (r *apiStubGameRepo) UpdateCourts(_ context.Context, _ int64, _ string, _ int) error {
	return nil
}
func (r *apiStubGameRepo) GetNextGameForTelegramUser(_ context.Context, _ int64) (*models.Game, error) {
	return nil, nil
}
func (r *apiStubGameRepo) GetGamesForPlayer(_ context.Context, _ int64) ([]models.PlayerGame, error) {
	return nil, nil
}
func (r *apiStubGameRepo) GetRecentCompletedGamesForPlayer(_ context.Context, _, _ int64, _ int) ([]models.PlayerGame, error) {
	return nil, nil
}
func (r *apiStubGameRepo) GetUpcomingUnnotifiedGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *apiStubGameRepo) MarkNotifiedDayBefore(_ context.Context, _ int64) error { return nil }
func (r *apiStubGameRepo) MarkCompleted(_ context.Context, _ int64) error         { return nil }
func (r *apiStubGameRepo) ListGroupIDsForPlayer(_ context.Context, _ int64) ([]int64, error) {
	return nil, nil
}
func (r *apiStubGameRepo) GetUpcomingGamesForFinalCheck(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *apiStubGameRepo) MarkFinalCourtCheckDone(_ context.Context, _ int64) error { return nil }
func (r *apiStubGameRepo) PlayerCanAccessGame(_ context.Context, _, _ int64) (bool, error) {
	return r.canAccess, r.canAccessErr
}

type apiStubPartRepo struct{}

func (r *apiStubPartRepo) Upsert(_ context.Context, _, _ int64, _ models.ParticipationStatus) error {
	return nil
}
func (r *apiStubPartRepo) GetByGame(_ context.Context, _ int64) ([]*models.GameParticipation, error) {
	return nil, nil
}
func (r *apiStubPartRepo) DeleteByGameAndPlayer(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}
func (r *apiStubPartRepo) GetRegisteredCount(_ context.Context, _ int64) (int, error) {
	return 0, nil
}

type apiStubResultRepo struct{}

func (r *apiStubResultRepo) Create(_ context.Context, _ *models.GameResult) (int64, error) {
	return 0, nil
}
func (r *apiStubResultRepo) GetByID(_ context.Context, _ int64) (*models.GameResult, error) {
	return nil, nil
}
func (r *apiStubResultRepo) SetApprovalMessage(_ context.Context, _, _ int64, _ int) error {
	return nil
}
func (r *apiStubResultRepo) Decide(_ context.Context, _ int64, _ models.GameResultStatus, _ time.Time) error {
	return nil
}
func (r *apiStubResultRepo) DecideInTx(_ context.Context, _ pgx.Tx, _ int64, _ models.GameResultStatus, _ time.Time) error {
	return nil
}
func (r *apiStubResultRepo) ListPendingOlderThan(_ context.Context, _ time.Time) ([]*models.GameResult, error) {
	return nil, nil
}
func (r *apiStubResultRepo) ListByGroupAndDate(_ context.Context, _ int64, _ time.Time) ([]*models.GameResult, error) {
	return nil, nil
}
func (r *apiStubResultRepo) ListByGameID(_ context.Context, _ int64) ([]*models.GameResult, error) {
	return nil, nil
}

type apiStubAuditRepo struct{}

func (r *apiStubAuditRepo) Insert(_ context.Context, _ *models.AuditEvent) error { return nil }
func (r *apiStubAuditRepo) Query(_ context.Context, _ models.AuditQueryFilter) ([]*models.AuditEvent, error) {
	return nil, nil
}
func (r *apiStubAuditRepo) DeleteOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

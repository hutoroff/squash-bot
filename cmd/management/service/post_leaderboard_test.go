package service

import (
	"context"
	"errors"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

var errSendBoom = errors.New("send boom")

// ── mock TelegramAPI for leaderboard tests ───────────────────────────────────

type mockTgAPIForLB struct {
	sentCount int
	lastMsg   tgbotapi.MessageConfig
	sendErr   error
}

func (m *mockTgAPIForLB) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	if msg, ok := c.(tgbotapi.MessageConfig); ok {
		m.lastMsg = msg
	}
	if m.sendErr != nil {
		return tgbotapi.Message{}, m.sendErr
	}
	m.sentCount++
	return tgbotapi.Message{}, nil
}

func (m *mockTgAPIForLB) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{Ok: true}, nil
}

func (m *mockTgAPIForLB) GetChatAdministrators(config tgbotapi.ChatAdministratorsConfig) ([]tgbotapi.ChatMember, error) {
	return nil, nil
}

// ── stub GroupRepository for leaderboard tests ───────────────────────────────

type stubGroupRepoForLB struct {
	group                     models.Group
	setLastLeaderboardCalls   int
	lastLeaderboardPostedDate time.Time
}

func (r *stubGroupRepoForLB) GetAll(_ context.Context) ([]models.Group, error) {
	return []models.Group{r.group}, nil
}

func (r *stubGroupRepoForLB) GetByID(_ context.Context, _ int64) (*models.Group, error) {
	return &r.group, nil
}

func (r *stubGroupRepoForLB) Upsert(_ context.Context, _ int64, _ string, _ bool) error {
	return nil
}

func (r *stubGroupRepoForLB) SetLanguage(_ context.Context, _ int64, _ string) error {
	return nil
}

func (r *stubGroupRepoForLB) SetTimezone(_ context.Context, _ int64, _ string) error {
	return nil
}

func (r *stubGroupRepoForLB) SetChangelogEnabled(_ context.Context, _ int64, _ bool) error {
	return nil
}

func (r *stubGroupRepoForLB) SetAutoBookingAllowed(_ context.Context, _ int64, _ bool) ([]int64, error) {
	return nil, nil
}

func (r *stubGroupRepoForLB) Remove(_ context.Context, _ int64) error { return nil }

func (r *stubGroupRepoForLB) Exists(_ context.Context, _ int64) (bool, error) {
	return true, nil
}

func (r *stubGroupRepoForLB) SetLastLeaderboardPostedFor(_ context.Context, _ int64, date time.Time) error {
	r.setLastLeaderboardCalls++
	r.lastLeaderboardPostedDate = date
	return nil
}

// ── stub GameResultRepository for leaderboard tests ──────────────────────────

type stubResultRepoForLB struct {
	results []*models.GameResult
}

func (r *stubResultRepoForLB) Create(_ context.Context, _ *models.GameResult) (int64, error) {
	return 0, nil
}

func (r *stubResultRepoForLB) GetByID(_ context.Context, _ int64) (*models.GameResult, error) {
	return nil, nil
}

func (r *stubResultRepoForLB) SetApprovalMessage(_ context.Context, _, _ int64, _ int) error {
	return nil
}

func (r *stubResultRepoForLB) Decide(_ context.Context, _ int64, _ models.GameResultStatus, _ time.Time) error {
	return nil
}

func (r *stubResultRepoForLB) DecideInTx(_ context.Context, _ pgx.Tx, _ int64, _ models.GameResultStatus, _ time.Time) error {
	return nil
}

func (r *stubResultRepoForLB) ListPendingOlderThan(_ context.Context, _ time.Time) ([]*models.GameResult, error) {
	return nil, nil
}

func (r *stubResultRepoForLB) ListByGroupAndDate(_ context.Context, _ int64, _ time.Time) ([]*models.GameResult, error) {
	return r.results, nil
}

func (r *stubResultRepoForLB) ListByGameID(_ context.Context, _ int64) ([]*models.GameResult, error) {
	return nil, nil
}

// ── stub PlayerRatingRepository for leaderboard tests ────────────────────────

type stubRatingRepoForLB struct {
	ratings []*models.PlayerRating
}

func (r *stubRatingRepoForLB) GetOrInit(_ context.Context, _, _ int64) (*models.PlayerRating, error) {
	return nil, nil
}

func (r *stubRatingRepoForLB) Upsert(_ context.Context, _ *models.PlayerRating) error {
	return nil
}

func (r *stubRatingRepoForLB) ListByGroup(_ context.Context, _ int64) ([]*models.PlayerRating, error) {
	return r.ratings, nil
}

func (r *stubRatingRepoForLB) ListGroupsForPlayer(_ context.Context, _ int64) ([]int64, error) {
	return nil, nil
}

// ── stub RatingChangeRepository for leaderboard tests ────────────────────────

type stubChangeRepoForLB struct{}

func (r *stubChangeRepoForLB) Insert(_ context.Context, _ *models.RatingChange) error {
	return nil
}

func (r *stubChangeRepoForLB) InsertInTx(_ context.Context, _ pgx.Tx, _ *models.RatingChange) error {
	return nil
}

func (r *stubChangeRepoForLB) ListByGroupAndDateRange(_ context.Context, _ int64, _, _ time.Time) ([]*models.RatingChange, error) {
	return nil, nil
}

// ── stub GameRepository for leaderboard tests ────────────────────────────────

type stubGameRepoForLB struct {
	completedGames []*models.Game
}

func (r *stubGameRepoForLB) Create(_ context.Context, _ *models.Game) (*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForLB) GetByID(_ context.Context, _ int64) (*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForLB) GetUpcomingGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForLB) GetUpcomingGamesByChatIDs(_ context.Context, _ []int64) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForLB) UpdateMessageID(_ context.Context, _, _ int64) error { return nil }
func (r *stubGameRepoForLB) UpdateCourts(_ context.Context, _ int64, _ string, _ int) error {
	return nil
}
func (r *stubGameRepoForLB) GetNextGameForTelegramUser(_ context.Context, _ int64) (*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForLB) GetGamesForPlayer(_ context.Context, _ int64) ([]models.PlayerGame, error) {
	return nil, nil
}
func (r *stubGameRepoForLB) GetUpcomingUnnotifiedGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForLB) GetUncompletedGamesByGroupAndDay(_ context.Context, _ int64, _, _ time.Time) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForLB) GetCompletedGamesByGroupAndDay(_ context.Context, _ int64, _, _ time.Time) ([]*models.Game, error) {
	return r.completedGames, nil
}
func (r *stubGameRepoForLB) MarkNotifiedDayBefore(_ context.Context, _ int64) error { return nil }
func (r *stubGameRepoForLB) MarkCompleted(_ context.Context, _ int64) error         { return nil }
func (r *stubGameRepoForLB) GetRecentCompletedGamesForPlayer(_ context.Context, _, _ int64, _ int) ([]models.PlayerGame, error) {
	return nil, nil
}
func (r *stubGameRepoForLB) GameInResultWindow(_ context.Context, _ int64, _ int) (bool, error) {
	return true, nil
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestPostLeaderboard_SkipsAlreadyPostedDay(t *testing.T) {
	// candidate day = now-24h truncated to day start
	candidateDate := time.Now().In(time.UTC).Add(-24 * time.Hour).Truncate(24 * time.Hour)

	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                   100,
			Timezone:                 "UTC",
			LastLeaderboardPostedFor: &candidateDate, // already posted
		},
	}
	resultRepo := &stubResultRepoForLB{}

	auditSvc, _ := newCaptureAuditSvc()
	ratingRepo := &stubRatingRepoForLB{}
	changeRepo := &stubChangeRepoForLB{}
	ratingSvc := NewRatingService(nil, ratingRepo, changeRepo, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 0 {
		t.Errorf("expected no messages sent, got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 0 {
		t.Errorf("expected no SetLastLeaderboardPostedFor calls, got %d", groupRepo.setLastLeaderboardCalls)
	}
}

func TestPostLeaderboard_SkipsWhenNoApprovedResults(t *testing.T) {
	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                   200,
			Timezone:                 "UTC",
			LastLeaderboardPostedFor: nil, // never posted
		},
	}
	// Return results that are NOT approved (e.g. pending, rejected).
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{ID: 1, GroupID: 200, Status: models.GameResultPending},
			{ID: 2, GroupID: 200, Status: models.GameResultRejected},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingRepo := &stubRatingRepoForLB{}
	changeRepo := &stubChangeRepoForLB{}
	ratingSvc := NewRatingService(nil, ratingRepo, changeRepo, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 0 {
		t.Errorf("expected no messages sent, got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 1 {
		t.Errorf("expected SetLastLeaderboardPostedFor called once, got %d", groupRepo.setLastLeaderboardCalls)
	}
}

func TestPostLeaderboard_PostsWhenApprovedResultsExist(t *testing.T) {
	api := &mockTgAPIForLB{}
	firstName := "Alice"
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                   300,
			Timezone:                 "UTC",
			LastLeaderboardPostedFor: nil,
		},
	}
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{ID: 10, GroupID: 300, Status: models.GameResultApproved},
		},
	}
	// Provide a rated player so GetLeaderboard returns a non-empty slice.
	ratingRepo := &stubRatingRepoForLB{
		ratings: []*models.PlayerRating{
			{
				GroupID:     300,
				PlayerID:    1,
				Rating:      1600,
				RD:          200,
				Volatility:  0.06,
				GamesPlayed: 5,
				Player:      &models.Player{ID: 1, FirstName: &firstName},
			},
		},
	}
	changeRepo := &stubChangeRepoForLB{}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, ratingRepo, changeRepo, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 1 {
		t.Errorf("expected 1 message sent, got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 1 {
		t.Errorf("expected SetLastLeaderboardPostedFor called once, got %d", groupRepo.setLastLeaderboardCalls)
	}
	// Names can contain Markdown control characters, and the message has no
	// formatting that needs interpreting — the scheduled post must go out as
	// plain text.
	if api.lastMsg.ParseMode != "" {
		t.Errorf("ParseMode: got %q, want empty", api.lastMsg.ParseMode)
	}
}

// 24h-after-last-start gate: when the last completed game on the candidate
// day started less than 24 h ago, the post must wait for a later poll.
func TestPostLeaderboard_SkipsWhenLastGameLessThan24hOld(t *testing.T) {
	api := &mockTgAPIForLB{}
	firstName := "Alice"
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                   400,
			Timezone:                 "UTC",
			LastLeaderboardPostedFor: nil,
		},
	}
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{ID: 10, GroupID: 400, Status: models.GameResultApproved},
		},
	}
	ratingRepo := &stubRatingRepoForLB{
		ratings: []*models.PlayerRating{
			{GroupID: 400, PlayerID: 1, Rating: 1600, GamesPlayed: 5,
				Player: &models.Player{ID: 1, FirstName: &firstName}},
		},
	}
	// Last game started 23h ago — gate must defer.
	gameRepo := &stubGameRepoForLB{
		completedGames: []*models.Game{
			{ID: 1, ChatID: 400, GameDate: time.Now().Add(-23 * time.Hour)},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, ratingRepo, &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, gameRepo, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 0 {
		t.Errorf("expected no message sent while gate is open, got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 0 {
		t.Errorf("expected no marker update while gate is open, got %d", groupRepo.setLastLeaderboardCalls)
	}
}

func TestPostLeaderboard_PostsWhenLastGameOlderThan24h(t *testing.T) {
	api := &mockTgAPIForLB{}
	firstName := "Alice"
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                   500,
			Timezone:                 "UTC",
			LastLeaderboardPostedFor: nil,
		},
	}
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{ID: 10, GroupID: 500, Status: models.GameResultApproved},
		},
	}
	ratingRepo := &stubRatingRepoForLB{
		ratings: []*models.PlayerRating{
			{GroupID: 500, PlayerID: 1, Rating: 1600, GamesPlayed: 5,
				Player: &models.Player{ID: 1, FirstName: &firstName}},
		},
	}
	gameRepo := &stubGameRepoForLB{
		completedGames: []*models.Game{
			{ID: 1, ChatID: 500, GameDate: time.Now().Add(-25 * time.Hour)},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, ratingRepo, &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, gameRepo, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 1 {
		t.Errorf("expected 1 message sent once gate is closed, got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 1 {
		t.Errorf("expected marker set once after successful send, got %d", groupRepo.setLastLeaderboardCalls)
	}
}

// If Send fails the marker must NOT advance — the next poll has to retry.
func TestPostLeaderboard_DoesNotMarkOnSendFailure(t *testing.T) {
	api := &mockTgAPIForLB{sendErr: errSendBoom}
	firstName := "Alice"
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                   600,
			Timezone:                 "UTC",
			LastLeaderboardPostedFor: nil,
		},
	}
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{ID: 10, GroupID: 600, Status: models.GameResultApproved},
		},
	}
	ratingRepo := &stubRatingRepoForLB{
		ratings: []*models.PlayerRating{
			{GroupID: 600, PlayerID: 1, Rating: 1600, GamesPlayed: 5,
				Player: &models.Player{ID: 1, FirstName: &firstName}},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, ratingRepo, &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 0 {
		t.Errorf("expected sentCount=0 because Send errored, got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 0 {
		t.Errorf("expected no marker update after send failure, got %d", groupRepo.setLastLeaderboardCalls)
	}
}

package service

import (
	"context"
	"errors"
	"strings"
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
	sentTexts []string
	lastMsg   tgbotapi.MessageConfig
	sendErr   error
}

func (m *mockTgAPIForLB) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	if msg, ok := c.(tgbotapi.MessageConfig); ok {
		m.lastMsg = msg
		m.sentTexts = append(m.sentTexts, msg.Text)
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
	postedDates               []time.Time
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
func (r *stubGroupRepoForLB) SetLeaderboardNotificationsEnabled(_ context.Context, _ int64, _ bool) error {
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
	r.postedDates = append(r.postedDates, date)
	return nil
}

// ── stub GameResultRepository for leaderboard tests ──────────────────────────

type stubResultRepoForLB struct {
	results []*models.GameResult
	// byDay overrides results on a per-date basis. When non-nil, dates not
	// present in the map return nil (not the global results fallback).
	byDay map[string][]*models.GameResult
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

func (r *stubResultRepoForLB) ListByGroupAndDate(_ context.Context, _ int64, d time.Time) ([]*models.GameResult, error) {
	if r.byDay != nil {
		key := d.Format("2006-01-02")
		return r.byDay[key], nil
	}
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
func (r *stubGameRepoForLB) ListGroupIDsForPlayer(_ context.Context, _ int64) ([]int64, error) {
	return nil, nil
}
func (r *stubGameRepoForLB) GetUpcomingGamesForFinalCheck(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForLB) MarkFinalCourtCheckDone(_ context.Context, _ int64) error { return nil }
func (r *stubGameRepoForLB) GetRecentCompletedGamesForPlayer(_ context.Context, _, _ int64, _ int) ([]models.PlayerGame, error) {
	return nil, nil
}
func (r *stubGameRepoForLB) GameInResultWindow(_ context.Context, _ int64, _ int) (bool, error) {
	return true, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// utcDay returns local midnight for a day offset from today in UTC.
// offset=0 → today midnight, offset=-1 → yesterday, etc.
func utcDay(offset int) time.Time {
	now := time.Now().In(time.UTC)
	today0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return today0.AddDate(0, 0, offset)
}

// aliceRatings returns a non-empty leaderboard stub for the given group ID.
func aliceRatings(groupID int64) *stubRatingRepoForLB {
	firstName := "Alice"
	return &stubRatingRepoForLB{
		ratings: []*models.PlayerRating{
			{
				GroupID:     groupID,
				PlayerID:    1,
				Rating:      1600,
				GamesPlayed: 5,
				Player:      &models.Player{ID: 1, FirstName: &firstName},
			},
		},
	}
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestPostLeaderboard_SkipsAlreadyPostedDay(t *testing.T) {
	candidateDate := utcDay(-1)

	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          100,
			LeaderboardNotificationsEnabled: true,
			Timezone:                        "UTC",
			LastLeaderboardPostedFor:        &candidateDate, // already posted yesterday
		},
	}
	resultRepo := &stubResultRepoForLB{}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, &stubRatingRepoForLB{}, &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 0 {
		t.Errorf("expected no messages sent, got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 0 {
		t.Errorf("expected no SetLastLeaderboardPostedFor calls, got %d", groupRepo.setLastLeaderboardCalls)
	}
}

// TestPostLeaderboard_PendingResultDoesNotMarkDayDone is the core bug fix:
// a day with fresh pending results and zero approved must not be marked done,
// so auto-approve can resolve it and a later poll will post.
func TestPostLeaderboard_PendingResultDoesNotMarkDayDone(t *testing.T) {
	twoDaysAgo := utcDay(-2)
	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          200,
			LeaderboardNotificationsEnabled: true,
			Timezone:                        "UTC",
			LastLeaderboardPostedFor:        &twoDaysAgo, // scoped to yesterday only
		},
	}
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{ID: 1, GroupID: 200, Status: models.GameResultPending, SubmittedAt: time.Now()},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, &stubRatingRepoForLB{}, &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 0 {
		t.Errorf("expected no messages sent, got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 0 {
		t.Errorf("expected SetLastLeaderboardPostedFor not called (day must stay open), got %d", groupRepo.setLastLeaderboardCalls)
	}
}

func TestPostLeaderboard_PostsWhenApprovedResultsExist(t *testing.T) {
	twoDaysAgo := utcDay(-2)
	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          300,
			LeaderboardNotificationsEnabled: true,
			Timezone:                        "UTC",
			LastLeaderboardPostedFor:        &twoDaysAgo,
		},
	}
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{ID: 10, GroupID: 300, Status: models.GameResultApproved},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, aliceRatings(300), &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

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
	twoDaysAgo := utcDay(-2)
	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          400,
			LeaderboardNotificationsEnabled: true,
			Timezone:                        "UTC",
			LastLeaderboardPostedFor:        &twoDaysAgo,
		},
	}
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{ID: 10, GroupID: 400, Status: models.GameResultApproved},
		},
	}
	// Last game started 23 h ago — gate must defer.
	gameRepo := &stubGameRepoForLB{
		completedGames: []*models.Game{
			{ID: 1, ChatID: 400, GameDate: time.Now().Add(-23 * time.Hour)},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, aliceRatings(400), &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

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
	twoDaysAgo := utcDay(-2)
	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          500,
			LeaderboardNotificationsEnabled: true,
			Timezone:                        "UTC",
			LastLeaderboardPostedFor:        &twoDaysAgo,
		},
	}
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{ID: 10, GroupID: 500, Status: models.GameResultApproved},
		},
	}
	gameRepo := &stubGameRepoForLB{
		completedGames: []*models.Game{
			{ID: 1, ChatID: 500, GameDate: time.Now().Add(-25 * time.Hour)},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, aliceRatings(500), &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

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
	twoDaysAgo := utcDay(-2)
	api := &mockTgAPIForLB{sendErr: errSendBoom}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          600,
			LeaderboardNotificationsEnabled: true,
			Timezone:                        "UTC",
			LastLeaderboardPostedFor:        &twoDaysAgo,
		},
	}
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{ID: 10, GroupID: 600, Status: models.GameResultApproved},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, aliceRatings(600), &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 0 {
		t.Errorf("expected sentCount=0 because Send errored, got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 0 {
		t.Errorf("expected no marker update after send failure, got %d", groupRepo.setLastLeaderboardCalls)
	}
}

// TestPostLeaderboard_PostsWithApprovedDespitePending — a fresh pending result
// does not block posting when at least one approved result also exists.
func TestPostLeaderboard_PostsWithApprovedDespitePending(t *testing.T) {
	twoDaysAgo := utcDay(-2)
	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          700,
			LeaderboardNotificationsEnabled: true,
			Timezone:                        "UTC",
			LastLeaderboardPostedFor:        &twoDaysAgo,
		},
	}
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{ID: 10, GroupID: 700, Status: models.GameResultApproved},
			{ID: 11, GroupID: 700, Status: models.GameResultPending, SubmittedAt: time.Now()},
		},
	}
	gameRepo := &stubGameRepoForLB{
		completedGames: []*models.Game{
			{ID: 1, ChatID: 700, GameDate: time.Now().Add(-25 * time.Hour)},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, aliceRatings(700), &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, gameRepo, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 1 {
		t.Errorf("expected 1 send (approved overrides pending), got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 1 {
		t.Errorf("expected 1 marker call, got %d", groupRepo.setLastLeaderboardCalls)
	}
}

// TestPostLeaderboard_StalePendingDoesNotBlockForever — a pending result older
// than the grace window is no longer blocking; the day is silently skipped.
func TestPostLeaderboard_StalePendingDoesNotBlockForever(t *testing.T) {
	twoDaysAgo := utcDay(-2)
	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          800,
			LeaderboardNotificationsEnabled: true,
			Timezone:                        "UTC",
			LastLeaderboardPostedFor:        &twoDaysAgo,
		},
	}
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{
				ID:          20,
				GroupID:     800,
				Status:      models.GameResultPending,
				SubmittedAt: time.Now().Add(-(leaderboardPendingGraceWindow + time.Hour)),
			},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, &stubRatingRepoForLB{}, &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 0 {
		t.Errorf("expected no send (stale pending, no approved), got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 1 {
		t.Errorf("expected 1 silent-skip marker call, got %d", groupRepo.setLastLeaderboardCalls)
	}
}

// TestPostLeaderboard_SettledRejectedOnlyAdvancesSilently — a day with only
// rejected/canceled results produces no message and silently advances the marker.
func TestPostLeaderboard_SettledRejectedOnlyAdvancesSilently(t *testing.T) {
	twoDaysAgo := utcDay(-2)
	yesterday := utcDay(-1)
	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          900,
			LeaderboardNotificationsEnabled: true,
			Timezone:                        "UTC",
			LastLeaderboardPostedFor:        &twoDaysAgo,
		},
	}
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{ID: 30, GroupID: 900, Status: models.GameResultRejected},
			{ID: 31, GroupID: 900, Status: models.GameResultCanceled},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, &stubRatingRepoForLB{}, &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 0 {
		t.Errorf("expected no send for rejected/canceled results, got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 1 {
		t.Errorf("expected 1 marker call (silent advance), got %d", groupRepo.setLastLeaderboardCalls)
	}
	if !groupRepo.lastLeaderboardPostedDate.Equal(yesterday) {
		t.Errorf("marker date: got %v, want %v",
			groupRepo.lastLeaderboardPostedDate.Format("2006-01-02"),
			yesterday.Format("2006-01-02"))
	}
}

// TestPostLeaderboard_LateApprovalPostsOnLaterDay — results approved on a day
// inside the look-back window (not yesterday) are posted on the next poll.
func TestPostLeaderboard_LateApprovalPostsOnLaterDay(t *testing.T) {
	threeDaysAgo := utcDay(-3)
	twoDaysAgo := utcDay(-2)
	yesterday := utcDay(-1)

	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          1000,
			LeaderboardNotificationsEnabled: true,
			Timezone:                        "UTC",
			LastLeaderboardPostedFor:        &threeDaysAgo,
		},
	}
	resultRepo := &stubResultRepoForLB{
		byDay: map[string][]*models.GameResult{
			twoDaysAgo.Format("2006-01-02"): {
				{ID: 40, GroupID: 1000, Status: models.GameResultApproved},
			},
			yesterday.Format("2006-01-02"): nil,
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, aliceRatings(1000), &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 1 {
		t.Errorf("expected 1 send for late-approved day, got %d", api.sentCount)
	}
	if len(groupRepo.postedDates) == 0 || !groupRepo.postedDates[0].Equal(twoDaysAgo) {
		t.Errorf("expected first posted date = %v, got %v",
			twoDaysAgo.Format("2006-01-02"), groupRepo.postedDates)
	}
}

// TestPostLeaderboard_MultiDayCatchUpPostsOnePerDay — two catch-up days with
// approved results produce two messages in oldest-first order.
func TestPostLeaderboard_MultiDayCatchUpPostsOnePerDay(t *testing.T) {
	fourDaysAgo := utcDay(-4)
	threeDaysAgo := utcDay(-3)
	twoDaysAgo := utcDay(-2)
	yesterday := utcDay(-1)

	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          1100,
			LeaderboardNotificationsEnabled: true,
			Timezone:                        "UTC",
			LastLeaderboardPostedFor:        &fourDaysAgo,
		},
	}
	resultRepo := &stubResultRepoForLB{
		byDay: map[string][]*models.GameResult{
			threeDaysAgo.Format("2006-01-02"): {
				{ID: 50, GroupID: 1100, Status: models.GameResultApproved},
			},
			twoDaysAgo.Format("2006-01-02"): {
				{ID: 51, GroupID: 1100, Status: models.GameResultApproved},
			},
			yesterday.Format("2006-01-02"): nil,
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, aliceRatings(1100), &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 2 {
		t.Errorf("expected 2 sends for multi-day catch-up, got %d", api.sentCount)
	}
	if len(api.sentTexts) < 2 {
		t.Fatalf("expected at least 2 sent texts, got %v", api.sentTexts)
	}
	// Messages must arrive oldest-first.
	wantFirst := "🏆 Leaderboard — " + threeDaysAgo.Format("02 Jan 2006")
	wantSecond := "🏆 Leaderboard — " + twoDaysAgo.Format("02 Jan 2006")
	if !strings.HasPrefix(api.sentTexts[0], wantFirst) {
		t.Errorf("first message: want prefix %q, got %q", wantFirst, api.sentTexts[0])
	}
	if !strings.HasPrefix(api.sentTexts[1], wantSecond) {
		t.Errorf("second message: want prefix %q, got %q", wantSecond, api.sentTexts[1])
	}
}

// TestPostLeaderboard_StopsAtFirstUnsettledDay — the loop stops when it hits a
// day with fresh pending results, not posting later days even if approved.
func TestPostLeaderboard_StopsAtFirstUnsettledDay(t *testing.T) {
	fourDaysAgo := utcDay(-4)
	threeDaysAgo := utcDay(-3)
	twoDaysAgo := utcDay(-2)
	yesterday := utcDay(-1)

	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          1200,
			LeaderboardNotificationsEnabled: true,
			Timezone:                        "UTC",
			LastLeaderboardPostedFor:        &fourDaysAgo,
		},
	}
	resultRepo := &stubResultRepoForLB{
		byDay: map[string][]*models.GameResult{
			threeDaysAgo.Format("2006-01-02"): {
				{ID: 60, GroupID: 1200, Status: models.GameResultApproved},
			},
			twoDaysAgo.Format("2006-01-02"): {
				{ID: 61, GroupID: 1200, Status: models.GameResultPending, SubmittedAt: time.Now()},
			},
			yesterday.Format("2006-01-02"): {
				{ID: 62, GroupID: 1200, Status: models.GameResultApproved},
			},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, aliceRatings(1200), &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 1 {
		t.Errorf("expected 1 send (stopped at unsettled day), got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 1 {
		t.Errorf("expected 1 marker call (only the first approved day), got %d", groupRepo.setLastLeaderboardCalls)
	}
}

// TestPostLeaderboard_ForceRespectsMarker — a manual (force) trigger must not
// re-post a day already covered by the marker; force only bypasses the 24h gate.
func TestPostLeaderboard_ForceRespectsMarker(t *testing.T) {
	yesterday := utcDay(-1)
	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          1300,
			LeaderboardNotificationsEnabled: true,
			Timezone:                        "UTC",
			LastLeaderboardPostedFor:        &yesterday, // everything through yesterday already posted
		},
	}
	// Approved results across the whole window — none should be re-posted because
	// the marker already covers up to yesterday and the loop ends at yesterday.
	resultRepo := &stubResultRepoForLB{
		results: []*models.GameResult{
			{ID: 70, GroupID: 1300, Status: models.GameResultApproved},
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, aliceRatings(1300), &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, resultRepo, ratingSvc, time.UTC, noopLogger())
	job.run(true) // force

	if api.sentCount != 0 {
		t.Errorf("expected force not to re-post days covered by the marker, got %d sends", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 0 {
		t.Errorf("expected no marker update, got %d", groupRepo.setLastLeaderboardCalls)
	}
}

// TestPostLeaderboard_SkipsGroupWithNotificationsDisabled verifies that a group
// with LeaderboardNotificationsEnabled == false is silently skipped — no message
// is sent and the marker is not updated, even when force == true.
func TestPostLeaderboard_SkipsGroupWithNotificationsDisabled(t *testing.T) {
	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          100,
			Timezone:                        "UTC",
			LeaderboardNotificationsEnabled: false,
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, &stubRatingRepoForLB{}, &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, &stubResultRepoForLB{}, ratingSvc, time.UTC, noopLogger())
	job.run(false)

	if api.sentCount != 0 {
		t.Errorf("expected no messages sent for disabled group, got %d", api.sentCount)
	}
	if groupRepo.setLastLeaderboardCalls != 0 {
		t.Errorf("expected no marker update for disabled group, got %d", groupRepo.setLastLeaderboardCalls)
	}
}

func TestPostLeaderboard_SkipsGroupWithNotificationsDisabled_Force(t *testing.T) {
	api := &mockTgAPIForLB{}
	groupRepo := &stubGroupRepoForLB{
		group: models.Group{
			ChatID:                          100,
			Timezone:                        "UTC",
			LeaderboardNotificationsEnabled: false,
		},
	}

	auditSvc, _ := newCaptureAuditSvc()
	ratingSvc := NewRatingService(nil, &stubRatingRepoForLB{}, &stubChangeRepoForLB{}, groupRepo, auditSvc, noopLogger())

	job := NewPostLeaderboardJob(api, groupRepo, &stubGameRepoForLB{}, &stubResultRepoForLB{}, ratingSvc, time.UTC, noopLogger())
	job.run(true) // force=true still respects the toggle

	if api.sentCount != 0 {
		t.Errorf("expected no messages sent for disabled group (force), got %d", api.sentCount)
	}
}

// TestFormatLeaderboard_DeltaShownOnlyWhenRequested — the ▲/▼ delta is rendered
// only when showDelta is true (i.e. for the "yesterday" candidate day), and
// suppressed for older catch-up days whose run-day delta would be misleading.
func TestFormatLeaderboard_DeltaShownOnlyWhenRequested(t *testing.T) {
	firstName := "Alice"
	entries := []LeaderboardEntry{
		{Rank: 1, Player: &models.Player{ID: 1, FirstName: &firstName}, Rating: 1600, GamesPlayed: 5, DeltaToday: 12},
	}
	day := utcDay(-1)

	with := formatLeaderboard(entries, day, time.UTC, true)
	if !strings.Contains(with, "▲") {
		t.Errorf("expected delta arrow when showDelta=true, got %q", with)
	}

	without := formatLeaderboard(entries, day, time.UTC, false)
	if strings.Contains(without, "▲") || strings.Contains(without, "▼") {
		t.Errorf("expected no delta arrow when showDelta=false, got %q", without)
	}
}

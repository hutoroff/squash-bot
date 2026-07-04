package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

// ── mock GameResultRepository ────────────────────────────────────────────────

type stubResultRepo struct {
	created   []*models.GameResult
	createID  int64
	createErr error

	getByIDResult *models.GameResult
	getByIDErr    error

	decideCalls []models.GameResultStatus
	decideErr   error
}

func (r *stubResultRepo) Create(_ context.Context, res *models.GameResult) (int64, error) {
	r.created = append(r.created, res)
	if r.createErr != nil {
		return 0, r.createErr
	}
	r.createID++
	return r.createID, nil
}

func (r *stubResultRepo) GetByID(_ context.Context, _ int64) (*models.GameResult, error) {
	return r.getByIDResult, r.getByIDErr
}

func (r *stubResultRepo) SetApprovalMessage(_ context.Context, _, _ int64, _ int) error {
	return nil
}

func (r *stubResultRepo) Decide(_ context.Context, _ int64, status models.GameResultStatus, _ time.Time) error {
	r.decideCalls = append(r.decideCalls, status)
	return r.decideErr
}

func (r *stubResultRepo) DecideInTx(_ context.Context, _ pgx.Tx, _ int64, status models.GameResultStatus, _ time.Time) error {
	r.decideCalls = append(r.decideCalls, status)
	return r.decideErr
}

func (r *stubResultRepo) ListPendingOlderThan(_ context.Context, _ time.Time) ([]*models.GameResult, error) {
	return nil, nil
}

func (r *stubResultRepo) ListByGroupAndDate(_ context.Context, _ int64, _ time.Time) ([]*models.GameResult, error) {
	return nil, nil
}

func (r *stubResultRepo) ListByGameID(_ context.Context, _ int64) ([]*models.GameResult, error) {
	return nil, nil
}

// ── mock PlayerRepository ────────────────────────────────────────────────────

type stubPlayerRepo struct {
	byTgID    map[int64]*models.Player // telegramID → Player
	byID      map[int64]*models.Player // DB id → Player
	upsertErr error
}

func (r *stubPlayerRepo) Upsert(_ context.Context, p *models.Player) (*models.Player, error) {
	return p, r.upsertErr
}

func (r *stubPlayerRepo) GetByTelegramID(_ context.Context, tgID int64) (*models.Player, error) {
	p, ok := r.byTgID[tgID]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return p, nil
}

func (r *stubPlayerRepo) GetByID(_ context.Context, id int64) (*models.Player, error) {
	p, ok := r.byID[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return p, nil
}

// ── mock ParticipationRepository ─────────────────────────────────────────────

type grPartRepo struct {
	participations []*models.GameParticipation
}

func (r *grPartRepo) Upsert(_ context.Context, _, _ int64, _ models.ParticipationStatus) error {
	return nil
}

func (r *grPartRepo) GetByGame(_ context.Context, _ int64) ([]*models.GameParticipation, error) {
	return r.participations, nil
}

func (r *grPartRepo) DeleteByGameAndPlayer(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}

func (r *grPartRepo) GetRegisteredCount(_ context.Context, _ int64) (int, error) {
	return 0, nil
}

// ── mock GameRepository (lightweight, only GetByID needed) ───────────────────

type stubGameRepoForResults struct {
	game        *models.Game
	err         error
	notInWindow bool // when true, GameInResultWindow reports the game is outside the window
}

func (r *stubGameRepoForResults) Create(_ context.Context, g *models.Game) (*models.Game, error) {
	return g, nil
}
func (r *stubGameRepoForResults) GetByID(_ context.Context, _ int64) (*models.Game, error) {
	return r.game, r.err
}
func (r *stubGameRepoForResults) UpdateMessageID(_ context.Context, _, _ int64) error { return nil }
func (r *stubGameRepoForResults) GetUncompletedGamesByGroupAndDay(_ context.Context, _ int64, _, _ time.Time) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForResults) GetCompletedGamesByGroupAndDay(_ context.Context, _ int64, _, _ time.Time) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForResults) GetUpcomingGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForResults) GetUpcomingGamesByChatIDs(_ context.Context, _ []int64) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForResults) UpdateCourts(_ context.Context, _ int64, _ string, _ int) error {
	return nil
}
func (r *stubGameRepoForResults) GetNextGameForTelegramUser(_ context.Context, _ int64) (*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForResults) GetGamesForPlayer(_ context.Context, _ int64) ([]models.PlayerGame, error) {
	return nil, nil
}
func (r *stubGameRepoForResults) GetRecentCompletedGamesForPlayer(_ context.Context, _, _ int64, _ int) ([]models.PlayerGame, error) {
	return nil, nil
}
func (r *stubGameRepoForResults) GameInResultWindow(_ context.Context, _ int64, _ int) (bool, error) {
	return !r.notInWindow, nil
}
func (r *stubGameRepoForResults) GetUpcomingUnnotifiedGames(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForResults) MarkNotifiedDayBefore(_ context.Context, _ int64) error { return nil }
func (r *stubGameRepoForResults) MarkCompleted(_ context.Context, _ int64) error         { return nil }
func (r *stubGameRepoForResults) ListGroupIDsForPlayer(_ context.Context, _ int64) ([]int64, error) {
	return nil, nil
}
func (r *stubGameRepoForResults) GetUpcomingGamesForFinalCheck(_ context.Context) ([]*models.Game, error) {
	return nil, nil
}
func (r *stubGameRepoForResults) MarkFinalCourtCheckDone(_ context.Context, _ int64) error {
	return nil
}
func (r *stubGameRepoForResults) PlayerCanAccessGame(_ context.Context, _, _ int64) (bool, error) {
	return false, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func int64Ptr(v int64) *int64 { return &v }

// newResultSvc builds a GameResultService wired to the given stubs.
func newResultSvc(
	resultRepo *stubResultRepo,
	gameRepo *stubGameRepoForResults,
	playerRepo *stubPlayerRepo,
	partRepo *grPartRepo,
) *GameResultService {
	auditSvc, _ := newCaptureAuditSvc()
	return NewGameResultService(nil, resultRepo, gameRepo, playerRepo, partRepo, auditSvc, 14, nil)
}

// defaultFixture returns a ready-to-use set of stubs where author (tg=100, id=1)
// and opponent (tg=200, id=2) are both registered in game 10 (group chatID=-1001).
func defaultFixture() (
	*stubResultRepo,
	*stubGameRepoForResults,
	*stubPlayerRepo,
	*grPartRepo,
) {
	resultRepo := &stubResultRepo{}
	gameRepo := &stubGameRepoForResults{
		game: &models.Game{ID: 10, ChatID: -1001},
	}
	playerRepo := &stubPlayerRepo{
		byTgID: map[int64]*models.Player{
			100: {ID: 1, TelegramID: 100},
			200: {ID: 2, TelegramID: 200},
		},
		byID: map[int64]*models.Player{
			1: {ID: 1, TelegramID: 100},
			2: {ID: 2, TelegramID: 200},
		},
	}
	partRepo := &grPartRepo{
		participations: []*models.GameParticipation{
			{GameID: 10, PlayerID: 1, Status: models.StatusRegistered},
			{GameID: 10, PlayerID: 2, Status: models.StatusRegistered},
		},
	}
	return resultRepo, gameRepo, playerRepo, partRepo
}

// ── Submit tests ─────────────────────────────────────────────────────────────

func TestSubmit_BadScoreFormat(t *testing.T) {
	rr, gr, pr, pp := defaultFixture()
	svc := newResultSvc(rr, gr, pr, pp)

	for _, bad := range []string{"abc", "3-1", "3", ":"} {
		_, err := svc.Submit(context.Background(), 10, 100, 2, int64Ptr(1), bad, "@alice")
		if !errors.Is(err, ErrGameResultBadScore) {
			t.Errorf("score %q: got %v, want ErrGameResultBadScore", bad, err)
		}
	}
}

func TestSubmit_WinnerNotInGame(t *testing.T) {
	rr, gr, pr, pp := defaultFixture()
	svc := newResultSvc(rr, gr, pr, pp)

	// winnerID=999 is neither author(1) nor opponent(2)
	_, err := svc.Submit(context.Background(), 10, 100, 2, int64Ptr(999), "3:1", "@alice")
	if !errors.Is(err, ErrGameResultBadScore) {
		t.Errorf("got %v, want ErrGameResultBadScore", err)
	}
}

func TestSubmit_DrawNilWinner_Success(t *testing.T) {
	rr, gr, pr, pp := defaultFixture()
	svc := newResultSvc(rr, gr, pr, pp)

	res, err := svc.Submit(context.Background(), 10, 100, 2, nil, "2:2", "@alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.WinnerID != nil {
		t.Errorf("WinnerID: got %v, want nil", res.WinnerID)
	}
	if res.Status != models.GameResultPending {
		t.Errorf("Status: got %q, want %q", res.Status, models.GameResultPending)
	}
}

func TestSubmit_AuthorEqualsOpponent(t *testing.T) {
	rr, gr, pr, pp := defaultFixture()
	svc := newResultSvc(rr, gr, pr, pp)

	// author tg=100 → player ID=1; opponent player ID=1 → same player
	_, err := svc.Submit(context.Background(), 10, 100, 1, nil, "", "@alice")
	if !errors.Is(err, ErrGameResultSamePlayer) {
		t.Errorf("got %v, want ErrGameResultSamePlayer", err)
	}
}

func TestSubmit_ValidSubmission(t *testing.T) {
	rr, gr, pr, pp := defaultFixture()
	svc := newResultSvc(rr, gr, pr, pp)

	res, err := svc.Submit(context.Background(), 10, 100, 2, int64Ptr(1), "3:1", "@alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AutoApproveAt == nil {
		t.Fatal("AutoApproveAt must be set")
	}
	if res.AutoApproveAt.Before(res.SubmittedAt.Add(autoApproveWindow - time.Second)) {
		t.Errorf("AutoApproveAt too early: %v", res.AutoApproveAt)
	}
	if res.AuthorID != 1 {
		t.Errorf("AuthorID: got %d, want 1", res.AuthorID)
	}
	if res.OpponentID != 2 {
		t.Errorf("OpponentID: got %d, want 2", res.OpponentID)
	}
	if res.Author == nil || res.Author.TelegramID != 100 {
		t.Errorf("Author: got %v, want TelegramID=100", res.Author)
	}
	if res.Opponent == nil || res.Opponent.TelegramID != 200 {
		t.Errorf("Opponent: got %v, want TelegramID=200", res.Opponent)
	}
	if len(rr.created) != 1 {
		t.Fatalf("expected 1 Create call, got %d", len(rr.created))
	}
}

func TestSubmit_OpponentNotRegistered(t *testing.T) {
	rr, gr, pr, pp := defaultFixture()
	// Add a third player who exists but is NOT in the game participations.
	pr.byTgID[300] = &models.Player{ID: 3, TelegramID: 300}
	pr.byID[3] = &models.Player{ID: 3, TelegramID: 300}
	svc := newResultSvc(rr, gr, pr, pp)

	// opponent=3 is a valid player but not registered in game 10
	_, err := svc.Submit(context.Background(), 10, 100, 3, nil, "", "@alice")
	if !errors.Is(err, ErrGameResultNotInGame) {
		t.Errorf("got %v, want ErrGameResultNotInGame", err)
	}
}

func TestSubmit_OutsideWindow(t *testing.T) {
	rr, gr, pr, pp := defaultFixture()
	gr.notInWindow = true // game is too old / in the future
	svc := newResultSvc(rr, gr, pr, pp)

	_, err := svc.Submit(context.Background(), 10, 100, 2, int64Ptr(1), "3:1", "@alice")
	if !errors.Is(err, ErrGameResultWindowClosed) {
		t.Errorf("got %v, want ErrGameResultWindowClosed", err)
	}
	if len(rr.created) != 0 {
		t.Errorf("no result should be created when outside the window, got %d", len(rr.created))
	}
}

// ── Score validation (table-driven) ──────────────────────────────────────────

func TestValidateScore(t *testing.T) {
	const authorID, opponentID int64 = 1, 2

	tests := []struct {
		name    string
		score   string
		winner  *int64
		wantErr error
	}{
		{
			name:    "empty score is valid",
			score:   "",
			winner:  nil,
			wantErr: nil,
		},
		{
			name:    "3:1 winner=author valid",
			score:   "3:1",
			winner:  int64Ptr(authorID),
			wantErr: nil,
		},
		{
			name:    "1:3 winner=author invalid (1 < 3)",
			score:   "1:3",
			winner:  int64Ptr(authorID),
			wantErr: ErrGameResultBadScore,
		},
		{
			name:    "3:1 winner=opponent invalid (opponent side is 1)",
			score:   "3:1",
			winner:  int64Ptr(opponentID),
			wantErr: ErrGameResultBadScore,
		},
		{
			name:    "2:2 draw (nil winner) valid",
			score:   "2:2",
			winner:  nil,
			wantErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateScore(tc.score, authorID, opponentID, tc.winner)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("validateScore(%q, winner=%v): got %v, want %v",
					tc.score, tc.winner, err, tc.wantErr)
			}
		})
	}
}

// ── Approve / Reject / Cancel transitions ────────────────────────────────────

func pendingResult() *models.GameResult {
	return &models.GameResult{
		ID:         1,
		GameID:     10,
		GroupID:    -1001,
		AuthorID:   1,
		OpponentID: 2,
		Status:     models.GameResultPending,
	}
}

func TestApprove_ByOpponent_Success(t *testing.T) {
	rr, gr, pr, pp := defaultFixture()
	rr.getByIDResult = pendingResult()
	svc := newResultSvc(rr, gr, pr, pp)

	res, err := svc.Approve(context.Background(), 1, 200, "@bob")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != models.GameResultApproved {
		t.Errorf("Status: got %q, want %q", res.Status, models.GameResultApproved)
	}
	if len(rr.decideCalls) != 1 || rr.decideCalls[0] != models.GameResultApproved {
		t.Errorf("Decide calls: %v", rr.decideCalls)
	}
	if res.Author == nil || res.Author.TelegramID != 100 {
		t.Errorf("Author after Approve: got %v, want TelegramID=100", res.Author)
	}
	if res.Opponent == nil || res.Opponent.TelegramID != 200 {
		t.Errorf("Opponent after Approve: got %v, want TelegramID=200", res.Opponent)
	}
}

func TestReject_ByOpponent_Success(t *testing.T) {
	rr, gr, pr, pp := defaultFixture()
	rr.getByIDResult = pendingResult()
	svc := newResultSvc(rr, gr, pr, pp)

	res, err := svc.Reject(context.Background(), 1, 200, "@bob")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != models.GameResultRejected {
		t.Errorf("Status: got %q, want %q", res.Status, models.GameResultRejected)
	}
	if res.Author == nil || res.Author.TelegramID != 100 {
		t.Errorf("Author after Reject: got %v, want TelegramID=100", res.Author)
	}
	if res.Opponent == nil || res.Opponent.TelegramID != 200 {
		t.Errorf("Opponent after Reject: got %v, want TelegramID=200", res.Opponent)
	}
}

func TestCancelByAuthor_Success(t *testing.T) {
	rr, gr, pr, pp := defaultFixture()
	rr.getByIDResult = pendingResult()
	svc := newResultSvc(rr, gr, pr, pp)

	res, err := svc.CancelByAuthor(context.Background(), 1, 100, "@alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != models.GameResultCanceled {
		t.Errorf("Status: got %q, want %q", res.Status, models.GameResultCanceled)
	}
	if res.Author == nil || res.Author.TelegramID != 100 {
		t.Errorf("Author after Cancel: got %v, want TelegramID=100", res.Author)
	}
	if res.Opponent == nil || res.Opponent.TelegramID != 200 {
		t.Errorf("Opponent after Cancel: got %v, want TelegramID=200", res.Opponent)
	}
}

func TestGet_EnrichesAuthorOpponent(t *testing.T) {
	rr, gr, pr, pp := defaultFixture()
	rr.getByIDResult = pendingResult()
	svc := newResultSvc(rr, gr, pr, pp)

	res, err := svc.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Author == nil || res.Author.TelegramID != 100 {
		t.Errorf("Author from Get: got %v, want TelegramID=100", res.Author)
	}
	if res.Opponent == nil || res.Opponent.TelegramID != 200 {
		t.Errorf("Opponent from Get: got %v, want TelegramID=200", res.Opponent)
	}
}

func TestApprove_ByWrongPerson_Forbidden(t *testing.T) {
	rr, gr, pr, pp := defaultFixture()
	rr.getByIDResult = pendingResult()
	svc := newResultSvc(rr, gr, pr, pp)

	// tg=100 is the author (player id=1), not the opponent → forbidden
	_, err := svc.Approve(context.Background(), 1, 100, "@alice")
	if !errors.Is(err, ErrGameResultForbidden) {
		t.Errorf("got %v, want ErrGameResultForbidden", err)
	}
}

func TestApprove_NonPending_Error(t *testing.T) {
	rr, gr, pr, pp := defaultFixture()
	rr.getByIDResult = pendingResult()
	rr.decideErr = storage.ErrGameResultNotPending
	svc := newResultSvc(rr, gr, pr, pp)

	_, err := svc.Approve(context.Background(), 1, 200, "@bob")
	if !errors.Is(err, storage.ErrGameResultNotPending) {
		t.Errorf("got %v, want ErrGameResultNotPending", err)
	}
}

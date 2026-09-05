//go:build integration

package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/cmd/management/service/rating"
	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/featureflags"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/testutil"
	"github.com/jackc/pgx/v5"
)

type ratingFixture struct {
	svc              *service.GameResultService
	ratings          *service.RatingService
	repo             *storage.GameResultRepo
	changes          *storage.RatingChangeRepo
	flags            *storage.FeatureFlagRepo
	flagSvc          *service.FeatureFlagService
	scheduler        *service.Scheduler
	result           *models.GameResult
	author, opponent *models.Player
}
type inertRatingTelegram struct{ service.TelegramAPI }

func (inertRatingTelegram) Send(tgbotapi.Chattable) (tgbotapi.Message, error) {
	return tgbotapi.Message{}, nil
}
func (inertRatingTelegram) Request(tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{Ok: true}, nil
}

func setupRatingFixture(t *testing.T, score string, kind models.ScoreKind) *ratingFixture {
	t.Helper()
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	users := storage.NewUserRepo(testPool)
	players := storage.NewPlayerRepo(testPool)
	au := mustCreateUser(t, ctx, 701, "author")
	ou := mustCreateUser(t, ctx, 702, "opponent")
	if err := users.SetServerOwner(ctx, au, true); err != nil {
		t.Fatal(err)
	}
	a, err := players.Upsert(ctx, au)
	if err != nil {
		t.Fatal(err)
	}
	b, err := players.Upsert(ctx, ou)
	if err != nil {
		t.Fatal(err)
	}
	groups := storage.NewGroupRepo(testPool)
	if err := groups.Upsert(ctx, -701, "Rating test", true); err != nil {
		t.Fatal(err)
	}
	games := storage.NewGameRepo(testPool)
	game, err := games.Create(ctx, &models.Game{ChatID: -701, GameDate: time.Now().Add(-24 * time.Hour), Courts: "1", CourtsCount: 1, PlayersPerCourt: 2, Sport: "table_tennis"})
	if err != nil {
		t.Fatal(err)
	}
	parts := storage.NewParticipationRepo(testPool)
	for _, p := range []*models.Player{a, b} {
		if err := parts.Upsert(ctx, game.ID, p.ID, models.StatusRegistered); err != nil {
			t.Fatal(err)
		}
	}
	audit := service.NewAuditService(storage.NewAuditEventRepo(testPool), logger)
	repo := storage.NewGameResultRepo(testPool)
	changes := storage.NewRatingChangeRepo(testPool)
	flags := storage.NewFeatureFlagRepo(testPool)
	ratings := service.NewRatingService(testPool, storage.NewPlayerRatingRepo(testPool), changes, groups, audit, logger)
	ratings.SetFeatureFlags(flags)
	svc := service.NewGameResultService(testPool, repo, games, players, parts, audit, 14, users)
	svc.SetRatingService(ratings)
	res, err := svc.Submit(ctx, game.ID, au, b.ID, &a.ID, score, "author", kind)
	if err != nil {
		t.Fatal(err)
	}
	// Make it eligible for the real auto-approval query as well.
	if _, err := testPool.Exec(ctx, `UPDATE game_results SET submitted_at = NOW() - INTERVAL '49 hours' WHERE id=$1`, res.ID); err != nil {
		t.Fatal(err)
	}
	job := service.NewAutoApproveResultsJob(inertRatingTelegram{}, testPool, repo, players, audit, logger)
	job.SetRatingService(ratings)
	return &ratingFixture{svc, ratings, repo, changes, flags, service.NewFeatureFlagService(flags, users, groups, audit), service.NewScheduler(logger, job), res, a, b}
}
func (f *ratingFixture) approve(t *testing.T, auto bool) {
	t.Helper()
	if auto {
		f.scheduler.RunScheduledTasks()
		return
	}
	if _, err := f.svc.Approve(context.Background(), f.result.ID, f.opponent.UserID, "opponent"); err != nil {
		t.Fatal(err)
	}
}
func (f *ratingFixture) assertState(t *testing.T, pending bool, weight float64, reason string) {
	t.Helper()
	ctx := context.Background()
	res, err := f.repo.GetByID(ctx, f.result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if (res.Status == models.GameResultPending) != pending {
		t.Fatalf("status %s", res.Status)
	}
	var ratingCount, changeCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM player_ratings`).Scan(&ratingCount); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM rating_changes`).Scan(&changeCount); err != nil {
		t.Fatal(err)
	}
	if pending {
		if ratingCount != 0 || changeCount != 0 {
			t.Fatalf("partial commit: %d ratings, %d history", ratingCount, changeCount)
		}
		return
	}
	if ratingCount != 2 || changeCount != 2 {
		t.Fatalf("counts %d/%d", ratingCount, changeCount)
	}
	changes, err := f.changes.ListByGroupAndDateRange(ctx, -701, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		version := "glicko2-v1"
		if reason == "typed_score" {
			version = "glicko2-score-v1"
		}
		if c.EvidenceWeight != weight || c.PolicyReason != reason || c.ScoreKind != f.result.ScoreKind || c.PolicyVersion != version || c.ScoreAwareEnabled != (reason != "disabled") {
			t.Fatalf("history %+v", c)
		}
		outcome := 0.0
		if c.PlayerID == f.author.ID {
			outcome = 1
		}
		expected := rating.Apply(rating.Default(), []rating.MatchResult{{Opponent: rating.Default(), Score: outcome, Weight: weight}}, rating.Tau)
		if math.Abs(c.NewRating-expected.R) > 1e-10 {
			t.Fatalf("actual %v, expected %v", c.NewRating, expected.R)
		}
	}
	var minGames, maxGames int
	if err := testPool.QueryRow(ctx, `SELECT min(games_played), max(games_played) FROM player_ratings`).Scan(&minGames, &maxGames); err != nil {
		t.Fatal(err)
	}
	if minGames != 1 || maxGames != 1 {
		t.Fatal("match counted more than once")
	}
}

func TestRatingPolicy_ApprovalTimeAndCompatibility(t *testing.T) {
	for _, auto := range []bool{false, true} {
		for _, tc := range []struct {
			name, score string
			kind        models.ScoreKind
			enable      bool
			weight      float64
			reason      string
		}{
			{"default off", "11:9", models.ScoreKindPoints, false, 1, "disabled"},
			{"points", "11:9", models.ScoreKindPoints, true, .8, "typed_score"},
			{"games", "3:2", models.ScoreKindGames, true, .85, "typed_score"},
			{"skip", "", "", true, 1, "missing_score"},
			{"old client", "3:2", "", true, 1, "unknown_score_kind"},
		} {
			t.Run(tc.name+map[bool]string{false: "/manual", true: "/auto"}[auto], func(t *testing.T) {
				f := setupRatingFixture(t, tc.score, tc.kind)
				got, err := f.repo.GetByID(context.Background(), f.result.ID)
				if err != nil || got.ScoreKind != tc.kind {
					t.Fatalf("score kind roundtrip: %+v %v", got, err)
				}
				if tc.enable {
					group := int64(-701)
					yes := true
					if err := f.flagSvc.Set(context.Background(), f.author.UserID, "author", featureflags.ScoreAwareRating, &group, &yes); err != nil {
						t.Fatal(err)
					}
				}
				f.approve(t, auto)
				f.assertState(t, false, tc.weight, tc.reason)
				// No replay of an already decided result, even when the flag changes.
				no := false
				group := int64(-701)
				if _, err := f.flags.Set(context.Background(), featureflags.ScoreAwareRating, &group, &no); err != nil {
					t.Fatal(err)
				}
				if _, err := f.svc.Approve(context.Background(), f.result.ID, f.opponent.UserID, "opponent"); !errors.Is(err, storage.ErrGameResultNotPending) {
					t.Fatalf("duplicate decision: %v", err)
				}
				f.scheduler.RunScheduledTasks()
				f.assertState(t, false, tc.weight, tc.reason)
			})
		}
	}
}

type failingRatingFlags struct{}

func (failingRatingFlags) EnabledInTx(context.Context, pgx.Tx, featureflags.Key, int64) (bool, error) {
	return false, errors.New("flag read failed")
}

type secondHistoryFailure struct {
	*storage.RatingChangeRepo
	n int
}

func (f *secondHistoryFailure) InsertInTx(ctx context.Context, tx pgx.Tx, c *models.RatingChange) error {
	f.n++
	if f.n == 2 {
		return errors.New("second history write failed")
	}
	return f.RatingChangeRepo.InsertInTx(ctx, tx, c)
}
func TestRatingPolicy_AtomicRollback(t *testing.T) {
	for _, auto := range []bool{false, true} {
		for _, failure := range []string{"flag", "second_history"} {
			t.Run(failure+map[bool]string{false: "/manual", true: "/auto"}[auto], func(t *testing.T) {
				f := setupRatingFixture(t, "11:9", models.ScoreKindPoints)
				if failure == "flag" {
					f.ratings.SetFeatureFlags(failingRatingFlags{})
				} else {
					// Use a real first insert and fail after ratings were written; every write must roll back.
					logger := slog.New(slog.NewTextHandler(io.Discard, nil))
					audit := service.NewAuditService(storage.NewAuditEventRepo(testPool), logger)
					broken := service.NewRatingService(testPool, storage.NewPlayerRatingRepo(testPool), &secondHistoryFailure{RatingChangeRepo: f.changes}, storage.NewGroupRepo(testPool), audit, logger)
					broken.SetFeatureFlags(f.flags)
					f.svc.SetRatingService(broken)
					job := service.NewAutoApproveResultsJob(inertRatingTelegram{}, testPool, f.repo, storage.NewPlayerRepo(testPool), audit, logger)
					job.SetRatingService(broken)
					f.scheduler = service.NewScheduler(logger, job)
				}
				if auto {
					f.scheduler.RunScheduledTasks()
				} else {
					if _, err := f.svc.Approve(context.Background(), f.result.ID, f.opponent.UserID, "opponent"); err == nil {
						t.Fatal("failure not propagated")
					}
				}
				f.assertState(t, true, 0, "")
				f.ratings.SetFeatureFlags(f.flags)
				f.svc.SetRatingService(f.ratings)
				f.approve(t, false)
				f.assertState(t, false, 1, "disabled")
			})
		}
	}
}
func TestRatingPolicy_ConcurrentManualAndAuto(t *testing.T) {
	f := setupRatingFixture(t, "11:9", models.ScoreKindPoints)
	yes := true
	if _, err := f.flags.Set(context.Background(), featureflags.ScoreAwareRating, nil, &yes); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				f.scheduler.RunScheduledTasks()
			} else {
				_, err := f.svc.Approve(context.Background(), f.result.ID, f.opponent.UserID, "opponent")
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, storage.ErrGameResultNotPending) {
			t.Fatal(err)
		}
	}
	f.assertState(t, false, .8, "typed_score")
}

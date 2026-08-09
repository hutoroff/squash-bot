//go:build integration

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
)

func seedRatingDeps(t *testing.T, ctx context.Context) (groupChatID, playerID int64) {
	t.Helper()
	groupRepo := storage.NewGroupRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)

	groupChatID = -8001
	if err := groupRepo.Upsert(ctx, groupChatID, "Rating Group", true); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	p := mustCreatePlayer(t, ctx, playerRepo, 333, "")
	return groupChatID, p.ID
}

func TestPlayerRatingRepo_GetOrInit_CreatesDefaults(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	groupID, playerID := seedRatingDeps(t, ctx)
	repo := storage.NewPlayerRatingRepo(testPool)

	pr, err := repo.GetOrInit(ctx, groupID, playerID)
	if err != nil {
		t.Fatalf("GetOrInit: %v", err)
	}
	if pr.Rating != 1500 {
		t.Errorf("Rating: got %.2f, want 1500", pr.Rating)
	}
	if pr.RD != 350 {
		t.Errorf("RD: got %.2f, want 350", pr.RD)
	}
	if pr.Volatility != 0.06 {
		t.Errorf("Volatility: got %.4f, want 0.06", pr.Volatility)
	}
	if pr.GamesPlayed != 0 {
		t.Errorf("GamesPlayed: got %d, want 0", pr.GamesPlayed)
	}
}

func TestPlayerRatingRepo_GetOrInit_ReturnsExisting(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	groupID, playerID := seedRatingDeps(t, ctx)
	repo := storage.NewPlayerRatingRepo(testPool)

	// Init defaults.
	repo.GetOrInit(ctx, groupID, playerID)

	// Update rating.
	now := time.Now().UTC()
	repo.Upsert(ctx, &models.PlayerRating{
		GroupID: groupID, PlayerID: playerID,
		Rating: 1600, RD: 200, Volatility: 0.05, GamesPlayed: 5, UpdatedAt: now,
	})

	// GetOrInit should return the updated values.
	pr, err := repo.GetOrInit(ctx, groupID, playerID)
	if err != nil {
		t.Fatalf("GetOrInit after update: %v", err)
	}
	if pr.Rating != 1600 {
		t.Errorf("Rating: got %.2f, want 1600", pr.Rating)
	}
	if pr.GamesPlayed != 5 {
		t.Errorf("GamesPlayed: got %d, want 5", pr.GamesPlayed)
	}
}

func TestPlayerRatingRepo_ListByGroup(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	groupRepo := storage.NewGroupRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	repo := storage.NewPlayerRatingRepo(testPool)

	groupID := int64(-8002)
	groupRepo.Upsert(ctx, groupID, "LB Group", true)

	p1 := mustCreatePlayer(t, ctx, playerRepo, 444, "")
	p2 := mustCreatePlayer(t, ctx, playerRepo, 555, "")

	now := time.Now().UTC()
	repo.Upsert(ctx, &models.PlayerRating{GroupID: groupID, PlayerID: p1.ID, Rating: 1600, RD: 200, Volatility: 0.05, GamesPlayed: 3, UpdatedAt: now})
	repo.Upsert(ctx, &models.PlayerRating{GroupID: groupID, PlayerID: p2.ID, Rating: 1400, RD: 250, Volatility: 0.06, GamesPlayed: 2, UpdatedAt: now})

	list, err := repo.ListByGroup(ctx, groupID)
	if err != nil {
		t.Fatalf("ListByGroup: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 ratings, got %d", len(list))
	}
	if list[0].Rating != 1600 {
		t.Errorf("first entry: got rating %.2f, want 1600 (sorted by rating DESC)", list[0].Rating)
	}
	if list[0].Player == nil {
		t.Error("Player should be joined in ListByGroup")
	}
}

// ── RatingChangeRepo ──────────────────────────────────────────────────────────

func TestRatingChangeRepo_InsertAndList(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	groupID, gameID, p1, _ := seedGameResultDeps(t, ctx)
	resultRepo := storage.NewGameResultRepo(testPool)

	resID, _ := resultRepo.Create(ctx, &models.GameResult{
		GameID: gameID, GroupID: groupID, AuthorID: p1, OpponentID: p1 + 1, // use p2 from seedGameResultDeps
		Status: models.GameResultPending, SubmittedAt: time.Now().UTC(),
	})

	changeRepo := storage.NewRatingChangeRepo(testPool)
	now := time.Now().UTC()
	change := &models.RatingChange{
		GameResultID: resID, GroupID: groupID, PlayerID: p1,
		OldRating: 1500, NewRating: 1530, OldRD: 350, NewRD: 300, Delta: 30, AppliedAt: now,
	}
	if err := changeRepo.Insert(ctx, change); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	from := now.Add(-1 * time.Hour)
	to := now.Add(1 * time.Hour)
	changes, err := changeRepo.ListByGroupAndDateRange(ctx, groupID, from, to)
	if err != nil {
		t.Fatalf("ListByGroupAndDateRange: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Delta != 30 {
		t.Errorf("Delta: got %.2f, want 30", changes[0].Delta)
	}
}

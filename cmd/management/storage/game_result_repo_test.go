//go:build integration

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
)

func seedGameResultDeps(t *testing.T, ctx context.Context) (groupChatID int64, gameID, player1ID, player2ID int64) {
	t.Helper()
	groupRepo := storage.NewGroupRepo(testPool)
	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)

	groupChatID = -9001
	if err := groupRepo.Upsert(ctx, groupChatID, "Test Group", true); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	game, err := gameRepo.Create(ctx, &models.Game{
		ChatID:      groupChatID,
		GameDate:    time.Now().Add(-24 * time.Hour),
		Courts:      "1,2",
		CourtsCount: 2,
	})
	if err != nil {
		t.Fatalf("seed game: %v", err)
	}
	gameID = game.ID

	p1 := mustCreatePlayer(t, ctx, playerRepo, 111, "")
	if err != nil {
		t.Fatalf("seed player1: %v", err)
	}
	p2 := mustCreatePlayer(t, ctx, playerRepo, 222, "")
	if err != nil {
		t.Fatalf("seed player2: %v", err)
	}
	return groupChatID, gameID, p1.ID, p2.ID
}

func TestGameResultRepo_CreateAndGetByID(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	groupID, gameID, p1, p2 := seedGameResultDeps(t, ctx)
	repo := storage.NewGameResultRepo(testPool)

	winnerID := p1
	res := &models.GameResult{
		GameID:      gameID,
		GroupID:     groupID,
		AuthorID:    p1,
		OpponentID:  p2,
		WinnerID:    &winnerID,
		Score:       "3:1",
		Status:      models.GameResultPending,
		SubmittedAt: time.Now().UTC(),
	}
	id, err := repo.Create(ctx, res)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Score != "3:1" {
		t.Errorf("Score: got %q, want %q", got.Score, "3:1")
	}
	if got.Status != models.GameResultPending {
		t.Errorf("Status: got %q, want %q", got.Status, models.GameResultPending)
	}
	if got.WinnerID == nil || *got.WinnerID != p1 {
		t.Errorf("WinnerID: got %v, want %d", got.WinnerID, p1)
	}
}

func TestGameResultRepo_Decide_Transitions(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	groupID, gameID, p1, p2 := seedGameResultDeps(t, ctx)
	repo := storage.NewGameResultRepo(testPool)

	cases := []struct {
		name   string
		status models.GameResultStatus
	}{
		{"approved", models.GameResultApproved},
		{"rejected", models.GameResultRejected},
		{"auto_approved", models.GameResultAutoApproved},
		{"canceled", models.GameResultCanceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := repo.Create(ctx, &models.GameResult{
				GameID: gameID, GroupID: groupID, AuthorID: p1, OpponentID: p2,
				Status: models.GameResultPending, SubmittedAt: time.Now().UTC(),
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			now := time.Now().UTC()
			if err := repo.Decide(ctx, id, tc.status, now); err != nil {
				t.Fatalf("Decide: %v", err)
			}
			got, _ := repo.GetByID(ctx, id)
			if got.Status != tc.status {
				t.Errorf("Status: got %q, want %q", got.Status, tc.status)
			}
			if got.DecidedAt == nil {
				t.Error("DecidedAt should be set after Decide")
			}
		})
	}
}

func TestGameResultRepo_Decide_RejectsNonPending(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	groupID, gameID, p1, p2 := seedGameResultDeps(t, ctx)
	repo := storage.NewGameResultRepo(testPool)

	id, _ := repo.Create(ctx, &models.GameResult{
		GameID: gameID, GroupID: groupID, AuthorID: p1, OpponentID: p2,
		Status: models.GameResultPending, SubmittedAt: time.Now().UTC(),
	})
	_ = repo.Decide(ctx, id, models.GameResultApproved, time.Now().UTC())

	err := repo.Decide(ctx, id, models.GameResultRejected, time.Now().UTC())
	if err != storage.ErrGameResultNotPending {
		t.Errorf("expected ErrGameResultNotPending, got %v", err)
	}
}

func TestGameResultRepo_ListPendingOlderThan(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	groupID, gameID, p1, p2 := seedGameResultDeps(t, ctx)
	repo := storage.NewGameResultRepo(testPool)

	old := time.Now().UTC().Add(-72 * time.Hour)
	recent := time.Now().UTC().Add(-1 * time.Hour)

	repo.Create(ctx, &models.GameResult{
		GameID: gameID, GroupID: groupID, AuthorID: p1, OpponentID: p2,
		Status: models.GameResultPending, SubmittedAt: old,
	})
	repo.Create(ctx, &models.GameResult{
		GameID: gameID, GroupID: groupID, AuthorID: p2, OpponentID: p1,
		Status: models.GameResultPending, SubmittedAt: recent,
	})

	cutoff := time.Now().UTC().Add(-48 * time.Hour)
	results, err := repo.ListPendingOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("ListPendingOlderThan: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 old pending result, got %d", len(results))
	}
	if results[0].AuthorID != p1 {
		t.Errorf("expected old result authored by p1, got author_id=%d", results[0].AuthorID)
	}
}

func TestGameResultRepo_MultipleSamePairSameGame(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	groupID, gameID, p1, p2 := seedGameResultDeps(t, ctx)
	repo := storage.NewGameResultRepo(testPool)

	for i := 0; i < 3; i++ {
		_, err := repo.Create(ctx, &models.GameResult{
			GameID: gameID, GroupID: groupID, AuthorID: p1, OpponentID: p2,
			Score: "3:1", Status: models.GameResultPending, SubmittedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
	}

	results, err := repo.ListByGameID(ctx, gameID)
	if err != nil {
		t.Fatalf("ListByGameID: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results for same pair+game, got %d", len(results))
	}
}

func TestGameResultRepo_SetApprovalMessage(t *testing.T) {
	ctx := context.Background()
	mustTruncate(t)
	groupID, gameID, p1, p2 := seedGameResultDeps(t, ctx)
	repo := storage.NewGameResultRepo(testPool)

	id, _ := repo.Create(ctx, &models.GameResult{
		GameID: gameID, GroupID: groupID, AuthorID: p1, OpponentID: p2,
		Status: models.GameResultPending, SubmittedAt: time.Now().UTC(),
	})

	if err := repo.SetApprovalMessage(ctx, id, 12345, 99); err != nil {
		t.Fatalf("SetApprovalMessage: %v", err)
	}

	got, _ := repo.GetByID(ctx, id)
	if got.ApprovalChatID == nil || *got.ApprovalChatID != 12345 {
		t.Errorf("ApprovalChatID: got %v, want 12345", got.ApprovalChatID)
	}
	if got.ApprovalMessageID == nil || *got.ApprovalMessageID != 99 {
		t.Errorf("ApprovalMessageID: got %v, want 99", got.ApprovalMessageID)
	}
}

//go:build integration

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/testutil"
)

// seedGameAndPlayer creates a game and a player for participation tests.
func seedGameAndPlayer(t *testing.T, ctx context.Context) (gameID, playerID int64) {
	t.Helper()
	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)

	g, err := gameRepo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1,2"))
	if err != nil {
		t.Fatalf("seed game: %v", err)
	}
	p, err := playerRepo.Upsert(ctx, &models.Player{
		TelegramID: 200001,
		Username:   strPtr("testuser"),
	})
	if err != nil {
		t.Fatalf("seed player: %v", err)
	}
	return g.ID, p.ID
}

func TestParticipationRepo_Upsert_Create(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	repo := storage.NewParticipationRepo(testPool)
	gameID, playerID := seedGameAndPlayer(t, ctx)

	if err := repo.Upsert(ctx, gameID, playerID, models.StatusRegistered); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	count, err := repo.GetRegisteredCount(ctx, gameID)
	if err != nil {
		t.Fatalf("GetRegisteredCount: %v", err)
	}
	if count != 1 {
		t.Errorf("registered count: got %d, want 1", count)
	}
}

func TestParticipationRepo_Upsert_UpdatesStatus(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	repo := storage.NewParticipationRepo(testPool)
	gameID, playerID := seedGameAndPlayer(t, ctx)

	_ = repo.Upsert(ctx, gameID, playerID, models.StatusRegistered)
	// Change to skipped via another upsert
	if err := repo.Upsert(ctx, gameID, playerID, models.StatusSkipped); err != nil {
		t.Fatalf("Upsert (change to skipped): %v", err)
	}

	count, _ := repo.GetRegisteredCount(ctx, gameID)
	if count != 0 {
		t.Errorf("registered count after skip: got %d, want 0", count)
	}

	parts, _ := repo.GetByGame(ctx, gameID)
	if len(parts) != 1 {
		t.Fatalf("GetByGame: got %d rows, want 1", len(parts))
	}
	if parts[0].Status != models.StatusSkipped {
		t.Errorf("status: got %q, want %q", parts[0].Status, models.StatusSkipped)
	}
}

func TestParticipationRepo_GetByGame_WithPlayerData(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	partRepo := storage.NewParticipationRepo(testPool)

	g, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1,2"))

	p1, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 201, Username: strPtr("player1")})
	p2, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 202, FirstName: strPtr("John"), LastName: strPtr("Doe")})

	_ = partRepo.Upsert(ctx, g.ID, p1.ID, models.StatusRegistered)
	_ = partRepo.Upsert(ctx, g.ID, p2.ID, models.StatusSkipped)

	parts, err := partRepo.GetByGame(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetByGame: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("got %d participations, want 2", len(parts))
	}

	// Verify player data is denormalized into the participation
	for _, p := range parts {
		if p.Player == nil {
			t.Error("Player field is nil, expected denormalized data")
		}
	}

	// First participation should be p1 (registered, ordered by created_at)
	if parts[0].Player.TelegramID != p1.TelegramID {
		t.Errorf("first player TelegramID: got %d, want %d", parts[0].Player.TelegramID, p1.TelegramID)
	}
	if parts[0].Status != models.StatusRegistered {
		t.Errorf("first status: got %q, want registered", parts[0].Status)
	}
	if parts[1].Status != models.StatusSkipped {
		t.Errorf("second status: got %q, want skipped", parts[1].Status)
	}
}

// --- DeleteByGameAndPlayer ---

func TestParticipationRepo_DeleteByGameAndPlayer_Success(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	repo := storage.NewParticipationRepo(testPool)
	gameID, playerID := seedGameAndPlayer(t, ctx)

	_ = repo.Upsert(ctx, gameID, playerID, models.StatusRegistered)

	removed, err := repo.DeleteByGameAndPlayer(ctx, gameID, playerID)
	if err != nil {
		t.Fatalf("DeleteByGameAndPlayer: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}

	count, _ := repo.GetRegisteredCount(ctx, gameID)
	if count != 0 {
		t.Errorf("registered count after deletion: got %d, want 0", count)
	}
}

func TestParticipationRepo_DeleteByGameAndPlayer_NotInGame(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	repo := storage.NewParticipationRepo(testPool)
	gameID, playerID := seedGameAndPlayer(t, ctx)

	removed, err := repo.DeleteByGameAndPlayer(ctx, gameID, playerID)
	if err != nil {
		t.Fatalf("DeleteByGameAndPlayer (not in game): %v", err)
	}
	if removed {
		t.Error("expected removed=false when player is not in the game")
	}
}

func TestParticipationRepo_DeleteByGameAndPlayer_DoesNotAffectOtherPlayers(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	partRepo := storage.NewParticipationRepo(testPool)

	g, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1,2,3"))
	p1, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 210001})
	p2, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 210002})

	_ = partRepo.Upsert(ctx, g.ID, p1.ID, models.StatusRegistered)
	_ = partRepo.Upsert(ctx, g.ID, p2.ID, models.StatusRegistered)

	removed, err := partRepo.DeleteByGameAndPlayer(ctx, g.ID, p1.ID)
	if err != nil {
		t.Fatalf("DeleteByGameAndPlayer: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}

	count, _ := partRepo.GetRegisteredCount(ctx, g.ID)
	if count != 1 {
		t.Errorf("registered count: got %d, want 1 (p2 should remain)", count)
	}
}

func TestParticipationRepo_GetRegisteredCount(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	partRepo := storage.NewParticipationRepo(testPool)

	g, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1,2,3"))

	for i := int64(0); i < 3; i++ {
		p, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 300 + i})
		_ = partRepo.Upsert(ctx, g.ID, p.ID, models.StatusRegistered)
	}
	// One player skips
	skipped, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 399})
	_ = partRepo.Upsert(ctx, g.ID, skipped.ID, models.StatusSkipped)

	count, err := partRepo.GetRegisteredCount(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetRegisteredCount: %v", err)
	}
	if count != 3 {
		t.Errorf("registered count: got %d, want 3", count)
	}
}

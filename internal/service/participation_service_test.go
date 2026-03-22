//go:build integration

package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/vkhutorov/squash_bot/internal/models"
	"github.com/vkhutorov/squash_bot/internal/service"
	"github.com/vkhutorov/squash_bot/internal/storage"
	"github.com/vkhutorov/squash_bot/internal/testutil"
)

// setupParticipationTest creates repos, services and a fresh game for participation tests.
func setupParticipationTest(t *testing.T, ctx context.Context) (
	*service.ParticipationService,
	int64, // gameID
) {
	t.Helper()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}

	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	partRepo := storage.NewParticipationRepo(testPool)

	gameSvc := service.NewGameService(gameRepo)
	partSvc := service.NewParticipationService(playerRepo, partRepo)

	game, err := gameSvc.CreateGame(ctx, -1001, time.Now().Add(48*time.Hour), "1,2")
	if err != nil {
		t.Fatalf("create game: %v", err)
	}
	return partSvc, game.ID
}

func TestParticipationService_Join(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	parts, err := svc.Join(ctx, gameID, 11001, "alice", "Alice", "Wonder")
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("got %d participations, want 1", len(parts))
	}
	if parts[0].Status != models.StatusRegistered {
		t.Errorf("status: got %q, want registered", parts[0].Status)
	}
	if parts[0].Player == nil || parts[0].Player.TelegramID != 11001 {
		t.Error("player data missing or wrong telegram_id")
	}
}

func TestParticipationService_Join_Idempotent(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	// Join twice — should produce exactly one registered participation
	_, _ = svc.Join(ctx, gameID, 11002, "bob", "Bob", "")
	parts, err := svc.Join(ctx, gameID, 11002, "bob", "Bob", "")
	if err != nil {
		t.Fatalf("second Join: %v", err)
	}
	if len(parts) != 1 {
		t.Errorf("got %d participations after double join, want 1", len(parts))
	}
	if parts[0].Status != models.StatusRegistered {
		t.Errorf("status after double join: got %q, want registered", parts[0].Status)
	}
}

func TestParticipationService_Skip_PlayerNotRegistered(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	// Unknown player – should get (nil, false, nil)
	parts, skipped, err := svc.Skip(ctx, gameID, 99999, "nobody", "", "")
	if err != nil {
		t.Fatalf("Skip (unknown player): %v", err)
	}
	if skipped {
		t.Error("skipped should be false for unknown player")
	}
	if parts != nil {
		t.Error("participations should be nil when player is unknown")
	}
}

func TestParticipationService_Skip_Registered(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	// First join, then skip
	_, _ = svc.Join(ctx, gameID, 11003, "carol", "Carol", "King")
	parts, skipped, err := svc.Skip(ctx, gameID, 11003, "carol", "Carol", "King")
	if err != nil {
		t.Fatalf("Skip: %v", err)
	}
	if !skipped {
		t.Error("skipped should be true for a registered player")
	}
	if len(parts) != 1 {
		t.Fatalf("got %d participations, want 1", len(parts))
	}
	if parts[0].Status != models.StatusSkipped {
		t.Errorf("status: got %q, want skipped", parts[0].Status)
	}
}

func TestParticipationService_Skip_AlreadySkipped(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	_, _ = svc.Join(ctx, gameID, 11004, "dave", "Dave", "")
	_, _, _ = svc.Skip(ctx, gameID, 11004, "dave", "Dave", "")

	// Skip again – player is no longer registered, so skipped=false
	_, skipped, err := svc.Skip(ctx, gameID, 11004, "dave", "Dave", "")
	if err != nil {
		t.Fatalf("second Skip: %v", err)
	}
	if skipped {
		t.Error("skipped should be false when player is already skipped")
	}
}

func TestParticipationService_RejoinAfterSkip(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	_, _ = svc.Join(ctx, gameID, 11005, "eve", "Eve", "")
	_, _, _ = svc.Skip(ctx, gameID, 11005, "eve", "Eve", "")

	// Rejoin after skip — status should flip back to registered
	parts, err := svc.Join(ctx, gameID, 11005, "eve", "Eve", "")
	if err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("got %d participations after rejoin, want 1", len(parts))
	}
	if parts[0].Status != models.StatusRegistered {
		t.Errorf("status after rejoin: got %q, want registered", parts[0].Status)
	}
}

func TestParticipationService_MultiplePlayersOrdering(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	// Three players join in order
	_, _ = svc.Join(ctx, gameID, 20001, "alpha", "Alpha", "")
	_, _ = svc.Join(ctx, gameID, 20002, "beta", "Beta", "")
	parts, err := svc.Join(ctx, gameID, 20003, "gamma", "Gamma", "")
	if err != nil {
		t.Fatalf("Join (third): %v", err)
	}

	if len(parts) != 3 {
		t.Fatalf("got %d participations, want 3", len(parts))
	}
	// All should be registered
	for i, p := range parts {
		if p.Status != models.StatusRegistered {
			t.Errorf("parts[%d] status: got %q, want registered", i, p.Status)
		}
	}
}

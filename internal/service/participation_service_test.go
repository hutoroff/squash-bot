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
	guestRepo := storage.NewGuestRepo(testPool)

	gameSvc := service.NewGameService(gameRepo)
	partSvc := service.NewParticipationService(playerRepo, partRepo, guestRepo)

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

// --- Guest tests ---

func TestParticipationService_AddGuest(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	// Any group member can add a guest regardless of their own registration status.
	parts, guests, err := svc.AddGuest(ctx, gameID, 30001, "alice", "Alice", "")
	if err != nil {
		t.Fatalf("AddGuest: %v", err)
	}
	if len(parts) != 0 {
		t.Errorf("expected 0 participations (inviter not registered), got %d", len(parts))
	}
	if len(guests) != 1 {
		t.Fatalf("got %d guests, want 1", len(guests))
	}
	if guests[0].InvitedBy == nil {
		t.Fatal("InvitedBy should be populated")
	}
	if guests[0].InvitedBy.TelegramID != 30001 {
		t.Errorf("InvitedBy TelegramID: got %d, want 30001", guests[0].InvitedBy.TelegramID)
	}
}

func TestParticipationService_AddGuest_WithoutJoining(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	// A player who has not joined can still add a guest.
	_, guests, err := svc.AddGuest(ctx, gameID, 30008, "nonmember", "", "")
	if err != nil {
		t.Fatalf("AddGuest without joining: %v", err)
	}
	if len(guests) != 1 {
		t.Errorf("expected 1 guest, got %d", len(guests))
	}
}

func TestParticipationService_AddGuest_AfterSkipping(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	// A player who skipped can still manage guests independently.
	_, _ = svc.Join(ctx, gameID, 30009, "skipper", "", "")
	_, _, _ = svc.Skip(ctx, gameID, 30009, "skipper", "", "")

	_, guests, err := svc.AddGuest(ctx, gameID, 30009, "skipper", "", "")
	if err != nil {
		t.Fatalf("AddGuest after skip: %v", err)
	}
	if len(guests) != 1 {
		t.Errorf("expected 1 guest, got %d", len(guests))
	}
}

func TestParticipationService_AddGuest_Multiple(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	_, guests1, _ := svc.AddGuest(ctx, gameID, 30002, "bob", "", "")
	_, guests2, err := svc.AddGuest(ctx, gameID, 30002, "bob", "", "")
	if err != nil {
		t.Fatalf("second AddGuest: %v", err)
	}

	if len(guests1) != 1 {
		t.Errorf("after first guest: got %d guests, want 1", len(guests1))
	}
	if len(guests2) != 2 {
		t.Errorf("after second guest: got %d guests, want 2", len(guests2))
	}
}

// TestParticipationService_Skip_DoesNotAffectGuests verifies that a player's
// guests remain on the roster when that player switches to skipped.
func TestParticipationService_Skip_DoesNotAffectGuests(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	// Alice joins and adds two guests.
	_, _ = svc.Join(ctx, gameID, 30010, "alice", "", "")
	_, _ = svc.AddGuest(ctx, gameID, 30010, "alice", "", "")
	_, _ = svc.AddGuest(ctx, gameID, 30010, "alice", "", "")

	// Bob adds a guest independently.
	_, _ = svc.AddGuest(ctx, gameID, 30011, "bob", "", "")

	guestsBefore, _ := svc.GetGuests(ctx, gameID)
	if len(guestsBefore) != 3 {
		t.Fatalf("expected 3 guests before skip, got %d", len(guestsBefore))
	}

	// Alice skips — her guests must NOT be removed.
	_, skipped, err := svc.Skip(ctx, gameID, 30010, "alice", "", "")
	if err != nil {
		t.Fatalf("Skip: %v", err)
	}
	if !skipped {
		t.Error("expected skipped=true")
	}

	guestsAfter, err := svc.GetGuests(ctx, gameID)
	if err != nil {
		t.Fatalf("GetGuests after skip: %v", err)
	}
	if len(guestsAfter) != 3 {
		t.Errorf("expected all 3 guests to remain after alice skipped, got %d", len(guestsAfter))
	}
}

func TestParticipationService_RemoveGuest_Success(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	_, _ = svc.AddGuest(ctx, gameID, 30003, "carol", "", "")
	_, _ = svc.AddGuest(ctx, gameID, 30003, "carol", "", "")

	removed, _, guests, err := svc.RemoveGuest(ctx, gameID, 30003)
	if err != nil {
		t.Fatalf("RemoveGuest: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}
	if len(guests) != 1 {
		t.Errorf("got %d guests after removal, want 1", len(guests))
	}
}

func TestParticipationService_RemoveGuest_NotFound(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	// Player who never interacted
	removed, _, _, err := svc.RemoveGuest(ctx, gameID, 99999)
	if err != nil {
		t.Fatalf("RemoveGuest (unknown player): %v", err)
	}
	if removed {
		t.Error("expected removed=false for unknown player")
	}
}

func TestParticipationService_RemoveGuest_NoGuests(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	// Player exists (via join) but added no guests.
	_, _ = svc.Join(ctx, gameID, 30004, "dave", "", "")

	removed, _, _, err := svc.RemoveGuest(ctx, gameID, 30004)
	if err != nil {
		t.Fatalf("RemoveGuest (no guests): %v", err)
	}
	if removed {
		t.Error("expected removed=false when player has no guests")
	}
}

func TestParticipationService_RemoveGuest_OnlyOwnGuests(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	_, _ = svc.AddGuest(ctx, gameID, 30005, "eve", "", "")
	_, _ = svc.AddGuest(ctx, gameID, 30006, "frank", "", "")

	// frank removes his guest — eve's guest should remain
	removed, _, guests, err := svc.RemoveGuest(ctx, gameID, 30006)
	if err != nil {
		t.Fatalf("RemoveGuest: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}
	if len(guests) != 1 {
		t.Fatalf("got %d guests, want 1 (eve's guest should remain)", len(guests))
	}
	if guests[0].InvitedBy == nil || guests[0].InvitedBy.TelegramID != 30005 {
		t.Error("remaining guest should belong to eve (30005)")
	}
}

func TestParticipationService_GetGuests(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	guests, err := svc.GetGuests(ctx, gameID)
	if err != nil {
		t.Fatalf("GetGuests: %v", err)
	}
	if len(guests) != 0 {
		t.Errorf("expected 0 guests initially, got %d", len(guests))
	}

	_, _ = svc.AddGuest(ctx, gameID, 30007, "grace", "", "")

	guests, err = svc.GetGuests(ctx, gameID)
	if err != nil {
		t.Fatalf("GetGuests after add: %v", err)
	}
	if len(guests) != 1 {
		t.Errorf("expected 1 guest, got %d", len(guests))
	}
}

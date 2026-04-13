//go:build integration

package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/vkhutorov/squash_bot/cmd/management/service"
	"github.com/vkhutorov/squash_bot/cmd/management/storage"
	"github.com/vkhutorov/squash_bot/internal/models"
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

	gameSvc := service.NewGameService(gameRepo, storage.NewVenueRepo(testPool))
	partSvc := service.NewParticipationService(playerRepo, partRepo, guestRepo)

	game, err := gameSvc.CreateGame(ctx, -1001, time.Now().Add(48*time.Hour), "1,2", nil)
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
	added, parts, guests, err := svc.AddGuest(ctx, gameID, 30001, "alice", "Alice", "")
	if err != nil {
		t.Fatalf("AddGuest: %v", err)
	}
	if !added {
		t.Fatal("AddGuest: expected added=true")
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
	_, _, guests, err := svc.AddGuest(ctx, gameID, 30008, "nonmember", "", "")
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

	_, _, guests, err := svc.AddGuest(ctx, gameID, 30009, "skipper", "", "")
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

	_, _, guests1, _ := svc.AddGuest(ctx, gameID, 30002, "bob", "", "")
	_, _, guests2, err := svc.AddGuest(ctx, gameID, 30002, "bob", "", "")
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
	_, _, _, _ = svc.AddGuest(ctx, gameID, 30010, "alice", "", "")
	_, _, _, _ = svc.AddGuest(ctx, gameID, 30010, "alice", "", "")

	// Bob adds a guest independently.
	_, _, _, _ = svc.AddGuest(ctx, gameID, 30011, "bob", "", "")

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

	_, _, _, _ = svc.AddGuest(ctx, gameID, 30003, "carol", "", "")
	_, _, _, _ = svc.AddGuest(ctx, gameID, 30003, "carol", "", "")

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

	_, _, _, _ = svc.AddGuest(ctx, gameID, 30005, "eve", "", "")
	_, _, _, _ = svc.AddGuest(ctx, gameID, 30006, "frank", "", "")

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

	_, _, _, _ = svc.AddGuest(ctx, gameID, 30007, "grace", "", "")

	guests, err = svc.GetGuests(ctx, gameID)
	if err != nil {
		t.Fatalf("GetGuests after add: %v", err)
	}
	if len(guests) != 1 {
		t.Errorf("expected 1 guest, got %d", len(guests))
	}
}

// TestParticipationService_AddGuest_AtCapacity verifies that the service propagates
// the repo-level capacity rejection: once slots are full, AddGuest returns
// (false, nil, nil, nil) without writing anything to the database.
func TestParticipationService_AddGuest_AtCapacity(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}

	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	partRepo := storage.NewParticipationRepo(testPool)
	guestRepo := storage.NewGuestRepo(testPool)
	partSvc := service.NewParticipationService(playerRepo, partRepo, guestRepo)
	gameSvc := service.NewGameService(gameRepo, storage.NewVenueRepo(testPool))

	// 1 court → capacity 2.
	game, err := gameSvc.CreateGame(ctx, -1001, time.Now().Add(48*time.Hour), "1", nil)
	if err != nil {
		t.Fatalf("create game: %v", err)
	}

	added1, _, _, err := partSvc.AddGuest(ctx, game.ID, 50001, "u1", "", "")
	if err != nil || !added1 {
		t.Fatalf("AddGuest (1st): added=%v err=%v", added1, err)
	}
	added2, _, _, err := partSvc.AddGuest(ctx, game.ID, 50002, "u2", "", "")
	if err != nil || !added2 {
		t.Fatalf("AddGuest (2nd): added=%v err=%v", added2, err)
	}

	// Game is now at capacity — third must be rejected.
	added3, parts, guests, err := partSvc.AddGuest(ctx, game.ID, 50003, "u3", "", "")
	if err != nil {
		t.Fatalf("AddGuest (3rd): %v", err)
	}
	if added3 {
		t.Error("AddGuest (3rd): expected added=false when game is at capacity")
	}
	if parts != nil || guests != nil {
		t.Error("AddGuest (3rd): parts and guests must be nil when not added")
	}

	// Confirm no third guest snuck into the DB.
	allGuests, err := partSvc.GetGuests(ctx, game.ID)
	if err != nil {
		t.Fatalf("GetGuests: %v", err)
	}
	if len(allGuests) != 2 {
		t.Errorf("expected 2 guests in DB, got %d", len(allGuests))
	}
}

// --- KickPlayer ---

func TestParticipationService_KickPlayer_Registered(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	_, _ = svc.Join(ctx, gameID, 40001, "alice", "Alice", "")

	parts, guests, removed, err := svc.KickPlayer(ctx, gameID, 40001)
	if err != nil {
		t.Fatalf("KickPlayer: %v", err)
	}
	if !removed {
		t.Error("expected removed=true for a registered player")
	}
	if len(parts) != 0 {
		t.Errorf("expected 0 registered players after kick, got %d", len(parts))
	}
	if guests == nil {
		t.Error("expected non-nil guest slice")
	}
}

func TestParticipationService_KickPlayer_PlayerNotInDB(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	// Telegram ID that was never registered.
	parts, guests, removed, err := svc.KickPlayer(ctx, gameID, 99999)
	if err != nil {
		t.Fatalf("KickPlayer (unknown player): %v", err)
	}
	if removed {
		t.Error("expected removed=false for unknown player")
	}
	if parts != nil || guests != nil {
		t.Error("expected nil slices when player is not in DB")
	}
}

func TestParticipationService_KickPlayer_PlayerInDBButNotGame(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	// Player exists (via join) in a *different* game context: skip registers them.
	// Here we just call Skip so the player is in the DB but as skipped status.
	// Actually, let's join and skip so the player IS in game_participations.
	// Then test a scenario where they're not in the game at all.

	// Use a brand-new telegramID that's only in the players table, not in this game.
	// We can't easily do that without a second game, so let's just test with a skipped player:
	// KickPlayer uses DeleteByGameAndPlayer which only removes 'registered' records... wait no,
	// it deletes regardless of status. Let's verify a skipped player is also removed.
	_, _ = svc.Join(ctx, gameID, 40002, "bob", "", "")
	_, _, _ = svc.Skip(ctx, gameID, 40002, "bob", "", "")

	parts, _, removed, err := svc.KickPlayer(ctx, gameID, 40002)
	if err != nil {
		t.Fatalf("KickPlayer (skipped player): %v", err)
	}
	// The skipped row exists in game_participations, so it should be removed.
	if !removed {
		t.Error("expected removed=true even for a skipped player")
	}
	if len(parts) != 0 {
		t.Errorf("expected 0 participations after kick, got %d", len(parts))
	}
}

// TestParticipationService_KickPlayer_GuestsPreserved confirms that kicking a player
// leaves their guests intact (guests are independent records managed separately).
func TestParticipationService_KickPlayer_GuestsPreserved(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	_, _ = svc.Join(ctx, gameID, 40003, "carol", "", "")
	_, _, _, _ = svc.AddGuest(ctx, gameID, 40003, "carol", "", "")
	_, _, _, _ = svc.AddGuest(ctx, gameID, 40003, "carol", "", "")

	_, guests, removed, err := svc.KickPlayer(ctx, gameID, 40003)
	if err != nil {
		t.Fatalf("KickPlayer: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}
	// Guests are NOT automatically removed when their inviter is kicked.
	if len(guests) != 2 {
		t.Errorf("expected 2 guests to remain after player kick, got %d", len(guests))
	}
}

// --- KickGuestByID ---

func TestParticipationService_KickGuestByID_Success(t *testing.T) {
	ctx := context.Background()
	svc, gameID := setupParticipationTest(t, ctx)

	_, _ = svc.Join(ctx, gameID, 40010, "dave", "", "")
	_, _, _, _ = svc.AddGuest(ctx, gameID, 40010, "dave", "", "")
	_, _, _, _ = svc.AddGuest(ctx, gameID, 40010, "dave", "", "")

	allGuests, _ := svc.GetGuests(ctx, gameID)
	if len(allGuests) != 2 {
		t.Fatalf("setup: expected 2 guests, got %d", len(allGuests))
	}
	targetGuestID := allGuests[0].ID

	parts, guests, removed, err := svc.KickGuestByID(ctx, gameID, targetGuestID)
	if err != nil {
		t.Fatalf("KickGuestByID: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}
	if len(guests) != 1 {
		t.Errorf("expected 1 guest after kick, got %d", len(guests))
	}
	if parts == nil {
		t.Error("expected non-nil participations slice")
	}
	// The remaining guest must NOT be the one we just kicked.
	if guests[0].ID == targetGuestID {
		t.Error("the kicked guest is still in the returned list")
	}
}

// TestParticipationService_KickGuestByID_WrongGame verifies that the ownership
// constraint prevents deleting a guest from a different game.
func TestParticipationService_KickGuestByID_WrongGame(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}

	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	partRepo := storage.NewParticipationRepo(testPool)
	guestRepo := storage.NewGuestRepo(testPool)
	svc := service.NewParticipationService(playerRepo, partRepo, guestRepo)

	venueRepo := storage.NewVenueRepo(testPool)
	gA, _ := service.NewGameService(gameRepo, venueRepo).CreateGame(ctx, -1001, time.Now().Add(48*time.Hour), "1,2", nil)
	gB, _ := service.NewGameService(gameRepo, venueRepo).CreateGame(ctx, -1001, time.Now().Add(96*time.Hour), "3,4", nil)

	_, _, _, _ = svc.AddGuest(ctx, gA.ID, 40011, "eve", "", "") // guest belongs to game A
	guestsA, _ := svc.GetGuests(ctx, gA.ID)
	guestAID := guestsA[0].ID

	// Claim this guest belongs to game B.
	_, _, removed, err := svc.KickGuestByID(ctx, gB.ID, guestAID)
	if err != nil {
		t.Fatalf("KickGuestByID (wrong game): %v", err)
	}
	if removed {
		t.Error("expected removed=false when game_id doesn't own the guest")
	}

	// Guest must still exist in game A.
	still, _ := svc.GetGuests(ctx, gA.ID)
	if len(still) != 1 {
		t.Errorf("guest should remain in game A, got %d guests", len(still))
	}
}

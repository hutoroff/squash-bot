//go:build integration

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/vkhutorov/squash_bot/internal/models"
	"github.com/vkhutorov/squash_bot/internal/storage"
	"github.com/vkhutorov/squash_bot/internal/testutil"
)

func TestGuestRepo_AddAndGet(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}

	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	guestRepo := storage.NewGuestRepo(testPool)

	g, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1,2"))
	p, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 400001, Username: strPtr("inviter")})

	if err := guestRepo.AddGuest(ctx, g.ID, p.ID); err != nil {
		t.Fatalf("AddGuest: %v", err)
	}

	guests, err := guestRepo.GetByGame(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetByGame: %v", err)
	}
	if len(guests) != 1 {
		t.Fatalf("got %d guests, want 1", len(guests))
	}
	if guests[0].InvitedByPlayerID != p.ID {
		t.Errorf("InvitedByPlayerID: got %d, want %d", guests[0].InvitedByPlayerID, p.ID)
	}
	if guests[0].InvitedBy == nil {
		t.Fatal("InvitedBy player not populated")
	}
	if guests[0].InvitedBy.TelegramID != p.TelegramID {
		t.Errorf("InvitedBy TelegramID: got %d, want %d", guests[0].InvitedBy.TelegramID, p.TelegramID)
	}
}

func TestGuestRepo_MultipleGuestsSameInviter(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}

	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	guestRepo := storage.NewGuestRepo(testPool)

	g, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1,2,3"))
	p, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 400002, Username: strPtr("multi_inviter")})

	_ = guestRepo.AddGuest(ctx, g.ID, p.ID)
	_ = guestRepo.AddGuest(ctx, g.ID, p.ID)
	_ = guestRepo.AddGuest(ctx, g.ID, p.ID)

	guests, err := guestRepo.GetByGame(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetByGame: %v", err)
	}
	if len(guests) != 3 {
		t.Errorf("got %d guests, want 3", len(guests))
	}
}

func TestGuestRepo_RemoveLatestGuest_Success(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}

	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	guestRepo := storage.NewGuestRepo(testPool)

	g, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1,2"))
	p, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 400003})

	_ = guestRepo.AddGuest(ctx, g.ID, p.ID)
	_ = guestRepo.AddGuest(ctx, g.ID, p.ID)

	removed, err := guestRepo.RemoveLatestGuest(ctx, g.ID, p.ID)
	if err != nil {
		t.Fatalf("RemoveLatestGuest: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}

	guests, _ := guestRepo.GetByGame(ctx, g.ID)
	if len(guests) != 1 {
		t.Errorf("got %d guests after removal, want 1", len(guests))
	}
}

func TestGuestRepo_RemoveLatestGuest_NoGuests(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}

	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	guestRepo := storage.NewGuestRepo(testPool)

	g, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1,2"))
	p, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 400004})

	removed, err := guestRepo.RemoveLatestGuest(ctx, g.ID, p.ID)
	if err != nil {
		t.Fatalf("RemoveLatestGuest: %v", err)
	}
	if removed {
		t.Error("expected removed=false when no guests exist")
	}
}

func TestGuestRepo_RemoveOnlyOwnGuests(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}

	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	guestRepo := storage.NewGuestRepo(testPool)

	g, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1,2,3"))
	p1, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 400005, Username: strPtr("owner")})
	p2, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 400006, Username: strPtr("other")})

	_ = guestRepo.AddGuest(ctx, g.ID, p1.ID)
	_ = guestRepo.AddGuest(ctx, g.ID, p2.ID)

	// p2 removes their own guest
	removed, err := guestRepo.RemoveLatestGuest(ctx, g.ID, p2.ID)
	if err != nil {
		t.Fatalf("RemoveLatestGuest: %v", err)
	}
	if !removed {
		t.Error("expected removed=true for p2's guest")
	}

	// p1's guest should still be there
	guests, _ := guestRepo.GetByGame(ctx, g.ID)
	if len(guests) != 1 {
		t.Fatalf("got %d guests, want 1 (p1's guest should remain)", len(guests))
	}
	if guests[0].InvitedByPlayerID != p1.ID {
		t.Errorf("remaining guest should belong to p1")
	}
}

func TestGuestRepo_GetCountByGame(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}

	gameRepo := storage.NewGameRepo(testPool)
	playerRepo := storage.NewPlayerRepo(testPool)
	guestRepo := storage.NewGuestRepo(testPool)

	g, _ := gameRepo.Create(ctx, newGame(-1, time.Now().Add(24*time.Hour), "1,2"))
	p, _ := playerRepo.Upsert(ctx, &models.Player{TelegramID: 400007})

	count, _ := guestRepo.GetCountByGame(ctx, g.ID)
	if count != 0 {
		t.Errorf("count before adding: got %d, want 0", count)
	}

	_ = guestRepo.AddGuest(ctx, g.ID, p.ID)
	_ = guestRepo.AddGuest(ctx, g.ID, p.ID)

	count, err := guestRepo.GetCountByGame(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetCountByGame: %v", err)
	}
	if count != 2 {
		t.Errorf("count after adding 2: got %d, want 2", count)
	}
}

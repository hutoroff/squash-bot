//go:build integration

package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/vkhutorov/squash_bot/cmd/management/service"
	"github.com/vkhutorov/squash_bot/cmd/management/storage"
	"github.com/vkhutorov/squash_bot/internal/testutil"
)

func TestGameService_CreateGame(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	svc := service.NewGameService(storage.NewGameRepo(testPool), storage.NewVenueRepo(testPool))

	gameDate := time.Now().Add(48 * time.Hour)
	game, err := svc.CreateGame(ctx, -1001, gameDate, "2,3,4", nil)
	if err != nil {
		t.Fatalf("CreateGame: %v", err)
	}

	if game.ID == 0 {
		t.Error("expected non-zero game ID")
	}
	if game.ChatID != -1001 {
		t.Errorf("ChatID: got %d, want -1001", game.ChatID)
	}
	if game.Courts != "2,3,4" {
		t.Errorf("Courts: got %q, want %q", game.Courts, "2,3,4")
	}
	// 3 courts parsed from "2,3,4"
	if game.CourtsCount != 3 {
		t.Errorf("CourtsCount: got %d, want 3", game.CourtsCount)
	}
}

func TestGameService_CreateGame_SingleCourt(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	svc := service.NewGameService(storage.NewGameRepo(testPool), storage.NewVenueRepo(testPool))

	game, err := svc.CreateGame(ctx, -1, time.Now().Add(24*time.Hour), "5", nil)
	if err != nil {
		t.Fatalf("CreateGame: %v", err)
	}
	if game.CourtsCount != 1 {
		t.Errorf("CourtsCount: got %d, want 1", game.CourtsCount)
	}
}

func TestGameService_UpdateMessageID(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	svc := service.NewGameService(storage.NewGameRepo(testPool), storage.NewVenueRepo(testPool))

	game, _ := svc.CreateGame(ctx, -1, time.Now().Add(24*time.Hour), "1", nil)
	if err := svc.UpdateMessageID(ctx, game.ID, 12345); err != nil {
		t.Fatalf("UpdateMessageID: %v", err)
	}

	updated, err := svc.GetByID(ctx, game.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.MessageID == nil || *updated.MessageID != 12345 {
		t.Errorf("MessageID: got %v, want 12345", updated.MessageID)
	}
}

func TestGameService_GetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	svc := service.NewGameService(storage.NewGameRepo(testPool), storage.NewVenueRepo(testPool))

	_, err := svc.GetByID(ctx, 9999999)
	if err == nil {
		t.Error("expected error for non-existent game, got nil")
	}
}

func TestGameService_GetNextGameForTelegramUser_Registered(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}

	gameSvc := service.NewGameService(storage.NewGameRepo(testPool), storage.NewVenueRepo(testPool))
	partSvc := service.NewParticipationService(
		storage.NewPlayerRepo(testPool),
		storage.NewParticipationRepo(testPool),
		storage.NewGuestRepo(testPool),
		nil,
	)

	game, _ := gameSvc.CreateGame(ctx, -1, time.Now().Add(48*time.Hour), "1,2", nil)
	_, _ = partSvc.Join(ctx, game.ID, 80001, "alice", "Alice", "")

	got, err := gameSvc.GetNextGameForTelegramUser(ctx, 80001)
	if err != nil {
		t.Fatalf("GetNextGameForTelegramUser: %v", err)
	}
	if got == nil {
		t.Fatal("expected a game, got nil")
	}
	if got.ID != game.ID {
		t.Errorf("got game ID %d, want %d", got.ID, game.ID)
	}
}

func TestGameService_GetNextGameForTelegramUser_NoGame(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	svc := service.NewGameService(storage.NewGameRepo(testPool), storage.NewVenueRepo(testPool))

	got, err := svc.GetNextGameForTelegramUser(ctx, 99999)
	if err != nil {
		t.Fatalf("GetNextGameForTelegramUser: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for user with no games, got game ID %d", got.ID)
	}
}

func TestGameService_UpdateCourts(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	svc := service.NewGameService(storage.NewGameRepo(testPool), storage.NewVenueRepo(testPool))

	game, _ := svc.CreateGame(ctx, -1, time.Now().Add(24*time.Hour), "1,2", nil)

	if err := svc.UpdateCourts(ctx, game.ID, "3,4,5"); err != nil {
		t.Fatalf("UpdateCourts: %v", err)
	}

	updated, _ := svc.GetByID(ctx, game.ID)
	if updated.Courts != "3,4,5" {
		t.Errorf("Courts: got %q, want %q", updated.Courts, "3,4,5")
	}
	// Service must recompute courts_count from the comma-separated string.
	if updated.CourtsCount != 3 {
		t.Errorf("CourtsCount: got %d, want 3", updated.CourtsCount)
	}
}

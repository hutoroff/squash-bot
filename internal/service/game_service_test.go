//go:build integration

package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/vkhutorov/squash_bot/internal/service"
	"github.com/vkhutorov/squash_bot/internal/storage"
	"github.com/vkhutorov/squash_bot/internal/testutil"
)

func TestGameService_CreateGame(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, testPool); err != nil {
		t.Fatal(err)
	}
	svc := service.NewGameService(storage.NewGameRepo(testPool))

	gameDate := time.Now().Add(48 * time.Hour)
	game, err := svc.CreateGame(ctx, -1001, gameDate, "2,3,4")
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
	svc := service.NewGameService(storage.NewGameRepo(testPool))

	game, err := svc.CreateGame(ctx, -1, time.Now().Add(24*time.Hour), "5")
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
	svc := service.NewGameService(storage.NewGameRepo(testPool))

	game, _ := svc.CreateGame(ctx, -1, time.Now().Add(24*time.Hour), "1")
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
	svc := service.NewGameService(storage.NewGameRepo(testPool))

	_, err := svc.GetByID(ctx, 9999999)
	if err == nil {
		t.Error("expected error for non-existent game, got nil")
	}
}

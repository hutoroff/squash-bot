//go:build integration

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
)

// seedVenue inserts a minimal group + venue and returns the venue ID.
func seedVenue(t *testing.T, chatID int64) int64 {
	t.Helper()
	ctx := context.Background()
	groupRepo := storage.NewGroupRepo(testPool)
	if err := groupRepo.Upsert(ctx, chatID, "Test Group", true); err != nil {
		t.Fatalf("upsert group: %v", err)
	}
	venueRepo := storage.NewVenueRepo(testPool)
	venue, err := venueRepo.Create(ctx, &models.Venue{
		GroupID:          chatID,
		Name:             "Test Venue",
		Courts:           "1,2",
		GameDays:         "3",
		BookingOpensDays: 14,
	})
	if err != nil {
		t.Fatalf("create venue: %v", err)
	}
	return venue.ID
}

// ── AutoBookingResultRepo ─────────────────────────────────────────────────────

func TestAutoBookingResultRepo_Save_GetByVenueAndDate(t *testing.T) {
	mustTruncate(t)
	ctx := context.Background()
	venueID := seedVenue(t, -400100)
	repo := storage.NewAutoBookingResultRepo(testPool)

	gameDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	// Not found before saving.
	results, err := repo.GetByVenueAndDate(ctx, venueID, gameDate)
	if err != nil {
		t.Fatalf("GetByVenueAndDate before save: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty slice before save, got %d rows", len(results))
	}

	// Save a result for a specific time slot.
	if err := repo.Save(ctx, venueID, gameDate, "18:00", "1,2", 2); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Now found.
	results, err = repo.GetByVenueAndDate(ctx, venueID, gameDate)
	if err != nil {
		t.Fatalf("GetByVenueAndDate after save: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	res := results[0]
	if res.VenueID != venueID {
		t.Errorf("VenueID: got %d, want %d", res.VenueID, venueID)
	}
	if res.GameTime != "18:00" {
		t.Errorf("GameTime: got %q, want %q", res.GameTime, "18:00")
	}
	if res.Courts != "1,2" {
		t.Errorf("Courts: got %q, want %q", res.Courts, "1,2")
	}
	if res.CourtsCount != 2 {
		t.Errorf("CourtsCount: got %d, want 2", res.CourtsCount)
	}
}

func TestAutoBookingResultRepo_Save_MultipleSlots(t *testing.T) {
	mustTruncate(t)
	ctx := context.Background()
	venueID := seedVenue(t, -400150)
	repo := storage.NewAutoBookingResultRepo(testPool)

	gameDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	if err := repo.Save(ctx, venueID, gameDate, "18:00", "1,2", 2); err != nil {
		t.Fatalf("Save 18:00: %v", err)
	}
	if err := repo.Save(ctx, venueID, gameDate, "20:00", "3,4", 2); err != nil {
		t.Fatalf("Save 20:00: %v", err)
	}

	results, err := repo.GetByVenueAndDate(ctx, venueID, gameDate)
	if err != nil {
		t.Fatalf("GetByVenueAndDate: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// Results are ordered by game_time ASC.
	if results[0].GameTime != "18:00" || results[1].GameTime != "20:00" {
		t.Errorf("unexpected order: %v, %v", results[0].GameTime, results[1].GameTime)
	}
}

func TestAutoBookingResultRepo_Save_UniqueConstraint(t *testing.T) {
	mustTruncate(t)
	ctx := context.Background()
	venueID := seedVenue(t, -400200)
	repo := storage.NewAutoBookingResultRepo(testPool)

	gameDate := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)

	// First save succeeds.
	if err := repo.Save(ctx, venueID, gameDate, "18:00", "1,2", 2); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	// Duplicate (same venue+date+time) is silently ignored.
	if err := repo.Save(ctx, venueID, gameDate, "18:00", "3,4", 2); err != nil {
		t.Fatalf("duplicate Save should be ignored, got: %v", err)
	}

	// Original record is unchanged.
	results, err := repo.GetByVenueAndDate(ctx, venueID, gameDate)
	if err != nil {
		t.Fatalf("GetByVenueAndDate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after duplicate save, got %d", len(results))
	}
	if results[0].Courts != "1,2" {
		t.Errorf("Courts: got %q, want original %q (duplicate should not overwrite)", results[0].Courts, "1,2")
	}
}

func TestAutoBookingResultRepo_GetByVenueAndDate_WrongDate(t *testing.T) {
	mustTruncate(t)
	ctx := context.Background()
	venueID := seedVenue(t, -400300)
	repo := storage.NewAutoBookingResultRepo(testPool)

	gameDate := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	if err := repo.Save(ctx, venueID, gameDate, "18:00", "1", 1); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Query a different date → empty slice.
	other := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	results, err := repo.GetByVenueAndDate(ctx, venueID, other)
	if err != nil {
		t.Fatalf("GetByVenueAndDate: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty slice for different date, got %d rows", len(results))
	}
}

func TestAutoBookingResultRepo_GetByVenueAndDateAndTime(t *testing.T) {
	mustTruncate(t)
	ctx := context.Background()
	venueID := seedVenue(t, -400400)
	repo := storage.NewAutoBookingResultRepo(testPool)

	gameDate := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	if err := repo.Save(ctx, venueID, gameDate, "18:00", "1,2", 2); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Exact time found.
	res, err := repo.GetByVenueAndDateAndTime(ctx, venueID, gameDate, "18:00")
	if err != nil {
		t.Fatalf("GetByVenueAndDateAndTime: %v", err)
	}
	if res == nil {
		t.Fatal("expected result, got nil")
	}
	if res.GameTime != "18:00" {
		t.Errorf("GameTime: got %q, want %q", res.GameTime, "18:00")
	}

	// Different time → not found.
	res, err = repo.GetByVenueAndDateAndTime(ctx, venueID, gameDate, "20:00")
	if err != nil {
		t.Fatalf("GetByVenueAndDateAndTime (not found): %v", err)
	}
	if res != nil {
		t.Errorf("expected nil for unregistered time slot, got %+v", res)
	}
}

func TestAutoBookingResultRepo_SetGameID_GetByGameID(t *testing.T) {
	mustTruncate(t)
	ctx := context.Background()
	chatID := int64(-400500)
	venueID := seedVenue(t, chatID)
	repo := storage.NewAutoBookingResultRepo(testPool)

	gameDate := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	if err := repo.Save(ctx, venueID, gameDate, "18:00", "1", 1); err != nil {
		t.Fatalf("Save: %v", err)
	}
	results, err := repo.GetByVenueAndDate(ctx, venueID, gameDate)
	if err != nil || len(results) != 1 {
		t.Fatalf("GetByVenueAndDate: err=%v, count=%d", err, len(results))
	}
	resultID := results[0].ID

	// GetByGameID returns nil for a non-existent game ID.
	res, err := repo.GetByGameID(ctx, 999)
	if err != nil {
		t.Fatalf("GetByGameID (before set): %v", err)
	}
	if res != nil {
		t.Errorf("expected nil before SetGameID, got %+v", res)
	}

	// Create a real game row to satisfy the FK constraint on game_id.
	gameRepo := storage.NewGameRepo(testPool)
	game, err := gameRepo.Create(ctx, &models.Game{
		ChatID:      chatID,
		GameDate:    gameDate,
		Courts:      "1",
		CourtsCount: 1,
	})
	if err != nil {
		t.Fatalf("create game: %v", err)
	}

	// Link the result to the game.
	if err := repo.SetGameID(ctx, resultID, game.ID); err != nil {
		t.Fatalf("SetGameID: %v", err)
	}

	// Now GetByGameID finds it.
	res, err = repo.GetByGameID(ctx, game.ID)
	if err != nil {
		t.Fatalf("GetByGameID (after set): %v", err)
	}
	if res == nil {
		t.Fatal("expected result after SetGameID, got nil")
	}
	if res.ID != resultID {
		t.Errorf("ID: got %d, want %d", res.ID, resultID)
	}
	if res.GameTime != "18:00" {
		t.Errorf("GameTime: got %q, want %q", res.GameTime, "18:00")
	}
}

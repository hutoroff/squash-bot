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
	res, err := repo.GetByVenueAndDate(ctx, venueID, gameDate)
	if err != nil {
		t.Fatalf("GetByVenueAndDate before save: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil before save, got %+v", res)
	}

	// Save a result.
	if err := repo.Save(ctx, venueID, gameDate, "1,2", 2); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Now found.
	res, err = repo.GetByVenueAndDate(ctx, venueID, gameDate)
	if err != nil {
		t.Fatalf("GetByVenueAndDate after save: %v", err)
	}
	if res == nil {
		t.Fatal("expected result, got nil")
	}
	if res.VenueID != venueID {
		t.Errorf("VenueID: got %d, want %d", res.VenueID, venueID)
	}
	if res.Courts != "1,2" {
		t.Errorf("Courts: got %q, want %q", res.Courts, "1,2")
	}
	if res.CourtsCount != 2 {
		t.Errorf("CourtsCount: got %d, want 2", res.CourtsCount)
	}
}

func TestAutoBookingResultRepo_Save_UniqueConstraint(t *testing.T) {
	mustTruncate(t)
	ctx := context.Background()
	venueID := seedVenue(t, -400200)
	repo := storage.NewAutoBookingResultRepo(testPool)

	gameDate := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)

	// First save succeeds.
	if err := repo.Save(ctx, venueID, gameDate, "1,2", 2); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	// Duplicate is silently ignored, no error.
	if err := repo.Save(ctx, venueID, gameDate, "3,4", 2); err != nil {
		t.Fatalf("duplicate Save should be ignored, got: %v", err)
	}

	// Original record is unchanged.
	res, err := repo.GetByVenueAndDate(ctx, venueID, gameDate)
	if err != nil {
		t.Fatalf("GetByVenueAndDate: %v", err)
	}
	if res == nil {
		t.Fatal("expected result after duplicate save, got nil")
	}
	if res.Courts != "1,2" {
		t.Errorf("Courts: got %q, want original %q (duplicate should not overwrite)", res.Courts, "1,2")
	}
}

func TestAutoBookingResultRepo_GetByVenueAndDate_WrongDate(t *testing.T) {
	mustTruncate(t)
	ctx := context.Background()
	venueID := seedVenue(t, -400300)
	repo := storage.NewAutoBookingResultRepo(testPool)

	gameDate := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	if err := repo.Save(ctx, venueID, gameDate, "1", 1); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Query a different date → not found.
	other := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	res, err := repo.GetByVenueAndDate(ctx, venueID, other)
	if err != nil {
		t.Fatalf("GetByVenueAndDate: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil for different date, got %+v", res)
	}
}

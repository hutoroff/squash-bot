//go:build integration

package storage_test

import (
	"context"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
)

func TestVenueRepo_UpdateOmittedFractionPreservesStoredValue(t *testing.T) {
	mustTruncate(t)
	ctx := context.Background()
	const groupID = int64(-400200)
	if err := storage.NewGroupRepo(testPool).Upsert(ctx, groupID, "Test Group", true); err != nil {
		t.Fatalf("upsert group: %v", err)
	}
	repo := storage.NewVenueRepo(testPool)
	venue, err := repo.Create(ctx, &models.Venue{
		GroupID: groupID, Name: "Test Venue", Courts: "1,2",
		PreventiveCancellationFraction: "1/3",
	})
	if err != nil {
		t.Fatalf("create venue: %v", err)
	}

	venue.Name = "Telegram edit"
	venue.PreventiveCancellationFraction = ""
	updated, err := repo.Update(ctx, venue)
	if err != nil {
		t.Fatalf("update venue: %v", err)
	}
	if updated.PreventiveCancellationFraction != "1/3" {
		t.Errorf("fraction: got %q, want %q", updated.PreventiveCancellationFraction, "1/3")
	}
}

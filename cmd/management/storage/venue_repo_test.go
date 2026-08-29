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

func TestVenueRepo_RoundTripsSports(t *testing.T) {
	mustTruncate(t)
	ctx := context.Background()
	const groupID = int64(-400201)
	if err := storage.NewGroupRepo(testPool).Upsert(ctx, groupID, "Sports Group", true); err != nil {
		t.Fatal(err)
	}
	players := 5
	venue, err := storage.NewVenueRepo(testPool).Create(ctx, &models.Venue{
		GroupID: groupID, Name: "Multi", PreventiveCancellationFraction: "1/2",
		Sports: []models.VenueSport{{Sport: "squash", Courts: "1,2"}, {Sport: "bowling", Courts: "A,B", PlayersPerCourt: &players}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if venue.Courts != "1,2" || venue.CourtsFor("bowling") != "A,B" || len(venue.Sports) != 2 {
		t.Fatalf("unexpected venue: %+v", venue)
	}
}

func TestVenueRepo_RejectsInvalidSportsAtDatabaseBoundary(t *testing.T) {
	mustTruncate(t)
	ctx := context.Background()
	const groupID = int64(-400202)
	if err := storage.NewGroupRepo(testPool).Upsert(ctx, groupID, "Guarded Sports", true); err != nil {
		t.Fatal(err)
	}
	zero := 0
	for _, venueSport := range []models.VenueSport{
		{Sport: "future_sport", Courts: "1"},
		{Sport: "squash", Courts: "1", PlayersPerCourt: &zero},
	} {
		_, err := storage.NewVenueRepo(testPool).Create(ctx, &models.Venue{
			GroupID: groupID, Name: "Invalid", Sports: []models.VenueSport{venueSport},
		})
		if err == nil {
			t.Fatalf("database accepted invalid sport: %+v", venueSport)
		}
	}
}

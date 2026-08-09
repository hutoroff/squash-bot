//go:build integration

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
)

func TestCourtBookingRepo_HasActive(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name       string
		dateOffset int
		canceled   bool
		want       bool
	}{
		{name: "past", dateOffset: -1},
		{name: "today", want: true},
		{name: "future", dateOffset: 1, want: true},
		{name: "canceled future", dateOffset: 1, canceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustTruncate(t)
			const groupID = -500001
			venueID := seedVenue(t, groupID)
			if _, err := testPool.Exec(ctx, "UPDATE bot_groups SET timezone = 'America/New_York' WHERE chat_id = $1", groupID); err != nil {
				t.Fatalf("set group timezone: %v", err)
			}
			var today time.Time
			if err := testPool.QueryRow(ctx, "SELECT (NOW() AT TIME ZONE 'America/New_York')::date").Scan(&today); err != nil {
				t.Fatalf("group current date: %v", err)
			}
			credential, err := storage.NewVenueCredentialRepo(testPool).Create(ctx, venueID, "test@example.com", "encrypted", 0, 3)
			if err != nil {
				t.Fatalf("create credential: %v", err)
			}

			repo := storage.NewCourtBookingRepo(testPool)
			matchID := "match-" + tc.name
			if err := repo.Save(ctx, &models.CourtBooking{
				VenueID:      venueID,
				GameDate:     today.AddDate(0, 0, tc.dateOffset),
				GameTime:     "18:00",
				CourtUUID:    "court-1",
				CourtLabel:   "1",
				MatchID:      matchID,
				BookingUUID:  "booking-" + tc.name,
				CredentialID: &credential.ID,
			}); err != nil {
				t.Fatalf("save booking: %v", err)
			}
			if tc.canceled {
				if err := repo.MarkCanceled(ctx, matchID); err != nil {
					t.Fatalf("mark canceled: %v", err)
				}
			}

			byCredential, err := repo.HasActiveByCredentialID(ctx, credential.ID)
			if err != nil {
				t.Fatalf("HasActiveByCredentialID: %v", err)
			}
			byVenue, err := repo.HasActiveByVenueID(ctx, venueID)
			if err != nil {
				t.Fatalf("HasActiveByVenueID: %v", err)
			}
			if byCredential != tc.want || byVenue != tc.want {
				t.Errorf("active = credential=%t venue=%t, want %t", byCredential, byVenue, tc.want)
			}
		})
	}
}

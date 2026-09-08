//go:build integration

package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/testutil"
)

func TestReliableCancellationReplaysWithoutSecondProviderMutation(t *testing.T) {
	if err := testutil.CheckDocker(); err != nil {
		t.Fatalf("integration test requires Docker: %v", err)
	}
	ctx := context.Background()
	pool, cleanup, err := testutil.SetupTestDB(ctx)
	if err != nil {
		t.Fatalf("setup database: %v", err)
	}
	defer cleanup()
	const groupID int64 = -880002
	if err := storage.NewGroupRepo(pool).Upsert(ctx, groupID, "Reliable Cancellation", true); err != nil {
		t.Fatal(err)
	}
	venue, err := storage.NewVenueRepo(pool).Create(ctx, &models.Venue{
		GroupID: groupID, Name: "Reliable Venue", Courts: "1", GameDays: "3", BookingOpensDays: 14,
	})
	if err != nil {
		t.Fatal(err)
	}
	gameDate := time.Date(2030, 1, 10, 18, 0, 0, 0, time.FixedZone("facility", 3600))
	venueID := venue.ID
	game, err := storage.NewGameRepo(pool).Create(ctx, &models.Game{
		ChatID: groupID, GameDate: gameDate, Courts: "1", CourtsCount: 1,
		Sport: "squash", PlayersPerCourt: 2, VenueID: &venueID,
	})
	if err != nil {
		t.Fatal(err)
	}
	encryptor, _ := NewEncryptor(testHexKey)
	encrypted, _ := encryptor.Encrypt("password")
	credential, err := storage.NewVenueCredentialRepo(pool).Create(ctx, venue.ID, "person@example.com", encrypted, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	courtBookings := storage.NewCourtBookingRepo(pool)
	entry := &models.CourtBooking{
		VenueID: venue.ID, GameDate: gameDate, GameTime: "18:00", CourtUUID: "court-uuid-1",
		CourtLabel: "1", MatchID: "match-1", BookingUUID: "booking-1", CredentialID: &credential.ID,
	}
	if err := courtBookings.Save(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry.OriginScope = testManagementBookingScope()
	intentRepo := storage.NewBookingIntentRepo(pool)
	intents, _ := NewBookingIntentService(intentRepo)
	mutationClient := &reliableCoreMutationClient{}
	executor, _ := NewReliableBookingExecutor(mutationClient, intentRepo, testManagementBookingScope(), time.Second, time.Millisecond)
	credentialService := NewVenueCredentialService(storage.NewVenueCredentialRepo(pool), storage.NewVenueRepo(pool), courtBookings, encryptor)
	job := &CancellationReminderJob{
		courtBookingRepo: courtBookings, credService: credentialService,
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		reliableBooking: &reliableBookingDeps{intents: intents, executor: executor, scope: testManagementBookingScope()},
	}
	game.Venue = venue
	updates := 0
	update := func(context.Context, int64, string, int) error { updates++; return nil }
	entries := map[int]*models.CourtBooking{1: entry}

	first, err := job.cancelUsingBookingEntriesReliable(ctx, game, entries, []int{1}, 1, update)
	if err != nil {
		t.Fatalf("first cancellation: %v", err)
	}
	second, err := job.cancelUsingBookingEntriesReliable(ctx, game, entries, []int{1}, 1, update)
	if err != nil {
		t.Fatalf("cancellation replay: %v", err)
	}
	if mutationClient.cancels.Load() != 1 {
		t.Fatalf("provider cancellation calls = %d, want 1", mutationClient.cancels.Load())
	}
	if len(first.canceledCourts) != 1 || len(second.canceledCourts) != 1 || updates != 2 {
		t.Fatalf("results = first %+v second %+v updates %d", first, second, updates)
	}
	var operationCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM management_booking_operations WHERE kind = 'cancel'`).Scan(&operationCount); err != nil {
		t.Fatal(err)
	}
	if operationCount != 1 {
		t.Fatalf("cancellation operations = %d, want 1", operationCount)
	}
}

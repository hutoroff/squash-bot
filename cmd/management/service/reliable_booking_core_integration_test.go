//go:build integration

package service

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/testutil"
)

type reliableCoreReadClient struct{}

func (*reliableCoreReadClient) ListCourts(context.Context, string, string, string) ([]BookingCourt, error) {
	return []BookingCourt{{ID: "1", UUID: "court-uuid-1", Name: "Court 1"}}, nil
}
func (*reliableCoreReadClient) ListMatches(context.Context, string, string, string, bool, string, string) ([]BookingSlot, error) {
	return nil, nil
}
func (*reliableCoreReadClient) CancelMatch(context.Context, string, string, string) error {
	panic("keyless cancellation called")
}
func (*reliableCoreReadClient) BookMatch(context.Context, string, string, string, string, string) (*BookMatchResult, error) {
	panic("keyless create called")
}

type reliableCoreMutationClient struct {
	creates atomic.Int32
	cancels atomic.Int32
}

func (*reliableCoreMutationClient) VerifyBinding(context.Context, bookingapi.Scope) error { return nil }
func (c *reliableCoreMutationClient) SubmitCreateOperation(_ context.Context, operationID bookingapi.OperationID, _ bookingapi.CreateRequest) (*bookingapi.OperationStatus, error) {
	c.creates.Add(1)
	return &bookingapi.OperationStatus{
		OperationID: string(operationID), Kind: bookingapi.OperationCreate, State: bookingapi.StateSucceeded,
		Phase: bookingapi.PhaseFinalization, Outcome: bookingapi.OutcomeSuccess, Revision: 1,
		Evidence: &bookingapi.ProviderEvidence{BookingUUID: "booking-1", BookingID: 10, PaymentID: 20, MatchID: "match-1", State: "ACCEPTED"},
	}, nil
}
func (c *reliableCoreMutationClient) SubmitCancelOperation(_ context.Context, operationID bookingapi.OperationID, matchID string, _ bookingapi.CancelRequest) (*bookingapi.OperationStatus, error) {
	c.cancels.Add(1)
	return &bookingapi.OperationStatus{
		OperationID: string(operationID), Kind: bookingapi.OperationCancel, State: bookingapi.StateSucceeded,
		Phase: bookingapi.PhaseFinalization, Outcome: bookingapi.OutcomeSuccess, Revision: 1,
		Evidence: &bookingapi.ProviderEvidence{MatchID: matchID, State: "CANCELLED"},
	}, nil
}
func (*reliableCoreMutationClient) GetOperationStatus(context.Context, bookingapi.OperationID) (*bookingapi.OperationStatus, error) {
	panic("unexpected status read")
}
func (*reliableCoreMutationClient) ReconcileOperation(context.Context, bookingapi.OperationID, bookingapi.ReconcileRequest) (*bookingapi.OperationStatus, error) {
	panic("unexpected reconciliation")
}
func (*reliableCoreMutationClient) ResolveOperation(context.Context, bookingapi.OperationID, bookingapi.OperationID, bookingapi.ResolutionRequest) (*bookingapi.OperationStatus, error) {
	panic("unexpected resolution")
}

func TestReliableBookFreeCourtsPersistsBeforeDispatchAndReplaysWithoutMutation(t *testing.T) {
	if err := testutil.CheckDocker(); err != nil {
		t.Fatalf("integration test requires Docker: %v", err)
	}
	ctx := context.Background()
	pool, cleanup, err := testutil.SetupTestDB(ctx)
	if err != nil {
		t.Fatalf("setup database: %v", err)
	}
	defer cleanup()
	const groupID int64 = -880001
	if err := storage.NewGroupRepo(pool).Upsert(ctx, groupID, "Reliable Core", true); err != nil {
		t.Fatalf("create group: %v", err)
	}
	venue, err := storage.NewVenueRepo(pool).Create(ctx, &models.Venue{
		GroupID: groupID, Name: "Reliable Venue", Courts: "1", AutoBookingCourts: "1",
		AutoBookingCourtsCount: 1, GameDays: "3", BookingOpensDays: 14,
	})
	if err != nil {
		t.Fatalf("create venue: %v", err)
	}
	encryptor, err := NewEncryptor(testHexKey)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := encryptor.Encrypt("password")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := storage.NewVenueCredentialRepo(pool).Create(ctx, venue.ID, "person@example.com", encrypted, 0, 1)
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	courtBookings := storage.NewCourtBookingRepo(pool)
	credentialService := NewVenueCredentialService(storage.NewVenueCredentialRepo(pool), storage.NewVenueRepo(pool), courtBookings, encryptor)
	intentRepo := storage.NewBookingIntentRepo(pool)
	intentService, err := NewBookingIntentService(intentRepo)
	if err != nil {
		t.Fatal(err)
	}
	mutationClient := &reliableCoreMutationClient{}
	executor, err := NewReliableBookingExecutor(mutationClient, intentRepo, testManagementBookingScope(), time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := bookingDeps{
		bookingClient: &reliableCoreReadClient{}, credService: credentialService,
		courtBookingRepo: courtBookings, logger: logger,
		reliable: &reliableBookingDeps{intents: intentService, executor: executor, scope: testManagementBookingScope()},
	}
	venue.AutoBookingCourts = "1"
	gameDate := time.Date(2030, 1, 10, 0, 0, 0, 0, time.FixedZone("facility", 3600))
	business := bookingIntentContext{requestKey: "scheduled:reliable-core:1", source: models.BookingIntentScheduled, generation: 1}

	first, err := bookFreeCourts(ctx, deps, venue, groupID, gameDate, "2030-01-10", "18:00", gameDate.Location(), 1, business)
	if err != nil {
		t.Fatalf("first booking: %v", err)
	}
	second, err := bookFreeCourts(ctx, deps, venue, groupID, gameDate, "2030-01-10", "18:00", gameDate.Location(), 1, business)
	if err != nil {
		t.Fatalf("replay booking: %v", err)
	}
	if mutationClient.creates.Load() != 1 {
		t.Fatalf("provider create calls = %d, want 1", mutationClient.creates.Load())
	}
	if len(first.BookedLabels) != 1 || len(second.BookedLabels) != 1 || first.BookedLabels[0] != "1" || second.BookedLabels[0] != "1" {
		t.Fatalf("booking results = first %+v second %+v", first, second)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM court_bookings WHERE match_id = 'match-1'`); err != nil {
		t.Fatalf("simulate lost local projection: %v", err)
	}
	recovery, err := NewReliableBookingRecovery(intentRepo, intentService, executor, credentialService, courtBookings, testManagementBookingScope(), logger, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovery.RunOnce(ctx); err != nil {
		t.Fatalf("recover local court booking projection: %v", err)
	}
	if mutationClient.creates.Load() != 1 {
		t.Fatalf("projection recovery repeated provider create; calls = %d", mutationClient.creates.Load())
	}
	var operationCount, bookingCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM management_booking_operations`).Scan(&operationCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM court_bookings WHERE credential_id = $1`, credential.ID).Scan(&bookingCount); err != nil {
		t.Fatal(err)
	}
	if operationCount != 1 || bookingCount != 1 {
		t.Fatalf("durable rows = operations %d court bookings %d", operationCount, bookingCount)
	}
	var createOperationID, serviceKey, facilityUUID string
	if err := pool.QueryRow(ctx, `
		SELECT management_create_operation_id::text, booking_service_key, booking_facility_uuid
		FROM court_bookings WHERE match_id = 'match-1'`).Scan(&createOperationID, &serviceKey, &facilityUUID); err != nil {
		t.Fatal(err)
	}
	if createOperationID == "" || serviceKey != testManagementBookingScope().ServiceKey || facilityUUID != testManagementBookingScope().FacilityUUID {
		t.Fatalf("court booking origin = operation %q service %q facility %q", createOperationID, serviceKey, facilityUUID)
	}
	loaded, err := courtBookings.GetByVenueAndDateAndTime(ctx, venue.ID, gameDate, "18:00")
	if err != nil || len(loaded) != 1 || loaded[0].OriginScope != testManagementBookingScope() || string(loaded[0].CreateOperationID) != createOperationID {
		t.Fatalf("loaded court booking origin = %+v err %v", loaded, err)
	}
}

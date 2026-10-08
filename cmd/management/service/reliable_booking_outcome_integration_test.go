//go:build integration

package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/testutil"
)

// These HTTP responses are synthetic v2 ledger projections, not real provider
// effects. Both static and registry routing use the same business-intent store.
func TestReliableBookingOutcomeAndAccountMatrix(t *testing.T) {
	for _, mode := range []string{"static-v2", "registry-v2"} {
		for _, scenario := range []string{"pre-dispatch-restart", "unknown-no-rotation", "credential-no-effect-fallback", "paid-without-match"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) { testReliableBookingOutcome(t, mode, scenario) })
		}
	}
}

func testReliableBookingOutcome(t *testing.T, mode, scenario string) {
	if err := testutil.CheckDocker(); err != nil {
		t.Fatalf("integration test requires Docker: %v", err)
	}
	ctx := context.Background()
	pool, cleanup, err := testutil.SetupTestDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	const groupID = int64(-880088)
	if err := storage.NewGroupRepo(pool).Upsert(ctx, groupID, "Outcome Fixture", true); err != nil {
		t.Fatal(err)
	}
	venues := storage.NewVenueRepo(pool)
	venue, err := venues.Create(ctx, &models.Venue{GroupID: groupID, Name: "Outcome Venue", Courts: "1", AutoBookingCourts: "1", AutoBookingCourtsCount: 1, GameDays: "4", BookingOpensDays: 5})
	if err != nil {
		t.Fatal(err)
	}
	key, err := NewEncryptor(testHexKey)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := key.Encrypt("synthetic-password")
	if err != nil {
		t.Fatal(err)
	}
	creds := storage.NewVenueCredentialRepo(pool)
	first, err := creds.Create(ctx, venue.ID, "primary@example.test", ciphertext, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := creds.Create(ctx, venue.ID, "secondary@example.test", ciphertext, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	courtBookings := storage.NewCourtBookingRepo(pool)
	credentialService := NewVenueCredentialService(creds, venues, courtBookings, key)
	intents := storage.NewBookingIntentRepo(pool)
	intentService, err := NewBookingIntentService(intents)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	keys := map[string]string{}
	var calls int
	scope := testManagementBookingScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/eversports/descriptor" {
			_ = json.NewEncoder(w).Encode(bookingapi.Descriptor{Scope: scope, Contract: bookingapi.ContractName, ContractMajor: bookingapi.ContractMajor, Capabilities: []string{"create", "cancel", "status", "reconcile", "resolve"}})
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/eversports/matches" {
			t.Errorf("unexpected booking request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var request bookingapi.CreateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("create payload: %v", err)
		}
		if request.Password != "synthetic-password" || request.CourtUUID != "court-uuid-1" || request.Start != "2030-01-10T18:00:00.000+01:00" {
			t.Errorf("changed provider payload: %+v", request)
		}
		operationID := r.Header.Get("Idempotency-Key")
		if operationID == "" {
			t.Error("missing durable booking key")
		}
		mu.Lock()
		calls++
		keys[request.Email] = operationID
		mu.Unlock()
		status := bookingapi.OperationStatus{OperationID: operationID, Kind: bookingapi.OperationCreate, Phase: bookingapi.PhaseFinalization, Revision: 1}
		switch {
		case scenario == "unknown-no-rotation":
			status.State = bookingapi.StateOutcomeUnknown
			status.Phase = bookingapi.PhasePayment
			status.Outcome = bookingapi.OutcomeUnknown
			status.Code = bookingapi.ErrorOperationOutcomeUnknown
			status.Evidence = &bookingapi.ProviderEvidence{BookingUUID: "reservation-unknown", PaymentID: 20, State: "PAYMENT_UNKNOWN"}
			w.WriteHeader(http.StatusConflict)
		case scenario == "credential-no-effect-fallback" && request.Email == "primary@example.test":
			status.State = bookingapi.StateRejectedNoEffect
			status.Phase = bookingapi.PhaseLogin
			status.Outcome = bookingapi.OutcomeNoEffect
			status.Code = bookingapi.ErrorCredentialRejected
			w.WriteHeader(http.StatusUnprocessableEntity)
		case scenario == "paid-without-match":
			status.State = bookingapi.StateSucceeded
			status.Outcome = bookingapi.OutcomeSuccessWithWarning
			status.Evidence = &bookingapi.ProviderEvidence{BookingUUID: "paid-booking", BookingID: 10, PaymentID: 20, State: "ACCEPTED"}
			status.Warnings = []string{"match attachment pending"}
			w.WriteHeader(http.StatusCreated)
		default:
			status.State = bookingapi.StateSucceeded
			status.Outcome = bookingapi.OutcomeSuccess
			status.Evidence = &bookingapi.ProviderEvidence{BookingUUID: "confirmed-booking", MatchID: "match-1", State: "ACCEPTED"}
			w.WriteHeader(http.StatusCreated)
		}
		_ = json.NewEncoder(w).Encode(status)
	}))
	defer server.Close()
	var client ReliableBookingServiceClient
	if mode == "static-v2" {
		client = NewReliableHTTPBookingClient(server.URL, "synthetic-secret")
	} else {
		resolver := bookingResolverFunc(func(context.Context, discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
			return []discovery.ResolvedEndpoint{{InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e07120501", API: server.URL}}, nil
		})
		client = NewDiscoveredReliableHTTPBookingClient(resolver, discovery.Requirement{ServiceKey: scope.ServiceKey, Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor}, "synthetic-secret")
	}
	executor, err := NewReliableBookingExecutor(client, intents, scope, time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	courtWriter := &rejectFirstCourtBookingSave{CourtBookingRepository: courtBookings}
	if scenario == "pre-dispatch-restart" {
		courtWriter.fail.Store(true)
	}
	deps := bookingDeps{bookingClient: &reliableCoreReadClient{}, credService: credentialService, courtBookingRepo: courtWriter, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), reliable: &reliableBookingDeps{intents: intentService, executor: executor, scope: scope}}
	zone := time.FixedZone("+01", 3600)
	date := time.Date(2030, 1, 10, 0, 0, 0, 0, zone)
	business := bookingIntentContext{requestKey: "scheduled:outcome-fixture:1", source: models.BookingIntentScheduled, generation: 1}
	book := func() *BookFreeCourtsResult {
		t.Helper()
		res, err := bookFreeCourts(ctx, deps, venue, groupID, date, "2030-01-10", "18:00", zone, 1, business)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	// Drop/rebuild the management caller without discarding its durable state.
	restartCaller := func() {
		restartedIntents := storage.NewBookingIntentRepo(pool)
		restartedService, err := NewBookingIntentService(restartedIntents)
		if err != nil {
			t.Fatal(err)
		}
		restartedExecutor, err := NewReliableBookingExecutor(client, restartedIntents, scope, time.Second, time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		deps.reliable = &reliableBookingDeps{intents: restartedService, executor: restartedExecutor, scope: scope}
	}
	if scenario == "pre-dispatch-restart" {
		intent, _, err := intentService.AdmitIntent(ctx, BookingIntentRequest{RequestKey: business.requestKey, Action: bookingapi.OperationCreate, Source: business.source, Scope: scope, GroupID: groupID, VenueID: venue.ID, GameDate: date, GameTime: "18:00", RequestedCount: 1, Generation: 1})
		if err != nil {
			t.Fatal(err)
		}
		start := time.Date(2030, 1, 10, 18, 0, 0, 0, zone)
		if _, _, err := intentService.AdmitOperation(ctx, BookingOperationRequest{IntentID: intent.ID, DedupKey: "create:court-uuid-1:g1:a1", Kind: bookingapi.OperationCreate, CourtUUID: "court-uuid-1", CourtLabel: "1", StartProvider: start.Format("2006-01-02T15:04:05.000Z07:00"), EndProvider: start.Add(autoBookingCourtDuration).Format("2006-01-02T15:04:05.000Z07:00"), CredentialID: &first.ID}); err != nil {
			t.Fatal(err)
		}
		restartCaller()
	}
	initial := book()
	if scenario == "pre-dispatch-restart" {
		var missingProjection int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM court_bookings`).Scan(&missingProjection); err != nil {
			t.Fatal(err)
		}
		if missingProjection != 0 {
			t.Fatalf("local write failure did not occur: %d", missingProjection)
		}
	}
	restartCaller()
	replay := book()
	mu.Lock()
	gotCalls := calls
	primaryKey, secondaryKey := keys["primary@example.test"], keys["secondary@example.test"]
	mu.Unlock()
	var count, unknown, rejected, success, resultCount, gameCount, courtCount int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE state='outcome_unknown'),count(*) FILTER (WHERE state='rejected_no_effect'),count(*) FILTER (WHERE state='succeeded') FROM management_booking_operations`).Scan(&count, &unknown, &rejected, &success); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM auto_booking_results`).Scan(&resultCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM games WHERE management_booking_intent_id IS NOT NULL`).Scan(&gameCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM court_bookings`).Scan(&courtCount); err != nil {
		t.Fatal(err)
	}
	switch scenario {
	case "pre-dispatch-restart":
		if len(initial.BookedLabels) != 1 || len(replay.BookedLabels) != 1 || gotCalls != 1 || primaryKey == "" || secondaryKey != "" || count != 1 || success != 1 || courtCount != 1 || resultCount != 1 || gameCount != 1 {
			t.Fatalf("restart must use admitted key: calls=%d keys=%q/%q rows=%d/%d/%d/%d/%d first=%+v replay=%+v", gotCalls, primaryKey, secondaryKey, count, success, courtCount, resultCount, gameCount, initial, replay)
		}
	case "unknown-no-rotation":
		if len(initial.BookedLabels) != 0 || len(replay.BookedLabels) != 0 || gotCalls != 1 || primaryKey == "" || secondaryKey != "" || count != 1 || unknown != 1 || resultCount != 0 || gameCount != 0 || courtCount != 0 {
			t.Fatalf("uncertainty rotated account or invented confirmed booking: calls=%d keys=%q/%q rows=%d/%d/%d/%d/%d initial=%+v replay=%+v", gotCalls, primaryKey, secondaryKey, count, unknown, resultCount, gameCount, courtCount, initial, replay)
		}
	case "credential-no-effect-fallback":
		if len(initial.BookedLabels) != 1 || len(replay.BookedLabels) != 1 || gotCalls != 2 || primaryKey == "" || secondaryKey == "" || primaryKey == secondaryKey || count != 2 || rejected != 1 || success != 1 || courtCount != 1 || resultCount != 1 || gameCount != 1 {
			t.Fatalf("deliberate no-effect fallback lost: calls=%d keys=%q/%q rows=%d/%d/%d/%d/%d/%d first=%+v replay=%+v", gotCalls, primaryKey, secondaryKey, count, rejected, success, resultCount, gameCount, courtCount, initial, replay)
		}
	case "paid-without-match":
		if len(initial.BookedLabels) != 1 || len(replay.BookedLabels) != 1 || gotCalls != 1 || primaryKey == "" || secondaryKey != "" || count != 1 || success != 1 || courtCount != 0 || resultCount != 1 || gameCount != 1 {
			t.Fatalf("paid warning must retain evidence, not rotate: calls=%d keys=%q/%q rows=%d/%d/%d/%d/%d first=%+v replay=%+v", gotCalls, primaryKey, secondaryKey, count, success, courtCount, resultCount, gameCount, initial, replay)
		}
		var bookingID, paymentID *int64
		var bookingUUID string
		if err := pool.QueryRow(ctx, `SELECT booking_uuid, booking_id, payment_id FROM management_booking_operations WHERE state='succeeded'`).Scan(&bookingUUID, &bookingID, &paymentID); err != nil {
			t.Fatal(err)
		}
		if bookingUUID != "paid-booking" || bookingID == nil || *bookingID != 10 || paymentID == nil || *paymentID != 20 {
			t.Fatalf("missing paid evidence: uuid=%q booking=%v payment=%v", bookingUUID, bookingID, paymentID)
		}
	}
	if first.ID == second.ID {
		t.Fatal("fixture credentials were not distinct")
	}
	var firstErrorAt, secondErrorAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT last_error_at FROM venue_credentials WHERE id=$1`, first.ID).Scan(&firstErrorAt); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT last_error_at FROM venue_credentials WHERE id=$1`, second.ID).Scan(&secondErrorAt); err != nil {
		t.Fatal(err)
	}
	if secondErrorAt != nil || (firstErrorAt != nil) != (scenario == "credential-no-effect-fallback") {
		t.Fatalf("invalid credential cooldown on %s: primary=%v secondary=%v", scenario, firstErrorAt, secondErrorAt)
	}
}

//go:build integration

package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/testutil"
)

// The real management intent store is used; the discovery selector and booking
// HTTP server are local fakes. No application process or external provider runs.
func TestRegistryBookingMissingThenReadyRetainsOneDurableIntentAndKey(t *testing.T) {
	if err := testutil.CheckDocker(); err != nil {
		t.Fatalf("integration test requires Docker: %v", err)
	}
	ctx := context.Background()
	pool, cleanup, err := testutil.SetupTestDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	const groupID = int64(-880090)
	if err := storage.NewGroupRepo(pool).Upsert(ctx, groupID, "registry fake", true); err != nil {
		t.Fatal(err)
	}
	venue, err := storage.NewVenueRepo(pool).Create(ctx, &models.Venue{GroupID: groupID, Name: "Registry Venue", Courts: "1", AutoBookingCourts: "1", AutoBookingCourtsCount: 1, GameDays: "4", BookingOpensDays: 5, AutoBookingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := NewEncryptor(testHexKey)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := encryptor.Encrypt("test-password")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := storage.NewVenueCredentialRepo(pool).Create(ctx, venue.ID, "registry@example.test", ciphertext, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	courtBookings := storage.NewCourtBookingRepo(pool)
	credentialService := NewVenueCredentialService(storage.NewVenueCredentialRepo(pool), storage.NewVenueRepo(pool), courtBookings, encryptor)
	intents := storage.NewBookingIntentRepo(pool)
	intentService, err := NewBookingIntentService(intents)
	if err != nil {
		t.Fatal(err)
	}
	var available atomic.Bool
	var mutationCalls, statusCalls atomic.Int32
	var originalKey atomic.Value
	scope := testManagementBookingScope()
	booking := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/eversports/descriptor":
			_ = json.NewEncoder(w).Encode(bookingapi.Descriptor{Scope: scope, Contract: bookingapi.ContractName, ContractMajor: bookingapi.ContractMajor, Capabilities: []string{"create", "cancel", "status", "reconcile", "resolve"}})
		case "/api/v2/eversports/matches":
			mutationCalls.Add(1)
			key := r.Header.Get("Idempotency-Key")
			originalKey.Store(key)
			var create bookingapi.CreateRequest
			if err := json.NewDecoder(r.Body).Decode(&create); err != nil || create.Email != "registry@example.test" || create.Password != "test-password" {
				t.Errorf("unexpected credential or request: %+v %v", create, err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"truncated":`)) // POST response ambiguity, no new key or account
		default:
			statusCalls.Add(1)
			if r.Method != http.MethodGet || r.URL.Path != "/api/v2/eversports/operations/"+originalKey.Load().(string) {
				t.Errorf("wrong status key/path: %s %s", r.Method, r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(bookingapi.OperationStatus{OperationID: originalKey.Load().(string), Kind: bookingapi.OperationCreate, State: bookingapi.StateOutcomeUnknown, Phase: bookingapi.PhasePayment, Outcome: bookingapi.OutcomeUnknown, Code: bookingapi.ErrorOperationOutcomeUnknown, Revision: 2, Evidence: &bookingapi.ProviderEvidence{BookingUUID: "unknown-booking", State: "PAYMENT_UNKNOWN"}})
		}
	}))
	defer booking.Close()
	resolver := bookingResolverFunc(func(context.Context, discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
		if !available.Load() {
			return nil, nil
		}
		return []discovery.ResolvedEndpoint{{InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e07120492", API: booking.URL}}, nil
	})
	client := NewDiscoveredReliableHTTPBookingClient(resolver, discovery.Requirement{ServiceKey: scope.ServiceKey, Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor}, "synthetic-secret")
	executor, err := NewReliableBookingExecutor(client, intents, scope, time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	deps := bookingDeps{bookingClient: &reliableCoreReadClient{}, credService: credentialService, courtBookingRepo: courtBookings, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), reliable: &reliableBookingDeps{intents: intentService, executor: executor, scope: scope}}
	zone := time.FixedZone("test +01", 3600)
	date := time.Date(2030, 1, 10, 0, 0, 0, 0, zone)
	business := bookingIntentContext{requestKey: "scheduled:registry-acceptance:1", source: models.BookingIntentScheduled, generation: 1}
	book := func() *BookFreeCourtsResult {
		t.Helper()
		result, err := bookFreeCourts(ctx, deps, venue, groupID, date, "2030-01-10", "18:00", zone, 1, business)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if first := book(); len(first.BookedLabels) != 0 || mutationCalls.Load() != 0 {
		t.Fatalf("absent booking target unexpectedly dispatched: %+v creates=%d", first, mutationCalls.Load())
	}
	var firstKey string
	if err := pool.QueryRow(ctx, `SELECT operation_id::text FROM management_booking_operations`).Scan(&firstKey); err != nil {
		t.Fatal(err)
	}
	available.Store(true) // same service/client/intent; no restart or new business identity
	if resumed := book(); len(resumed.BookedLabels) != 0 || mutationCalls.Load() != 1 || statusCalls.Load() != 1 {
		t.Fatalf("uncertain outcome: %+v create=%d status=%d", resumed, mutationCalls.Load(), statusCalls.Load())
	}
	if firstKey != originalKey.Load().(string) {
		t.Fatalf("planned key %q changed to %q", firstKey, originalKey.Load())
	}
	if replay := book(); len(replay.BookedLabels) != 0 || mutationCalls.Load() != 1 {
		t.Fatalf("ordinary replay replaced uncertain create: %+v creates=%d", replay, mutationCalls.Load())
	}
	var operationCount, unknownCount, otherCredentialCount int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE state = 'outcome_unknown'),count(*) FILTER (WHERE credential_id <> $1) FROM management_booking_operations`, credential.ID).Scan(&operationCount, &unknownCount, &otherCredentialCount); err != nil {
		t.Fatal(err)
	}
	if operationCount != 1 || unknownCount != 1 || otherCredentialCount != 0 {
		t.Fatalf("durable operations=%d unknown=%d other account=%d", operationCount, unknownCount, otherCredentialCount)
	}
}

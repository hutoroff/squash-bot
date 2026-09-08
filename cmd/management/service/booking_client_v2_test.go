package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/discovery"
)

const managementTestOperationID = bookingapi.OperationID("0195f8db-7a6f-7c7b-8f71-0f6e0712042d")

func TestReliableHTTPBookingClientVerifiesContractAndScope(t *testing.T) {
	scope := testManagementBookingScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/eversports/descriptor" {
			t.Errorf("descriptor path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(bookingapi.Descriptor{
			Scope: scope, Contract: bookingapi.ContractName, ContractMajor: bookingapi.ContractMajor,
			Capabilities: []string{"create", "cancel", "status", "reconcile", "resolve"},
		})
	}))
	defer server.Close()
	client := NewReliableHTTPBookingClient(server.URL, "secret")
	if err := client.VerifyBinding(context.Background(), scope); err != nil {
		t.Fatalf("VerifyBinding: %v", err)
	}
	mismatch := scope
	mismatch.FacilityUUID = "other-facility"
	if err := client.VerifyBinding(context.Background(), mismatch); err == nil {
		t.Fatal("mismatched facility binding was accepted")
	}
}

func TestReliableHTTPBookingClientSubmitsCreateWithDurableKey(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/eversports/matches" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Values("Idempotency-Key"); len(got) != 1 || got[0] != string(managementTestOperationID) {
			t.Errorf("Idempotency-Key = %v", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer internal-secret" {
			t.Errorf("Authorization = %q", got)
		}
		var request bookingapi.CreateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Email != "person@example.com" || request.Password != "password" || request.CourtUUID != "court-1" {
			t.Errorf("request = %+v", request)
		}
		writeManagementBookingStatus(t, w, http.StatusAccepted, bookingapi.OperationStatus{
			OperationID: string(managementTestOperationID), Kind: bookingapi.OperationCreate,
			State: bookingapi.StateAccepted, Phase: bookingapi.PhaseAdmission, Outcome: bookingapi.OutcomePending,
		})
	}))
	t.Cleanup(server.Close)

	client := NewReliableHTTPBookingClient(server.URL, "internal-secret")
	status, err := client.SubmitCreateOperation(context.Background(), managementTestOperationID, bookingapi.CreateRequest{
		CourtUUID: "court-1", Start: "2027-01-10T18:00:00.000+01:00", End: "2027-01-10T18:45:00.000+01:00",
		Email: "person@example.com", Password: "password",
	})
	if err != nil {
		t.Fatalf("SubmitCreateOperation: %v", err)
	}
	if status.State != bookingapi.StateAccepted || calls.Load() != 1 {
		t.Fatalf("status = %+v, calls = %d", status, calls.Load())
	}
}

func TestReliableHTTPBookingClientResolutionSeparatesEventKeyFromOperationID(t *testing.T) {
	eventKey := bookingapi.OperationID("0195f8db-7a6f-7c7b-8f71-0f6e0712042e")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Idempotency-Key") != string(eventKey) {
			t.Errorf("resolution event key = %q", r.Header.Get("Idempotency-Key"))
		}
		writeManagementBookingStatus(t, w, http.StatusOK, bookingapi.OperationStatus{
			OperationID: string(managementTestOperationID), Kind: bookingapi.OperationCreate,
			State: bookingapi.StateSucceeded, Phase: bookingapi.PhaseFinalization, Outcome: bookingapi.OutcomeSuccess, Revision: 3,
		})
	}))
	defer server.Close()
	status, err := NewReliableHTTPBookingClient(server.URL, "secret").ResolveOperation(context.Background(), managementTestOperationID, eventKey, bookingapi.ResolutionRequest{})
	if err != nil || status.OperationID != string(managementTestOperationID) {
		t.Fatalf("resolution status = %+v err = %v", status, err)
	}
}

func TestReliableHTTPBookingClientReturnsTypedContractErrorWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(bookingapi.ErrorResponse{
			Code: bookingapi.ErrorIdempotencyKeyConflict, Message: "provider body must not escape",
			OperationID: string(managementTestOperationID), State: string(bookingapi.StateNotDispatched),
		})
	}))
	t.Cleanup(server.Close)

	client := NewReliableHTTPBookingClient(server.URL, "internal-secret")
	_, err := client.SubmitCancelOperation(context.Background(), managementTestOperationID, "match/id", bookingapi.CancelRequest{Email: "a", Password: "b"})
	var contractError *BookingContractError
	if !errors.As(err, &contractError) || contractError.Code != bookingapi.ErrorIdempotencyKeyConflict {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "provider body") {
		t.Fatalf("error leaked response message: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestReliableHTTPBookingClientStatusAndResultExpiry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/eversports/operations/"+string(managementTestOperationID) {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Idempotency-Key") != "" {
			t.Error("status read unexpectedly sent Idempotency-Key")
		}
		writeManagementBookingStatus(t, w, http.StatusGone, bookingapi.OperationStatus{
			OperationID: string(managementTestOperationID), Kind: bookingapi.OperationCreate,
			State: bookingapi.StateSucceeded, Phase: bookingapi.PhaseFinalization, Outcome: bookingapi.OutcomeSuccess,
			ResultExpired: true,
		})
	}))
	t.Cleanup(server.Close)

	status, err := NewReliableHTTPBookingClient(server.URL, "secret").GetOperationStatus(context.Background(), managementTestOperationID)
	if err != nil {
		t.Fatalf("GetOperationStatus: %v", err)
	}
	if !status.ResultExpired || status.State != bookingapi.StateSucceeded {
		t.Fatalf("status = %+v", status)
	}
}

type bookingResolverFunc func(context.Context, discovery.Requirement) ([]discovery.ResolvedEndpoint, error)

func (f bookingResolverFunc) Resolve(ctx context.Context, requirement discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
	return f(ctx, requirement)
}

func TestDiscoveredReliableBookingClientAcceptsLateTargetWithoutRestart(t *testing.T) {
	operationID := bookingapi.OperationID("0195f8db-7a6f-7c7b-8f71-0f6e07120434")
	var available atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(bookingapi.OperationStatus{OperationID: string(operationID), Kind: bookingapi.OperationCreate, State: bookingapi.StateAccepted, Phase: bookingapi.PhaseAdmission, Outcome: bookingapi.OutcomePending})
	}))
	defer server.Close()
	resolver := bookingResolverFunc(func(context.Context, discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
		if !available.Load() {
			return nil, nil
		}
		return []discovery.ResolvedEndpoint{{InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e07120435", API: server.URL}}, nil
	})
	client := NewDiscoveredReliableHTTPBookingClient(resolver, discovery.Requirement{ServiceKey: "booking.default", Contract: "eversports-booking", Major: 2}, "secret")
	if _, err := client.SubmitCreateOperation(context.Background(), operationID, bookingapi.CreateRequest{}); !errors.Is(err, discovery.ErrNoEndpoint) {
		t.Fatalf("unavailable error = %v", err)
	}
	available.Store(true)
	if _, err := client.SubmitCreateOperation(context.Background(), operationID, bookingapi.CreateRequest{}); err != nil {
		t.Fatalf("late target: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestDiscoveredReliableBookingClientRejectsMultipleTargetsBeforeDispatch(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	descriptor := discovery.Descriptor{SchemaVersion: discovery.SchemaVersion, InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e07120431", ServiceKey: "booking.default", ServiceKind: "booking", Implementation: "eversports", ReleaseVersion: "test", Scope: map[string]string{"provider": "eversports", "region": "at", "environment": "test", "account_set": "default"}, Endpoints: discovery.Endpoints{API: server.URL, Admin: server.URL}, Contracts: []discovery.Contract{{Name: "eversports-booking", Major: 2}}, State: discovery.StateReady}
	second := descriptor
	second.InstanceID = "0195f8db-7a6f-7c7b-8f71-0f6e07120432"
	resolver, err := discovery.NewStaticResolver([]discovery.Descriptor{descriptor, second})
	if err != nil {
		t.Fatal(err)
	}
	client := NewDiscoveredReliableHTTPBookingClient(resolver, discovery.Requirement{ServiceKey: "booking.default", Contract: "eversports-booking", Major: 2, Scope: descriptor.Scope}, "secret")
	operationID := bookingapi.OperationID("0195f8db-7a6f-7c7b-8f71-0f6e07120433")
	_, err = client.SubmitCreateOperation(context.Background(), operationID, bookingapi.CreateRequest{})
	if !errors.Is(err, discovery.ErrMultipleActive) {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestReliableHTTPBookingClientDoesNotFollowMutationRedirect(t *testing.T) {
	var redirectedCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirectedCalls.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL+"/replayed")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client := NewReliableHTTPBookingClient(server.URL, "secret")
	if _, err := client.SubmitCreateOperation(context.Background(), managementTestOperationID, bookingapi.CreateRequest{CourtUUID: "court"}); err == nil {
		t.Fatal("redirect response unexpectedly succeeded")
	}
	if redirectedCalls.Load() != 0 {
		t.Fatalf("redirect replayed mutation %d times", redirectedCalls.Load())
	}
}

func TestReliableBookingReadClientRejectsKeylessMutationsWithoutHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	client := NewReliableBookingReadClient(server.URL, "secret")
	if _, err := client.BookMatch(context.Background(), "court", "start", "end", "email", "password"); err == nil {
		t.Fatal("keyless create unexpectedly succeeded")
	}
	if err := client.CancelMatch(context.Background(), "match", "email", "password"); err == nil {
		t.Fatal("keyless cancellation unexpectedly succeeded")
	}
	if calls.Load() != 0 {
		t.Fatalf("keyless mutation made %d HTTP requests", calls.Load())
	}
}

func TestReliableHTTPBookingClientRejectsMismatchedOrOversizedStatus(t *testing.T) {
	otherID := "0195f8db-7a6f-7c7b-8f71-0f6e0712042e"
	t.Run("mismatched operation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeManagementBookingStatus(t, w, http.StatusOK, bookingapi.OperationStatus{
				OperationID: otherID, Kind: bookingapi.OperationCreate, State: bookingapi.StateRunning,
				Phase: bookingapi.PhaseReserve, Outcome: bookingapi.OutcomePending,
			})
		}))
		defer server.Close()
		if _, err := NewReliableHTTPBookingClient(server.URL, "secret").GetOperationStatus(context.Background(), managementTestOperationID); err == nil {
			t.Fatal("expected operation ID mismatch")
		}
	})
	t.Run("oversized response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(make([]byte, maxReliableBookingResponseBytes+1))
		}))
		defer server.Close()
		if _, err := NewReliableHTTPBookingClient(server.URL, "secret").GetOperationStatus(context.Background(), managementTestOperationID); err == nil {
			t.Fatal("expected response bound error")
		}
	})
}

func writeManagementBookingStatus(t *testing.T, w http.ResponseWriter, code int, status bookingapi.OperationStatus) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(status); err != nil {
		t.Errorf("encode status: %v", err)
	}
}

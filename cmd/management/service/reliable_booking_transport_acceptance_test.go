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
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/hutoroff/squash-bot/internal/models"
)

func TestBookingContractAndFacilityMismatchNeverReceivesCredentialsOrMutation(t *testing.T) {
	for _, mode := range []string{"static", "registry"} {
		for _, mismatch := range []string{"v1", "other-facility"} {
			t.Run(mode+"/"+mismatch, func(t *testing.T) {
				var descriptorCalls, mutationCalls atomic.Int32
				scope := testManagementBookingScope()
				descriptor := bookingapi.Descriptor{Scope: scope, Contract: bookingapi.ContractName, ContractMajor: bookingapi.ContractMajor, Capabilities: []string{"create", "cancel", "status", "reconcile", "resolve"}}
				if mismatch == "v1" {
					descriptor.ContractMajor = 1
				} else {
					descriptor.Scope.FacilityUUID = "other-facility"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v2/eversports/descriptor" {
						descriptorCalls.Add(1)
						_ = json.NewEncoder(w).Encode(descriptor)
						return
					}
					mutationCalls.Add(1)
					w.WriteHeader(http.StatusCreated)
				}))
				defer server.Close()
				var client ReliableBookingServiceClient
				if mode == "static" {
					client = NewReliableHTTPBookingClient(server.URL, "synthetic-secret")
				} else {
					resolver := bookingResolverFunc(func(context.Context, discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
						return []discovery.ResolvedEndpoint{{API: server.URL}}, nil
					})
					client = NewDiscoveredReliableHTTPBookingClient(resolver, discovery.Requirement{ServiceKey: scope.ServiceKey, Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor, Scope: map[string]string{"facility_uuid": scope.FacilityUUID}}, "synthetic-secret")
				}
				op := testPlannedManagementOperation()
				executor, err := NewReliableBookingExecutor(client, &fakeBookingProjectionRepository{operation: op}, scope, time.Second, time.Millisecond)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := executor.ExecuteCreate(context.Background(), op, bookingapi.Credentials{Email: "account@example.test", Password: "synthetic-password"}); err == nil || descriptorCalls.Load() != 1 || mutationCalls.Load() != 0 {
					t.Fatalf("wrong contract/scope dispatch: err=%v descriptor=%d mutation=%d", err, descriptorCalls.Load(), mutationCalls.Load())
				}
			})
		}
	}
}

func TestRegistryOutageIsNotAnEmptyLedgerOrNewKey(t *testing.T) {
	resolver := bookingResolverFunc(func(context.Context, discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
		return nil, discovery.ErrUnavailable
	})
	client := NewDiscoveredReliableHTTPBookingClient(resolver, discovery.Requirement{ServiceKey: "booking.default", Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor}, "synthetic-secret")
	operation := testPlannedManagementOperation()
	executor, err := NewReliableBookingExecutor(client, &fakeBookingProjectionRepository{operation: operation}, testManagementBookingScope(), time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.ExecuteCreate(context.Background(), operation, bookingapi.Credentials{Email: "account@example.test", Password: "synthetic"})
	if !errors.Is(err, discovery.ErrUnavailable) || result.OperationID != operation.OperationID || result.State != models.ManagementBookingOperationPlanned {
		t.Fatalf("registry outage status=%+v err=%v", result, err)
	}

	var creates, statuses atomic.Int32
	scope := testManagementBookingScope()
	booking := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/eversports/descriptor":
			_ = json.NewEncoder(w).Encode(bookingapi.Descriptor{Scope: scope, Contract: bookingapi.ContractName, ContractMajor: bookingapi.ContractMajor, Capabilities: []string{"create", "cancel", "status", "reconcile", "resolve"}})
		case "/api/v2/eversports/matches":
			creates.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"ledger unavailable"}`))
		default:
			statuses.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"ledger unavailable"}`))
		}
	}))
	defer booking.Close()
	resolver = bookingResolverFunc(func(context.Context, discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
		return []discovery.ResolvedEndpoint{{API: booking.URL}}, nil
	})
	client = NewDiscoveredReliableHTTPBookingClient(resolver, discovery.Requirement{ServiceKey: "booking.default", Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor}, "synthetic-secret")
	executor, err = NewReliableBookingExecutor(client, &fakeBookingProjectionRepository{operation: operation}, scope, time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	result, err = executor.ExecuteCreate(context.Background(), operation, bookingapi.Credentials{Email: "account@example.test", Password: "synthetic"})
	if err == nil || result.OperationID != operation.OperationID || result.State != models.ManagementBookingOperationPlanned || creates.Load() != 1 || statuses.Load() != 1 {
		t.Fatalf("ledger outage must keep original pending key, no fallback: result=%+v err=%v creates=%d statuses=%d", result, err, creates.Load(), statuses.Load())
	}
}

// A malformed/lost POST response can mean that a provider mutation happened.
// Status must use the same key even when registry routing selects another
// instance; it must never send the create again with a new key or account.
func TestReliableBookingLostResponseStaticAndReplacedRegistryTarget(t *testing.T) {
	for _, registry := range []bool{false, true} {
		name := "static"
		if registry {
			name = "registry"
		}
		t.Run(name, func(t *testing.T) {
			var firstCreates, secondCreates, firstStatuses, secondStatuses atomic.Int32
			var selected atomic.Value
			credentials := bookingapi.Credentials{Email: "chosen@example.test", Password: "synthetic-password"}
			status := bookingapi.OperationStatus{OperationID: string(managementTestOperationID), Kind: bookingapi.OperationCreate, State: bookingapi.StateOutcomeUnknown, Phase: bookingapi.PhasePayment, Outcome: bookingapi.OutcomeUnknown, Code: bookingapi.ErrorOperationOutcomeUnknown, Revision: 2, Evidence: &bookingapi.ProviderEvidence{BookingUUID: "booking-1", State: "PAYMENT_UNKNOWN"}}
			scope := testManagementBookingScope()
			first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v2/eversports/descriptor":
					_ = json.NewEncoder(w).Encode(bookingapi.Descriptor{Scope: scope, Contract: bookingapi.ContractName, ContractMajor: bookingapi.ContractMajor, Capabilities: []string{"create", "cancel", "status", "reconcile", "resolve"}})
				case r.URL.Path == "/api/v2/eversports/matches" && r.Method == http.MethodPost:
					firstCreates.Add(1)
					if r.Header.Get("Idempotency-Key") != string(managementTestOperationID) {
						t.Errorf("mutated key: %q", r.Header.Get("Idempotency-Key"))
					}
					var create bookingapi.CreateRequest
					if err := json.NewDecoder(r.Body).Decode(&create); err != nil || create.Email != credentials.Email || create.Password != credentials.Password {
						t.Errorf("mutated account or invalid request: %+v %v", create, err)
					}
					if registry {
						selected.Store("replacement")
					}
					w.WriteHeader(http.StatusAccepted)
					_, _ = w.Write([]byte(`{"truncated":`)) // admitted but the caller cannot parse outcome
				case r.URL.Path == "/api/v2/eversports/operations/"+string(managementTestOperationID):
					firstStatuses.Add(1)
					_ = json.NewEncoder(w).Encode(status)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer first.Close()
			second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v2/eversports/descriptor" {
					_ = json.NewEncoder(w).Encode(bookingapi.Descriptor{Scope: scope, Contract: bookingapi.ContractName, ContractMajor: bookingapi.ContractMajor, Capabilities: []string{"create", "cancel", "status", "reconcile", "resolve"}})
					return
				}
				if r.Method == http.MethodPost {
					secondCreates.Add(1)
					w.WriteHeader(http.StatusConflict)
					return
				}
				if r.URL.Path != "/api/v2/eversports/operations/"+string(managementTestOperationID) {
					t.Errorf("replacement read path: %s", r.URL.Path)
				}
				secondStatuses.Add(1)
				_ = json.NewEncoder(w).Encode(status)
			}))
			defer second.Close()
			selected.Store("original")
			var client ReliableBookingServiceClient
			if registry {
				resolver := bookingResolverFunc(func(context.Context, discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
					address := first.URL
					if selected.Load().(string) == "replacement" {
						address = second.URL
					}
					return []discovery.ResolvedEndpoint{{InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e07120435", API: address}}, nil
				})
				client = NewDiscoveredReliableHTTPBookingClient(resolver, discovery.Requirement{ServiceKey: scope.ServiceKey, Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor, Scope: map[string]string{"facility_uuid": scope.FacilityUUID}}, strings.Repeat("s", 32))
			} else {
				client = NewReliableHTTPBookingClient(first.URL, strings.Repeat("s", 32))
			}
			op := testPlannedManagementOperation()
			repo := &fakeBookingProjectionRepository{operation: op}
			executor, err := NewReliableBookingExecutor(client, repo, scope, time.Second, time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			result, err := executor.ExecuteCreate(context.Background(), op, credentials)
			if err != nil || result.OperationID != op.OperationID || result.State != models.ManagementBookingOperationOutcomeUnknown {
				t.Fatalf("unknown persisted result=%+v err=%v", result, err)
			}
			if firstCreates.Load() != 1 || secondCreates.Load() != 0 || firstStatuses.Load()+secondStatuses.Load() != 1 {
				t.Fatalf("mutations=%d/%d status=%d/%d", firstCreates.Load(), secondCreates.Load(), firstStatuses.Load(), secondStatuses.Load())
			}
			if registry && (firstStatuses.Load() != 0 || secondStatuses.Load() != 1) {
				t.Fatalf("replacement status not read: original=%d replacement=%d", firstStatuses.Load(), secondStatuses.Load())
			}
		})
	}
}

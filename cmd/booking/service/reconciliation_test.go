package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
)

var reconciliationIdentityKey = []byte("0123456789abcdef0123456789abcdef")

var reconciliationScope = bookingapi.Scope{
	ServiceKey: "booking.eversports.primary", Provider: "eversports", FacilityID: "76443",
	FacilityUUID: "facility-uuid", FacilitySlug: "facility-slug", Sport: "squash",
}

type reconciliationStore struct {
	mu        sync.Mutex
	operation *storage.Operation
	booking   *storage.ProviderBooking
	applied   []storage.VerifiedResolution
	events    map[bookingapi.OperationID]*storage.OperationEvent
}

func (s *reconciliationStore) Get(context.Context, bookingapi.OperationID) (*storage.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.operation == nil {
		return nil, pgx.ErrNoRows
	}
	copy := *s.operation
	return &copy, nil
}
func (s *reconciliationStore) ListSteps(context.Context, bookingapi.OperationID) ([]*storage.Step, error) {
	return nil, nil
}
func (s *reconciliationStore) GetProviderBooking(context.Context, bookingapi.OperationID) (*storage.ProviderBooking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.booking == nil {
		return nil, pgx.ErrNoRows
	}
	copy := *s.booking
	return &copy, nil
}
func (s *reconciliationStore) GetEvent(_ context.Context, _ bookingapi.OperationID, eventKey bookingapi.OperationID) (*storage.OperationEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	event := s.events[eventKey]
	if event == nil {
		return nil, pgx.ErrNoRows
	}
	copy := *event
	return &copy, nil
}
func (s *reconciliationStore) ApplyVerifiedResolution(ctx context.Context, resolution storage.VerifiedResolution) (*storage.Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = append(s.applied, resolution)
	evidence, _ := json.Marshal(map[string]string{"match_id": resolution.MatchID, "provider_state": resolution.ProviderState, "actor": resolution.Actor})
	expected := resolution.ExpectedRevision
	if s.events == nil {
		s.events = make(map[bookingapi.OperationID]*storage.OperationEvent)
	}
	s.events[resolution.EventKey] = &storage.OperationEvent{
		OperationID: resolution.OperationID, EventKey: resolution.EventKey, EventType: resolution.EventType,
		ExpectedRevision: &expected, ResultingRevision: expected + 1, Reason: resolution.Reason, Evidence: evidence,
	}
	copy := *s.operation
	copy.State, copy.Outcome, copy.Phase = bookingapi.StateSucceeded, bookingapi.OutcomeSuccess, bookingapi.PhaseFinalization
	copy.Revision = resolution.ExpectedRevision + 1
	s.operation = &copy
	if s.booking == nil {
		s.booking = &storage.ProviderBooking{CreateOperationID: copy.OperationID}
	}
	s.booking.MatchID, s.booking.PaymentState = resolution.MatchID, "confirmed"
	if resolution.ProviderState == "cancelled" {
		s.booking.BookingState = "cancelled"
	} else {
		s.booking.BookingState = "confirmed"
	}
	return &copy, nil
}

type fakeMatchReader struct {
	match         *eversports.Booking
	err           error
	calls         atomic.Int32
	entered       chan struct{}
	release       chan struct{}
	afterResponse func()
}

func (r *fakeMatchReader) GetMatchByID(ctx context.Context, _ string) (*eversports.Booking, error) {
	r.calls.Add(1)
	if r.entered != nil {
		select {
		case r.entered <- struct{}{}:
		default:
		}
	}
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if r.afterResponse != nil {
		r.afterResponse()
	}
	return r.match, r.err
}

func TestReconcileCoalescesReadsAndAppliesExactKnownMatch(t *testing.T) {
	store, reader, reconciliation := newReconciliationFixture(t, bookingapi.OperationCreate)
	reader.entered, reader.release = make(chan struct{}, 1), make(chan struct{})
	credentials := bookingapi.Credentials{Email: "player@example.com", Password: "password"}
	results := make(chan error, 2)
	go func() {
		_, err := reconciliation.Reconcile(context.Background(), operation1, credentials)
		results <- err
	}()
	<-reader.entered
	go func() {
		_, err := reconciliation.Reconcile(context.Background(), operation1, credentials)
		results <- err
	}()
	close(reader.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if reader.calls.Load() != 1 {
		t.Fatalf("provider reads = %d, want 1", reader.calls.Load())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.applied) != 1 || store.applied[0].EventType != "reconciliation" || store.operation.State != bookingapi.StateSucceeded {
		t.Fatalf("applied resolutions/operation = %+v/%+v", store.applied, store.operation)
	}
}

func TestReconcileFailsClosedForMismatchedOrInsufficientEvidence(t *testing.T) {
	tests := []struct {
		name  string
		kind  bookingapi.OperationKind
		alter func(*eversports.Booking)
		want  error
	}{
		{name: "wrong facility", kind: bookingapi.OperationCreate, alter: func(match *eversports.Booking) { match.Venue.ID = "other" }, want: ErrVerificationMismatch},
		{name: "wrong time", kind: bookingapi.OperationCreate, alter: func(match *eversports.Booking) { match.Start = match.Start.Add(time.Hour) }, want: ErrVerificationMismatch},
		{name: "active cancellation is not proof", kind: bookingapi.OperationCancel, alter: func(match *eversports.Booking) { match.State = "ACCEPTED" }, want: ErrReconciliationUnresolved},
		{name: "unrecognized provider state", kind: bookingapi.OperationCreate, alter: func(match *eversports.Booking) { match.State = "PENDING_REVIEW" }, want: ErrReconciliationUnresolved},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, reader, reconciliation := newReconciliationFixture(t, test.kind)
			test.alter(reader.match)
			_, err := reconciliation.Reconcile(context.Background(), operation1, bookingapi.Credentials{Email: "player@example.com", Password: "password"})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if len(store.applied) != 0 {
				t.Fatalf("unsafe evidence applied: %+v", store.applied)
			}
		})
	}
}

func TestResolveRequiresMatchingAccountAndPreservesExplicitProvenance(t *testing.T) {
	store, reader, reconciliation := newReconciliationFixture(t, bookingapi.OperationCreate)
	request := bookingapi.ResolutionRequest{
		ExpectedRevision: 3, MatchID: "match-1", Email: "player@example.com", Password: "rotated",
		Actor: "management", Reason: "verified legacy match reference",
	}
	status, err := reconciliation.Resolve(context.Background(), operation1, operation2, request)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != bookingapi.StateSucceeded || len(store.applied) != 1 || store.applied[0].EventKey != operation2 || store.applied[0].Actor != "management" {
		t.Fatalf("status/resolution = %+v/%+v", status, store.applied)
	}
	if _, err := reconciliation.Resolve(context.Background(), operation1, operation2, request); err != nil {
		t.Fatalf("resolution replay: %v", err)
	}
	if reader.calls.Load() != 1 || len(store.applied) != 1 {
		t.Fatalf("resolution replay performed work: reads=%d applies=%d", reader.calls.Load(), len(store.applied))
	}
	request.Actor = "different-actor"
	if _, err := reconciliation.Resolve(context.Background(), operation1, operation2, request); err == nil {
		t.Fatal("changed resolution key reuse was accepted")
	}

	otherStore, _, other := newReconciliationFixture(t, bookingapi.OperationCreate)
	request.Email = "other@example.com"
	if _, err := other.Resolve(context.Background(), operation1, operation2, request); err == nil {
		t.Fatal("cross-account resolution was accepted")
	}
	if len(otherStore.applied) != 0 {
		t.Fatal("cross-account resolution persisted evidence")
	}
}

func TestResolutionPersistsProviderResponseAfterCallerCancellation(t *testing.T) {
	store, reader, reconciliation := newReconciliationFixture(t, bookingapi.OperationCreate)
	ctx, cancel := context.WithCancel(context.Background())
	reader.entered = make(chan struct{}, 1)
	reader.release = make(chan struct{})
	reader.afterResponse = cancel
	done := make(chan error, 1)
	go func() {
		_, err := reconciliation.Resolve(ctx, operation1, operation2, bookingapi.ResolutionRequest{
			ExpectedRevision: 3, MatchID: "match-1", Email: "player@example.com", Password: "password",
			Actor: "management", Reason: "verified provider response",
		})
		done <- err
	}()
	<-reader.entered
	close(reader.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(store.applied) != 1 {
		t.Fatalf("persisted resolutions = %d", len(store.applied))
	}
}

func newReconciliationFixture(t *testing.T, kind bookingapi.OperationKind) (*reconciliationStore, *fakeMatchReader, *ReconciliationService) {
	t.Helper()
	start := time.Date(2026, 8, 10, 20, 45, 0, 0, time.FixedZone("CEST", 2*60*60))
	end := start.Add(45 * time.Minute)
	identity, err := bookingapi.DeriveAccountIdentity(reconciliationIdentityKey, reconciliationScope.Provider, "player@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	operation := &storage.Operation{
		OperationID: operation1, Kind: kind, Scope: reconciliationScope, AccountIdentity: identity,
		State: bookingapi.StateOutcomeUnknown, Outcome: bookingapi.OutcomeUnknown, Phase: bookingapi.PhaseFinalization, Revision: 3,
	}
	if kind == bookingapi.OperationCreate {
		operation.Start, operation.End = &start, &end
	} else {
		operation.TargetMatchID = "match-1"
	}
	store := &reconciliationStore{operation: operation, booking: &storage.ProviderBooking{CreateOperationID: operation1, MatchID: "match-1", BookingState: "unknown", PaymentState: "unknown"}}
	reader := &fakeMatchReader{match: &eversports.Booking{ID: "match-1", Start: start, End: end, State: "ACCEPTED"}}
	reader.match.Venue.ID, reader.match.Venue.Slug = reconciliationScope.FacilityID, reconciliationScope.FacilitySlug
	reader.match.Sport.Slug = reconciliationScope.Sport
	if kind == bookingapi.OperationCancel {
		reader.match.State = "CANCELLED"
	}
	status, err := NewStatusService(store, store)
	if err != nil {
		t.Fatal(err)
	}
	reconciliation, err := NewReconciliationService(store, store, SessionFactoryFunc(func(context.Context, bookingapi.Credentials) (Session, error) {
		return reader, nil
	}), status, ReconciliationConfig{Scope: reconciliationScope, IdentityKey: reconciliationIdentityKey, PersistenceTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return store, reader, reconciliation
}

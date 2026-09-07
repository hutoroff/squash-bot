package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
)

type fakeOperationStore struct {
	mu                 sync.Mutex
	operations         map[bookingapi.OperationID]*storage.Operation
	admissions         int
	finalizations      int
	commitAcknowledged bool
}

func newFakeOperationStore() *fakeOperationStore {
	return &fakeOperationStore{operations: make(map[bookingapi.OperationID]*storage.Operation), commitAcknowledged: true}
}

func (s *fakeOperationStore) Get(_ context.Context, id bookingapi.OperationID) (*storage.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation := s.operations[id]
	if operation == nil {
		return nil, pgx.ErrNoRows
	}
	copy := *operation
	return &copy, nil
}

func (s *fakeOperationStore) AdmitCreate(_ context.Context, request bookingapi.ValidatedCreate, deadline time.Time, executor bookingapi.OperationID) (*storage.Admission, error) {
	return s.admit(request.OperationID, request.Fingerprint(), request.AccountIdentity, bookingapi.OperationCreate, deadline, executor)
}

func (s *fakeOperationStore) AdmitCancel(_ context.Context, request bookingapi.ValidatedCancel, deadline time.Time, executor bookingapi.OperationID) (*storage.Admission, error) {
	return s.admit(request.OperationID, request.Fingerprint(), request.AccountIdentity, bookingapi.OperationCancel, deadline, executor)
}

func (s *fakeOperationStore) admit(id bookingapi.OperationID, fingerprint, account string, kind bookingapi.OperationKind, deadline time.Time, executor bookingapi.OperationID) (*storage.Admission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.operations[id]; existing != nil {
		if existing.RequestFingerprint != fingerprint {
			return nil, &storage.ConflictError{Code: bookingapi.ErrorIdempotencyKeyConflict}
		}
		copy := *existing
		return &storage.Admission{Disposition: storage.AdmissionReplay, Operation: &copy}, nil
	}
	s.admissions++
	operation := &storage.Operation{
		OperationID: id, RequestFingerprint: fingerprint, Kind: kind, AccountIdentity: account,
		State: bookingapi.StateAccepted, Phase: bookingapi.PhaseAdmission, Outcome: bookingapi.OutcomePending,
		ExecutorID: string(executor), Revision: 1, DeadlineAt: deadline,
	}
	s.operations[id] = operation
	copy := *operation
	admission := &storage.Admission{Disposition: storage.AdmissionAccepted, Operation: &copy}
	if !s.commitAcknowledged {
		return nil, errors.New("commit acknowledgement lost")
	}
	return admission, nil
}

func (s *fakeOperationStore) Finalize(_ context.Context, id, executor bookingapi.OperationID, state bookingapi.OperationState, outcome bookingapi.OutcomeCategory, phase bookingapi.OperationPhase) (*storage.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation := s.operations[id]
	if operation == nil {
		return nil, pgx.ErrNoRows
	}
	if operation.ExecutorID != string(executor) {
		return nil, errors.New("stale executor")
	}
	operation.State, operation.Outcome, operation.Phase = state, outcome, phase
	operation.ExecutorID = ""
	operation.Revision++
	s.finalizations++
	copy := *operation
	return &copy, nil
}

func (s *fakeOperationStore) counts() (admissions, finalizations int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.admissions, s.finalizations
}

type fakeAuthority struct {
	healthy atomic.Bool
	lost    chan struct{}
	once    sync.Once
}

func newFakeAuthority() *fakeAuthority {
	a := &fakeAuthority{lost: make(chan struct{})}
	a.healthy.Store(true)
	return a
}
func (a *fakeAuthority) Healthy() bool         { return a.healthy.Load() }
func (a *fakeAuthority) Lost() <-chan struct{} { return a.lost }
func (a *fakeAuthority) lose() {
	a.healthy.Store(false)
	a.once.Do(func() { close(a.lost) })
}

func TestCoordinatorDefaultsGenerateCanonicalExecutorAndEnforceSingleFlow(t *testing.T) {
	store := newFakeOperationStore()
	authority := newFakeAuthority()
	manager, err := NewSessionManager(SessionFactoryFunc(func(context.Context, bookingapi.Credentials) (Session, error) {
		return &testSession{}, nil
	}), SessionManagerConfig{CacheKey: []byte("0123456789abcdef")})
	if err != nil {
		t.Fatal(err)
	}
	runner := RunnerFunc(func(context.Context, Execution) (Completion, error) { return unknownCompletion(), nil })
	coordinator, err := NewCoordinator(store, manager, runner, authority, slog.New(slog.NewTextHandler(io.Discard, nil)), CoordinatorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bookingapi.ParseOperationID(string(coordinator.ExecutorID())); err != nil {
		t.Fatalf("generated executor ID is invalid: %v", err)
	}
	shutdownCoordinator(t, coordinator)

	manager = newTestSessionManager(t, 1, time.Hour, func(context.Context, bookingapi.Credentials) (Session, error) { return &testSession{}, nil })
	if _, err := NewCoordinator(store, manager, runner, newFakeAuthority(), nil, CoordinatorConfig{MaxConcurrent: 2}); err == nil {
		t.Fatal("coordinator accepted multiple external mutation flows")
	}
}

func TestCoordinatorConcurrentSameKeyStartsOneExecution(t *testing.T) {
	store := newFakeOperationStore()
	authority := newFakeAuthority()
	runnerStarted := make(chan struct{})
	releaseRunner := make(chan struct{})
	var runs atomic.Int32
	coordinator := newTestCoordinator(t, store, authority, RunnerFunc(func(context.Context, Execution) (Completion, error) {
		if runs.Add(1) == 1 {
			close(runnerStarted)
		}
		<-releaseRunner
		return unknownCompletion(), nil
	}), 1, time.Second)
	request := testCreate(operation1, "account-1")

	start := make(chan struct{})
	results := make(chan *storage.Admission, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			admission, err := coordinator.SubmitCreate(context.Background(), request, testCredentials)
			if err != nil {
				errs <- err
				return
			}
			results <- admission
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("SubmitCreate: %v", err)
	}
	accepted, replayed := 0, 0
	for result := range results {
		switch result.Disposition {
		case storage.AdmissionAccepted:
			accepted++
		case storage.AdmissionReplay:
			replayed++
		}
	}
	if accepted != 1 || replayed != 7 {
		t.Fatalf("accepted/replayed = %d/%d, want 1/7", accepted, replayed)
	}
	select {
	case <-runnerStarted:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	if runs.Load() != 1 {
		t.Fatalf("runner calls = %d, want 1", runs.Load())
	}
	close(releaseRunner)
	shutdownCoordinator(t, coordinator)
}

func TestCoordinatorExecutionOutlivesCallerContext(t *testing.T) {
	store := newFakeOperationStore()
	authority := newFakeAuthority()
	started := make(chan struct{})
	release := make(chan struct{})
	cancelled := make(chan struct{}, 1)
	coordinator := newTestCoordinator(t, store, authority, RunnerFunc(func(ctx context.Context, _ Execution) (Completion, error) {
		close(started)
		select {
		case <-ctx.Done():
			cancelled <- struct{}{}
			return unknownCompletion(), ctx.Err()
		case <-release:
			return unknownCompletion(), nil
		}
	}), 1, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := coordinator.SubmitCreate(ctx, testCreate(operation1, "account-1"), testCredentials); err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	select {
	case <-cancelled:
		t.Fatal("caller cancellation stopped accepted execution")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	shutdownCoordinator(t, coordinator)
}

func TestCoordinatorAuthorityLossFailsClosedAndCancelsExecutor(t *testing.T) {
	store := newFakeOperationStore()
	authority := newFakeAuthority()
	started := make(chan struct{})
	stopped := make(chan struct{})
	coordinator := newTestCoordinator(t, store, authority, RunnerFunc(func(ctx context.Context, _ Execution) (Completion, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return unknownCompletion(), ctx.Err()
	}), 1, time.Second)
	if _, err := coordinator.SubmitCreate(context.Background(), testCreate(operation1, "account-1"), testCredentials); err != nil {
		t.Fatal(err)
	}
	<-started
	authority.lose()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("executor was not cancelled after authority loss")
	}
	if _, err := coordinator.SubmitCreate(context.Background(), testCreate(operation2, "account-2"), testCredentials); !errors.Is(err, ErrCoordinatorClosed) {
		t.Fatalf("new submission error = %v, want coordinator closed", err)
	}
	waitOperationState(t, store, operation1, bookingapi.StateOutcomeUnknown)
	shutdownCoordinator(t, coordinator)
}

func TestCoordinatorCloseAdmissionUnblocksWaitingSubmission(t *testing.T) {
	store := newFakeOperationStore()
	authority := newFakeAuthority()
	coordinator := newTestCoordinator(t, store, authority, RunnerFunc(func(context.Context, Execution) (Completion, error) {
		return unknownCompletion(), nil
	}), 1, time.Second)
	gate, err := coordinator.acquireOperationGate(context.Background(), operation1)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := coordinator.SubmitCreate(context.Background(), testCreate(operation1, "account-1"), testCredentials)
		result <- err
	}()
	time.Sleep(20 * time.Millisecond)
	coordinator.CloseAdmission()
	select {
	case err := <-result:
		if !errors.Is(err, ErrCoordinatorClosed) {
			t.Fatalf("waiting submission error = %v, want coordinator closed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting submission was not unblocked by admission close")
	}
	coordinator.releaseOperationGate(operation1, gate)
	shutdownCoordinator(t, coordinator)
}

func TestCoordinatorRejectsCapacityBeforeAdmission(t *testing.T) {
	store := newFakeOperationStore()
	authority := newFakeAuthority()
	started := make(chan struct{})
	release := make(chan struct{})
	coordinator := newTestCoordinator(t, store, authority, RunnerFunc(func(context.Context, Execution) (Completion, error) {
		close(started)
		<-release
		return unknownCompletion(), nil
	}), 1, 20*time.Millisecond)
	if _, err := coordinator.SubmitCreate(context.Background(), testCreate(operation1, "account-1"), testCredentials); err != nil {
		t.Fatal(err)
	}
	<-started
	_, err := coordinator.SubmitCreate(context.Background(), testCreate(operation2, "account-2"), testCredentials)
	if !errors.Is(err, ErrExecutionCapacity) {
		t.Fatalf("second submission error = %v, want capacity error", err)
	}
	admissions, _ := store.counts()
	if admissions != 1 {
		t.Fatalf("admissions = %d, want 1", admissions)
	}
	close(release)
	shutdownCoordinator(t, coordinator)
}

func TestCoordinatorResolvesLostCommitAcknowledgementWithoutSecondAdmission(t *testing.T) {
	store := newFakeOperationStore()
	store.commitAcknowledged = false
	authority := newFakeAuthority()
	release := make(chan struct{})
	var runs atomic.Int32
	coordinator := newTestCoordinator(t, store, authority, RunnerFunc(func(context.Context, Execution) (Completion, error) {
		runs.Add(1)
		<-release
		return unknownCompletion(), nil
	}), 1, time.Second)
	admission, err := coordinator.SubmitCreate(context.Background(), testCreate(operation1, "account-1"), testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if admission.Disposition != storage.AdmissionAccepted {
		t.Fatalf("disposition = %q, want accepted", admission.Disposition)
	}
	admissions, _ := store.counts()
	if admissions != 1 {
		t.Fatalf("admission writes = %d, want 1", admissions)
	}
	close(release)
	shutdownCoordinator(t, coordinator)
	if runs.Load() != 1 {
		t.Fatalf("runner calls = %d, want 1", runs.Load())
	}
}

func TestCoordinatorDoesNotTakeOverUnfinishedOperationFromPreviousExecutor(t *testing.T) {
	store := newFakeOperationStore()
	request := testCreate(operation1, "account-1")
	store.operations[operation1] = &storage.Operation{
		OperationID: operation1, RequestFingerprint: request.Fingerprint(), Kind: bookingapi.OperationCreate,
		AccountIdentity: "account-1", State: bookingapi.StateRunning, Phase: bookingapi.PhaseReserve,
		Outcome: bookingapi.OutcomePending, ExecutorID: string(operation2), Revision: 2, DeadlineAt: time.Now().Add(time.Minute),
	}
	var runs atomic.Int32
	coordinator := newTestCoordinator(t, store, newFakeAuthority(), RunnerFunc(func(context.Context, Execution) (Completion, error) {
		runs.Add(1)
		return unknownCompletion(), nil
	}), 1, time.Second)
	admission, err := coordinator.SubmitCreate(context.Background(), request, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if admission.Disposition != storage.AdmissionReplay || runs.Load() != 0 {
		t.Fatalf("unfinished takeover disposition/runs = %q/%d", admission.Disposition, runs.Load())
	}
	shutdownCoordinator(t, coordinator)
}

func TestCoordinatorRestartAndFreshCacheReplayWithoutExecution(t *testing.T) {
	store := newFakeOperationStore()
	firstAuthority := newFakeAuthority()
	first := newTestCoordinator(t, store, firstAuthority, RunnerFunc(func(context.Context, Execution) (Completion, error) {
		return unknownCompletion(), nil
	}), 1, time.Second)
	request := testCreate(operation1, "account-1")
	if _, err := first.SubmitCreate(context.Background(), request, testCredentials); err != nil {
		t.Fatal(err)
	}
	waitOperationState(t, store, operation1, bookingapi.StateOutcomeUnknown)
	shutdownCoordinator(t, first)

	secondAuthority := newFakeAuthority()
	var secondRuns atomic.Int32
	second := newTestCoordinator(t, store, secondAuthority, RunnerFunc(func(context.Context, Execution) (Completion, error) {
		secondRuns.Add(1)
		return unknownCompletion(), nil
	}), 1, time.Second)
	admission, err := second.SubmitCreate(context.Background(), request, bookingapi.Credentials{Email: "player@example.com", Password: "rotated"})
	if err != nil {
		t.Fatal(err)
	}
	if admission.Disposition != storage.AdmissionReplay {
		t.Fatalf("restart disposition = %q, want replay", admission.Disposition)
	}
	if secondRuns.Load() != 0 {
		t.Fatalf("restart runner calls = %d, want 0", secondRuns.Load())
	}
	shutdownCoordinator(t, second)
}

func TestCoordinatorSessionConstructionFailureRejectsBeforeAdmission(t *testing.T) {
	store := newFakeOperationStore()
	authority := newFakeAuthority()
	manager, err := NewSessionManager(SessionFactoryFunc(func(context.Context, bookingapi.Credentials) (Session, error) {
		return nil, errors.New("session constructor failed")
	}), SessionManagerConfig{MaxEntries: 1, IdleTTL: time.Hour, CacheKey: []byte("0123456789abcdef")})
	if err != nil {
		t.Fatal(err)
	}
	var runs atomic.Int32
	coordinator, err := NewCoordinator(store, manager, RunnerFunc(func(context.Context, Execution) (Completion, error) {
		runs.Add(1)
		return unknownCompletion(), nil
	}), authority, slog.New(slog.NewTextHandler(io.Discard, nil)), testCoordinatorConfig(1, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.SubmitCreate(context.Background(), testCreate(operation1, "account-1"), testCredentials); err == nil {
		t.Fatal("submission unexpectedly succeeded")
	}
	admissions, finalizations := store.counts()
	if admissions != 0 || finalizations != 0 {
		t.Fatalf("admissions/finalizations = %d/%d, want 0/0", admissions, finalizations)
	}
	if runs.Load() != 0 {
		t.Fatalf("runner calls = %d, want 0", runs.Load())
	}
	shutdownCoordinator(t, coordinator)
}

func newTestCoordinator(t *testing.T, store *fakeOperationStore, authority *fakeAuthority, runner Runner, maxConcurrent int, admissionWait time.Duration) *Coordinator {
	t.Helper()
	manager := newTestSessionManager(t, 8, time.Hour, func(context.Context, bookingapi.Credentials) (Session, error) {
		return &testSession{}, nil
	})
	coordinator, err := NewCoordinator(store, manager, runner, authority, slog.New(slog.NewTextHandler(io.Discard, nil)), testCoordinatorConfig(maxConcurrent, admissionWait))
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func testCoordinatorConfig(maxConcurrent int, admissionWait time.Duration) CoordinatorConfig {
	return CoordinatorConfig{
		ExecutorID: executor1, MaxConcurrent: maxConcurrent, AdmissionWait: admissionWait,
		ExecutionTimeout: time.Second, FinalizationTimeout: time.Second,
	}
}

func shutdownCoordinator(t *testing.T, coordinator *Coordinator) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := coordinator.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func waitOperationState(t *testing.T, store *fakeOperationStore, id bookingapi.OperationID, state bookingapi.OperationState) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		operation, err := store.Get(context.Background(), id)
		if err == nil && operation.State == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	operation, _ := store.Get(context.Background(), id)
	t.Fatalf("operation state = %q, want %q", operation.State, state)
}

func testCreate(id bookingapi.OperationID, account string) bookingapi.ValidatedCreate {
	return bookingapi.ValidatedCreate{
		OperationID: id, AccountIdentity: account, CourtUUID: "court-1",
		Scope: bookingapi.Scope{ServiceKey: "booking.test", Provider: "eversports", FacilityID: "1", FacilityUUID: "facility-1", FacilitySlug: "facility", Sport: "squash"},
		Start: time.Date(2026, 4, 12, 8, 45, 0, 0, time.FixedZone("CEST", 2*60*60)),
		End:   time.Date(2026, 4, 12, 9, 30, 0, 0, time.FixedZone("CEST", 2*60*60)),
	}
}

func unknownCompletion() Completion {
	return Completion{State: bookingapi.StateOutcomeUnknown, Outcome: bookingapi.OutcomeUnknown, Phase: bookingapi.PhaseReserve}
}

var testCredentials = bookingapi.Credentials{Email: "player@example.com", Password: "test-password"}

const (
	operation1 = bookingapi.OperationID("0195f8db-7a6f-7c7b-8f71-0f6e0712042d")
	operation2 = bookingapi.OperationID("550e8400-e29b-41d4-a716-446655440000")
	executor1  = bookingapi.OperationID("018f8d2c-6f7a-7b01-9341-aaaaaaaaaaaa")
)

package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
)

var (
	ErrCoordinatorClosed = errors.New("booking coordinator is not accepting operations")
	ErrExecutionCapacity = errors.New("booking execution capacity unavailable")

	_ OperationStore     = (*storage.OperationRepo)(nil)
	_ ExecutionAuthority = (*storage.SingletonGuard)(nil)
)

type OperationStore interface {
	Get(context.Context, bookingapi.OperationID) (*storage.Operation, error)
	AdmitCreate(context.Context, bookingapi.ValidatedCreate, time.Time, bookingapi.OperationID) (*storage.Admission, error)
	AdmitCancel(context.Context, bookingapi.ValidatedCancel, time.Time, bookingapi.OperationID) (*storage.Admission, error)
	Finalize(context.Context, bookingapi.OperationID, bookingapi.OperationID, bookingapi.OperationState, bookingapi.OutcomeCategory, bookingapi.OperationPhase) (*storage.Operation, error)
}

type ExecutionAuthority interface {
	Healthy() bool
	Lost() <-chan struct{}
}

type Runner interface {
	Run(context.Context, Execution) (Completion, error)
}

type RunnerFunc func(context.Context, Execution) (Completion, error)

func (f RunnerFunc) Run(ctx context.Context, execution Execution) (Completion, error) {
	return f(ctx, execution)
}

type Execution struct {
	Operation   *storage.Operation
	ExecutorID  bookingapi.OperationID
	Credentials bookingapi.Credentials
	Session     Session
}

type Completion struct {
	State          bookingapi.OperationState
	Outcome        bookingapi.OutcomeCategory
	Phase          bookingapi.OperationPhase
	DiscardSession bool
}

type CoordinatorConfig struct {
	ExecutorID          bookingapi.OperationID
	MaxConcurrent       int
	AdmissionWait       time.Duration
	ExecutionTimeout    time.Duration
	FinalizationTimeout time.Duration
	Clock               func() time.Time
}

type Coordinator struct {
	store     OperationStore
	sessions  *SessionManager
	runner    Runner
	authority ExecutionAuthority
	logger    *slog.Logger
	cfg       CoordinatorConfig

	rootCtx         context.Context
	rootCancel      context.CancelFunc
	ready           atomic.Bool
	capacity        chan struct{}
	admissionClosed chan struct{}
	admissionOnce   sync.Once

	startMu sync.Mutex
	wg      sync.WaitGroup

	activeMu sync.Mutex
	active   map[bookingapi.OperationID]context.CancelFunc

	gateMu sync.Mutex
	gates  map[bookingapi.OperationID]*operationGate
}

type operationGate struct {
	token chan struct{}
	refs  int
}

func NewCoordinator(store OperationStore, sessions *SessionManager, runner Runner, authority ExecutionAuthority, logger *slog.Logger, cfg CoordinatorConfig) (*Coordinator, error) {
	if store == nil || sessions == nil || runner == nil || authority == nil {
		return nil, fmt.Errorf("booking coordinator dependencies are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = 1
	} else if cfg.MaxConcurrent != 1 {
		return nil, fmt.Errorf("booking coordinator supports exactly one concurrent external mutation flow")
	}
	if cfg.AdmissionWait == 0 {
		cfg.AdmissionWait = time.Second
	} else if cfg.AdmissionWait < 0 {
		return nil, fmt.Errorf("booking admission wait must be positive")
	}
	if cfg.ExecutionTimeout == 0 {
		cfg.ExecutionTimeout = 90 * time.Second
	} else if cfg.ExecutionTimeout < 0 {
		return nil, fmt.Errorf("booking execution timeout must be positive")
	}
	if cfg.FinalizationTimeout == 0 {
		cfg.FinalizationTimeout = 5 * time.Second
	} else if cfg.FinalizationTimeout < 0 {
		return nil, fmt.Errorf("booking finalization timeout must be positive")
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.ExecutorID == "" {
		id, err := newOperationID()
		if err != nil {
			return nil, err
		}
		cfg.ExecutorID = id
	} else if _, err := bookingapi.ParseOperationID(string(cfg.ExecutorID)); err != nil {
		return nil, fmt.Errorf("invalid executor ID: %w", err)
	}
	rootCtx, rootCancel := context.WithCancel(context.Background())
	coordinator := &Coordinator{
		store: store, sessions: sessions, runner: runner, authority: authority, logger: logger, cfg: cfg,
		rootCtx: rootCtx, rootCancel: rootCancel, capacity: make(chan struct{}, cfg.MaxConcurrent),
		admissionClosed: make(chan struct{}), active: make(map[bookingapi.OperationID]context.CancelFunc),
		gates: make(map[bookingapi.OperationID]*operationGate),
	}
	coordinator.ready.Store(authority.Healthy())
	go coordinator.watchAuthority()
	return coordinator, nil
}

func (c *Coordinator) ExecutorID() bookingapi.OperationID { return c.cfg.ExecutorID }
func (c *Coordinator) Ready() bool                        { return c.ready.Load() && c.authority.Healthy() }

func (c *Coordinator) SubmitCreate(ctx context.Context, request bookingapi.ValidatedCreate, credentials bookingapi.Credentials) (*storage.Admission, error) {
	return c.submit(ctx, request.OperationID, request.Fingerprint(), request.AccountIdentity, credentials, func(admissionCtx context.Context, deadline time.Time) (*storage.Admission, error) {
		return c.store.AdmitCreate(admissionCtx, request, deadline, c.cfg.ExecutorID)
	})
}

func (c *Coordinator) SubmitCancel(ctx context.Context, request bookingapi.ValidatedCancel, credentials bookingapi.Credentials) (*storage.Admission, error) {
	return c.submit(ctx, request.OperationID, request.Fingerprint(), request.AccountIdentity, credentials, func(admissionCtx context.Context, deadline time.Time) (*storage.Admission, error) {
		return c.store.AdmitCancel(admissionCtx, request, deadline, c.cfg.ExecutorID)
	})
}

func (c *Coordinator) submit(
	ctx context.Context,
	operationID bookingapi.OperationID,
	fingerprint string,
	accountIdentity string,
	credentials bookingapi.Credentials,
	admit func(context.Context, time.Time) (*storage.Admission, error),
) (*storage.Admission, error) {
	if !c.Ready() {
		return nil, ErrCoordinatorClosed
	}
	admissionCtx, cancelAdmission := context.WithTimeout(ctx, c.cfg.AdmissionWait)
	defer cancelAdmission()
	gate, err := c.acquireOperationGate(admissionCtx, operationID)
	if err != nil {
		return nil, err
	}
	defer c.releaseOperationGate(operationID, gate)
	if !c.Ready() {
		return nil, ErrCoordinatorClosed
	}
	if existing, err := c.store.Get(admissionCtx, operationID); err == nil {
		return replayAdmission(existing, fingerprint)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("read booking operation before admission: %w", err)
	}

	select {
	case c.capacity <- struct{}{}:
	case <-c.rootCtx.Done():
		return nil, ErrCoordinatorClosed
	case <-c.admissionClosed:
		return nil, ErrCoordinatorClosed
	case <-c.authority.Lost():
		return nil, ErrCoordinatorClosed
	case <-admissionCtx.Done():
		return nil, fmt.Errorf("%w: %v", ErrExecutionCapacity, admissionCtx.Err())
	}
	capacityOwned := true
	defer func() {
		if capacityOwned {
			<-c.capacity
		}
	}()
	if !c.Ready() {
		return nil, ErrCoordinatorClosed
	}
	lease, err := c.sessions.Acquire(admissionCtx, accountIdentity, credentials)
	if err != nil {
		return nil, fmt.Errorf("prepare booking session before admission: %w", err)
	}
	leaseOwned := true
	defer func() {
		if leaseOwned {
			lease.Release()
		}
	}()
	if !c.Ready() {
		return nil, ErrCoordinatorClosed
	}

	deadline := c.cfg.Clock().Add(c.cfg.ExecutionTimeout)
	admission, err := admit(admissionCtx, deadline)
	if err != nil {
		resolutionCtx, cancelResolution := context.WithTimeout(c.rootCtx, c.cfg.AdmissionWait)
		admission, err = c.resolveAdmissionError(resolutionCtx, operationID, fingerprint, err)
		cancelResolution()
		if err != nil {
			return nil, err
		}
	}
	if admission.Disposition == storage.AdmissionReplay {
		return admission, nil
	}
	if admission.Operation == nil {
		return nil, fmt.Errorf("accepted booking admission has no operation")
	}
	started, alreadyRunning := c.startExecution(admission.Operation, credentials, lease)
	if !started {
		if alreadyRunning {
			admission.Disposition = storage.AdmissionReplay
			return admission, nil
		}
		c.finalizeNotDispatched(operationID)
		return admission, ErrCoordinatorClosed
	}
	capacityOwned = false // executor releases the slot after durable finalization
	leaseOwned = false    // executor holds the account/session lease through provider work
	return admission, nil
}

func replayAdmission(operation *storage.Operation, fingerprint string) (*storage.Admission, error) {
	if operation.RequestFingerprint != fingerprint {
		return nil, &storage.ConflictError{
			Code: bookingapi.ErrorIdempotencyKeyConflict, ConflictingOperationID: string(operation.OperationID),
		}
	}
	return &storage.Admission{Disposition: storage.AdmissionReplay, Operation: operation}, nil
}

func (c *Coordinator) resolveAdmissionError(ctx context.Context, operationID bookingapi.OperationID, fingerprint string, admissionErr error) (*storage.Admission, error) {
	operation, err := c.store.Get(ctx, operationID)
	if err != nil {
		return nil, admissionErr
	}
	admission, err := replayAdmission(operation, fingerprint)
	if err != nil {
		return nil, err
	}
	if operation.ExecutorID == string(c.cfg.ExecutorID) &&
		(operation.State == bookingapi.StateAccepted || operation.State == bookingapi.StateRunning) {
		admission.Disposition = storage.AdmissionAccepted
	}
	return admission, nil
}

func (c *Coordinator) startExecution(operation *storage.Operation, credentials bookingapi.Credentials, lease *SessionLease) (started, alreadyRunning bool) {
	c.startMu.Lock()
	defer c.startMu.Unlock()
	if !c.Ready() {
		return false, false
	}
	c.activeMu.Lock()
	if _, exists := c.active[operation.OperationID]; exists {
		c.activeMu.Unlock()
		return false, true
	}
	remaining := operation.DeadlineAt.Sub(c.cfg.Clock())
	if remaining < 0 {
		remaining = 0
	}
	executionCtx, cancel := context.WithTimeout(c.rootCtx, remaining)
	c.active[operation.OperationID] = cancel
	c.activeMu.Unlock()
	c.wg.Add(1)
	go c.execute(executionCtx, operation, credentials, lease)
	return true, false
}

func (c *Coordinator) execute(ctx context.Context, operation *storage.Operation, credentials bookingapi.Credentials, lease *SessionLease) {
	defer c.wg.Done()
	defer func() { <-c.capacity }()
	defer func() {
		c.activeMu.Lock()
		cancel := c.active[operation.OperationID]
		delete(c.active, operation.OperationID)
		c.activeMu.Unlock()
		if cancel != nil {
			cancel()
		}
	}()

	defer lease.Release()
	completion, err := c.runner.Run(ctx, Execution{
		Operation: operation, ExecutorID: c.cfg.ExecutorID, Credentials: credentials, Session: lease.Session,
	})
	if !validCompletion(completion) {
		completion = Completion{State: bookingapi.StateOutcomeUnknown, Outcome: bookingapi.OutcomeUnknown, Phase: operation.Phase}
	}
	if completion.Phase == "" {
		completion.Phase = operation.Phase
	}
	if err != nil {
		c.logger.Error("booking execution failed", "operation_id", operation.OperationID, "phase", completion.Phase, "err", err)
	}
	if completion.DiscardSession {
		lease.Discard()
	}

	finalizeCtx, cancel := context.WithTimeout(context.Background(), c.cfg.FinalizationTimeout)
	defer cancel()
	if _, finalizeErr := c.store.Finalize(finalizeCtx, operation.OperationID, c.cfg.ExecutorID, completion.State, completion.Outcome, completion.Phase); finalizeErr != nil {
		c.logger.Error("booking execution finalization failed", "operation_id", operation.OperationID, "phase", completion.Phase, "err", finalizeErr)
	}
}

func validCompletion(completion Completion) bool {
	switch completion.State {
	case bookingapi.StateNotDispatched, bookingapi.StateRejectedNoEffect:
		return completion.Outcome == bookingapi.OutcomeNoEffect
	case bookingapi.StateSucceeded:
		return completion.Outcome == bookingapi.OutcomeSuccess || completion.Outcome == bookingapi.OutcomeSuccessWithWarning
	case bookingapi.StateOutcomeUnknown:
		return completion.Outcome == bookingapi.OutcomeUnknown
	default:
		return false
	}
}

func (c *Coordinator) finalizeNotDispatched(operationID bookingapi.OperationID) {
	ctx, cancel := context.WithTimeout(context.Background(), c.cfg.FinalizationTimeout)
	defer cancel()
	if _, err := c.store.Finalize(ctx, operationID, c.cfg.ExecutorID, bookingapi.StateNotDispatched, bookingapi.OutcomeNoEffect, bookingapi.PhaseAdmission); err != nil {
		c.logger.Error("booking admission finalization failed", "operation_id", operationID, "err", err)
	}
}

func (c *Coordinator) CloseAdmission() {
	c.startMu.Lock()
	c.ready.Store(false)
	c.admissionOnce.Do(func() { close(c.admissionClosed) })
	c.startMu.Unlock()
}

func (c *Coordinator) Shutdown(ctx context.Context) error {
	c.CloseAdmission()
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		c.rootCancel()
		c.sessions.Close()
		return nil
	case <-ctx.Done():
		c.rootCancel()
		return ctx.Err()
	}
}

func (c *Coordinator) Running() int {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	return len(c.active)
}

func (c *Coordinator) watchAuthority() {
	select {
	case <-c.authority.Lost():
		c.startMu.Lock()
		c.ready.Store(false)
		c.admissionOnce.Do(func() { close(c.admissionClosed) })
		c.rootCancel()
		c.startMu.Unlock()
	case <-c.rootCtx.Done():
	}
}

func (c *Coordinator) acquireOperationGate(ctx context.Context, operationID bookingapi.OperationID) (*operationGate, error) {
	c.gateMu.Lock()
	gate := c.gates[operationID]
	if gate == nil {
		gate = &operationGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		c.gates[operationID] = gate
	}
	gate.refs++
	c.gateMu.Unlock()
	select {
	case <-ctx.Done():
		c.gateMu.Lock()
		gate.refs--
		if gate.refs == 0 {
			delete(c.gates, operationID)
		}
		c.gateMu.Unlock()
		return nil, ctx.Err()
	case <-c.admissionClosed:
		c.gateMu.Lock()
		gate.refs--
		if gate.refs == 0 {
			delete(c.gates, operationID)
		}
		c.gateMu.Unlock()
		return nil, ErrCoordinatorClosed
	case <-gate.token:
		return gate, nil
	}
}

func (c *Coordinator) releaseOperationGate(operationID bookingapi.OperationID, gate *operationGate) {
	gate.token <- struct{}{}
	c.gateMu.Lock()
	gate.refs--
	if gate.refs == 0 {
		delete(c.gates, operationID)
	}
	c.gateMu.Unlock()
}

func newOperationID() (bookingapi.OperationID, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate executor ID: %w", err)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return bookingapi.OperationID(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])), nil
}

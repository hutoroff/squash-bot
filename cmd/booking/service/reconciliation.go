package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
)

var (
	ErrReconciliationUnresolved = errors.New("provider read did not prove a terminal booking outcome")
	ErrVerificationMismatch     = errors.New("provider match does not match the durable operation")
)

type ReconciliationOperationStore interface {
	Get(context.Context, bookingapi.OperationID) (*storage.Operation, error)
}

type ReconciliationEvidenceStore interface {
	GetProviderBooking(context.Context, bookingapi.OperationID) (*storage.ProviderBooking, error)
	GetEvent(context.Context, bookingapi.OperationID, bookingapi.OperationID) (*storage.OperationEvent, error)
	ApplyVerifiedResolution(context.Context, storage.VerifiedResolution) (*storage.Operation, error)
}

type ReconciliationStatusReader interface {
	StatusFromOperation(context.Context, *storage.Operation) (*bookingapi.OperationStatus, error)
}

type MatchReader interface {
	GetMatchByID(context.Context, string) (*eversports.Booking, error)
}

// ReconciliationConfig uses SessionFactory directly rather than SessionManager;
// callers must provide a factory that constructs a fresh isolated read session.
type ReconciliationConfig struct {
	Scope              bookingapi.Scope
	IdentityKey        []byte
	PersistenceTimeout time.Duration
}

type ReconciliationService struct {
	operations         ReconciliationOperationStore
	evidence           ReconciliationEvidenceStore
	factory            SessionFactory
	status             ReconciliationStatusReader
	scope              bookingapi.Scope
	identityKey        []byte
	persistenceTimeout time.Duration

	gatesMu sync.Mutex
	gates   map[bookingapi.OperationID]*reconciliationGate
}

type reconciliationGate struct {
	mu   sync.Mutex
	refs int
}

func NewReconciliationService(
	operations ReconciliationOperationStore,
	evidence ReconciliationEvidenceStore,
	factory SessionFactory,
	status ReconciliationStatusReader,
	cfg ReconciliationConfig,
) (*ReconciliationService, error) {
	if operations == nil || evidence == nil || factory == nil || status == nil {
		return nil, fmt.Errorf("reconciliation stores, session factory, and status reader are required")
	}
	if cfg.PersistenceTimeout == 0 {
		cfg.PersistenceTimeout = 5 * time.Second
	}
	if cfg.PersistenceTimeout < 0 {
		return nil, fmt.Errorf("reconciliation persistence timeout must be positive")
	}
	if _, err := bookingapi.ValidateCancel("018f8d2c-6f7a-7b01-9341-aaaaaaaaaaaa", cfg.Scope, cfg.IdentityKey, "config-check", bookingapi.CancelRequest{
		Email: "config@example.invalid", Password: "config-check",
	}); err != nil {
		return nil, fmt.Errorf("invalid reconciliation scope or identity configuration: %w", err)
	}
	return &ReconciliationService{
		operations: operations, evidence: evidence, factory: factory, status: status,
		scope: cfg.Scope, identityKey: append([]byte(nil), cfg.IdentityKey...),
		persistenceTimeout: cfg.PersistenceTimeout, gates: make(map[bookingapi.OperationID]*reconciliationGate),
	}, nil
}

// Reconcile performs one coalesced credentialed provider read using a match ID
// already tied to the operation. It never sends a provider mutation.
func (s *ReconciliationService) Reconcile(ctx context.Context, operationID bookingapi.OperationID, credentials bookingapi.Credentials) (*bookingapi.OperationStatus, error) {
	release := s.lockOperation(operationID)
	defer release()
	operation, err := s.operations.Get(ctx, operationID)
	if err != nil {
		return nil, err
	}
	if operation.State != bookingapi.StateOutcomeUnknown {
		return s.status.StatusFromOperation(ctx, operation)
	}
	if err := s.validateCredentials(operation, credentials); err != nil {
		return nil, err
	}
	matchID := operation.TargetMatchID
	if operation.Kind == bookingapi.OperationCreate {
		providerBooking, evidenceErr := s.evidence.GetProviderBooking(ctx, operationID)
		if evidenceErr != nil {
			if errors.Is(evidenceErr, pgx.ErrNoRows) {
				status, statusErr := s.status.StatusFromOperation(ctx, operation)
				if statusErr != nil {
					return nil, statusErr
				}
				return status, ErrReconciliationUnresolved
			}
			return nil, fmt.Errorf("read reconciliation provider evidence: %w", evidenceErr)
		}
		matchID = providerBooking.MatchID
	}
	if matchID == "" {
		status, statusErr := s.status.StatusFromOperation(ctx, operation)
		if statusErr != nil {
			return nil, statusErr
		}
		return status, ErrReconciliationUnresolved
	}
	return s.verifyAndApply(ctx, operation, credentials, matchID, operation.Revision, derivedReconciliationKey(operationID, matchID), "reconciliation", "booking-reconciler", "credentialed exact-match reconciliation")
}

// Resolve verifies an explicitly supplied exact provider match and atomically
// records the keyed provenance. It cannot mark an operation no-effect.
func (s *ReconciliationService) Resolve(ctx context.Context, operationID, eventKey bookingapi.OperationID, request bookingapi.ResolutionRequest) (*bookingapi.OperationStatus, error) {
	release := s.lockOperation(operationID)
	defer release()
	operation, err := s.operations.Get(ctx, operationID)
	if err != nil {
		return nil, err
	}
	if err := bookingapi.ValidateResolutionRequest(request); err != nil {
		return nil, err
	}
	if replay, replayErr := s.replayResolution(ctx, operation, eventKey, request); replayErr != nil || replay != nil {
		return replay, replayErr
	}
	return s.verifyAndApply(ctx, operation, bookingapi.Credentials{Email: request.Email, Password: request.Password}, request.MatchID,
		request.ExpectedRevision, eventKey, "resolution", request.Actor, request.Reason)
}

func (s *ReconciliationService) replayResolution(ctx context.Context, operation *storage.Operation, eventKey bookingapi.OperationID, request bookingapi.ResolutionRequest) (*bookingapi.OperationStatus, error) {
	event, err := s.evidence.GetEvent(ctx, operation.OperationID, eventKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read booking resolution replay: %w", err)
	}
	var evidence struct {
		MatchID string `json:"match_id"`
		Actor   string `json:"actor"`
	}
	if json.Unmarshal(event.Evidence, &evidence) != nil || event.EventType != "resolution" || event.ExpectedRevision == nil ||
		*event.ExpectedRevision != request.ExpectedRevision || event.Reason != request.Reason ||
		evidence.MatchID != request.MatchID || evidence.Actor != request.Actor {
		return nil, &storage.ConflictError{Code: bookingapi.ErrorIdempotencyKeyConflict, ConflictingOperationID: string(operation.OperationID)}
	}
	current, err := s.operations.Get(ctx, operation.OperationID)
	if err != nil {
		return nil, err
	}
	return s.status.StatusFromOperation(ctx, current)
}

func (s *ReconciliationService) verifyAndApply(
	ctx context.Context,
	operation *storage.Operation,
	credentials bookingapi.Credentials,
	matchID string,
	expectedRevision int64,
	eventKey bookingapi.OperationID,
	eventType, actor, reason string,
) (*bookingapi.OperationStatus, error) {
	if operation.ExecutorID != "" || operation.State != bookingapi.StateOutcomeUnknown || operation.Revision != expectedRevision {
		return nil, &storage.ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(operation.OperationID)}
	}
	if operation.Scope != s.scope {
		return nil, &storage.ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: string(operation.OperationID)}
	}
	if err := s.validateCredentials(operation, credentials); err != nil {
		return nil, err
	}
	session, err := s.factory.NewSession(ctx, credentials)
	if err != nil {
		return nil, fmt.Errorf("construct reconciliation session: %w", err)
	}
	if closer, ok := session.(io.Closer); ok {
		defer closer.Close()
	}
	client, ok := session.(MatchReader)
	if !ok {
		return nil, fmt.Errorf("reconciliation session does not support match reads")
	}
	providerMatch, err := client.GetMatchByID(ctx, matchID)
	if err != nil {
		return nil, fmt.Errorf("read provider match for reconciliation: %w", err)
	}
	providerState, err := verifyProviderMatch(operation, matchID, providerMatch)
	if err != nil {
		return nil, err
	}
	if operation.Kind == bookingapi.OperationCancel && providerState != "cancelled" {
		status, statusErr := s.status.StatusFromOperation(ctx, operation)
		if statusErr != nil {
			return nil, statusErr
		}
		return status, ErrReconciliationUnresolved
	}
	persistenceCtx, cancel := context.WithTimeout(context.Background(), s.persistenceTimeout)
	defer cancel()
	resolved, err := s.evidence.ApplyVerifiedResolution(persistenceCtx, storage.VerifiedResolution{
		OperationID: operation.OperationID, EventKey: eventKey, ExpectedRevision: expectedRevision,
		EventType: eventType, MatchID: matchID, ProviderState: providerState, Actor: actor, Reason: reason,
	})
	if err != nil {
		return nil, err
	}
	return s.status.StatusFromOperation(persistenceCtx, resolved)
}

func (s *ReconciliationService) validateCredentials(operation *storage.Operation, credentials bookingapi.Credentials) error {
	identity, err := bookingapi.DeriveAccountIdentity(s.identityKey, operation.Scope.Provider, credentials.Email, credentials.Password)
	if err != nil {
		return &bookingapi.ValidationError{Field: "credentials", Code: "required"}
	}
	if identity != operation.AccountIdentity {
		return &storage.ConflictError{Code: bookingapi.ErrorAccountBlocked, ConflictingOperationID: string(operation.OperationID)}
	}
	return nil
}

func verifyProviderMatch(operation *storage.Operation, requestedMatchID string, match *eversports.Booking) (string, error) {
	if match == nil || match.ID != requestedMatchID || match.State == "" {
		return "", ErrVerificationMismatch
	}
	if (match.Venue.ID != operation.Scope.FacilityID && match.Venue.ID != operation.Scope.FacilityUUID) ||
		match.Venue.Slug != operation.Scope.FacilitySlug || !strings.EqualFold(match.Sport.Slug, operation.Scope.Sport) {
		return "", ErrVerificationMismatch
	}
	if operation.Kind == bookingapi.OperationCreate {
		if operation.Start == nil || operation.End == nil || !match.Start.Equal(*operation.Start) || !match.End.Equal(*operation.End) {
			return "", ErrVerificationMismatch
		}
	} else if operation.TargetMatchID != requestedMatchID {
		return "", ErrVerificationMismatch
	}
	switch strings.ToUpper(match.State) {
	case "CANCELLED":
		return "cancelled", nil
	case "ACCEPTED":
		return "active", nil
	default:
		return "", ErrReconciliationUnresolved
	}
}

func derivedReconciliationKey(operationID bookingapi.OperationID, matchID string) bookingapi.OperationID {
	sum := sha256.Sum256([]byte("booking-reconciliation-v1\x00" + string(operationID) + "\x00" + matchID))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(sum[:])
	return bookingapi.OperationID(encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32])
}

func (s *ReconciliationService) lockOperation(operationID bookingapi.OperationID) func() {
	s.gatesMu.Lock()
	gate := s.gates[operationID]
	if gate == nil {
		gate = &reconciliationGate{}
		s.gates[operationID] = gate
	}
	gate.refs++
	s.gatesMu.Unlock()
	gate.mu.Lock()
	return func() {
		gate.mu.Unlock()
		s.gatesMu.Lock()
		gate.refs--
		if gate.refs == 0 {
			delete(s.gates, operationID)
		}
		s.gatesMu.Unlock()
	}
}

package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
)

const confirmedEffect = "confirmed"

var (
	_ Runner                = (*ProviderRunner)(nil)
	_ ProviderClient        = (*eversports.Client)(nil)
	_ StepJournal           = (*storage.OperationRepo)(nil)
	_ ProviderEvidenceStore = (*storage.EvidenceRepo)(nil)
)

type ProviderClient interface {
	EnsureLoggedIn(context.Context) error
	Reauthenticate(context.Context) error
	ProviderUserID() string
	ReserveCourtProviderTime(context.Context, string, string, string, string, string) (*eversports.ReservationResult, error)
	PayOffline(context.Context, int) error
	AttachMatch(context.Context, string) (string, error)
	CompleteBookingTracking(context.Context, int, string) error
	CancelMatchOnce(context.Context, string) (*eversports.CancellationResult, error)
}

type StepJournal interface {
	AppendStepIntent(context.Context, bookingapi.OperationID, bookingapi.OperationPhase, int, bookingapi.OperationID) (*storage.Step, error)
	AppendStepEvidence(context.Context, bookingapi.OperationID, bookingapi.OperationPhase, int, string, string, any, bookingapi.OperationID) (*storage.Step, error)
	SetProviderUserAlias(context.Context, string, string, string) error
}

type ProviderEvidenceStore interface {
	UpsertProviderBooking(context.Context, storage.ProviderBooking, bookingapi.OperationID) (*storage.ProviderBooking, error)
}

type ProviderRunnerConfig struct {
	IdentityKey        []byte
	SportUUID          string
	PersistenceTimeout time.Duration
}

type ProviderRunner struct {
	steps              StepJournal
	evidence           ProviderEvidenceStore
	identityKey        []byte
	sportUUID          string
	persistenceTimeout time.Duration
}

func NewProviderRunner(steps StepJournal, evidence ProviderEvidenceStore, cfg ProviderRunnerConfig) (*ProviderRunner, error) {
	if steps == nil || evidence == nil {
		return nil, fmt.Errorf("booking provider runner stores are required")
	}
	if len(cfg.IdentityKey) < 32 {
		return nil, fmt.Errorf("booking provider alias identity key must contain at least 32 bytes")
	}
	if cfg.SportUUID == "" {
		return nil, fmt.Errorf("booking provider sport UUID is required")
	}
	if cfg.PersistenceTimeout == 0 {
		cfg.PersistenceTimeout = 5 * time.Second
	} else if cfg.PersistenceTimeout < 0 {
		return nil, fmt.Errorf("booking provider persistence timeout must be positive")
	}
	return &ProviderRunner{
		steps: steps, evidence: evidence,
		identityKey: append([]byte(nil), cfg.IdentityKey...), sportUUID: cfg.SportUUID,
		persistenceTimeout: cfg.PersistenceTimeout,
	}, nil
}

func (r *ProviderRunner) Run(ctx context.Context, execution Execution) (Completion, error) {
	if execution.Operation == nil {
		return unknownAt(bookingapi.PhaseAdmission, false), sanitizedExecutionError(bookingapi.PhaseAdmission, "missing_operation")
	}
	client, ok := execution.Session.(ProviderClient)
	if !ok {
		return notDispatchedAt(bookingapi.PhaseAdmission), sanitizedExecutionError(bookingapi.PhaseAdmission, "invalid_session")
	}
	if err := r.login(ctx, execution, client, 1, false); err != nil {
		var authErr *eversports.AuthenticationError
		if errors.As(err.providerErr, &authErr) && authErr.Rejected {
			completion := rejectedAt(bookingapi.PhaseLogin)
			completion.DiscardSession = true
			return completion, err
		}
		completion := notDispatchedAt(bookingapi.PhaseLogin)
		completion.DiscardSession = err.providerErr != nil
		return completion, err
	}

	switch execution.Operation.Kind {
	case bookingapi.OperationCreate:
		return r.runCreate(ctx, execution, client)
	case bookingapi.OperationCancel:
		return r.runCancel(ctx, execution, client)
	default:
		return notDispatchedAt(bookingapi.PhaseLogin), sanitizedExecutionError(bookingapi.PhaseLogin, "unsupported_operation_kind")
	}
}

func (r *ProviderRunner) login(ctx context.Context, execution Execution, client ProviderClient, attempt int, refresh bool) *providerExecutionError {
	if _, err := r.steps.AppendStepIntent(ctx, execution.Operation.OperationID, bookingapi.PhaseLogin, attempt, execution.ExecutorID); err != nil {
		return journalExecutionError(bookingapi.PhaseLogin, "intent_persist_failed", err)
	}
	var loginErr error
	if refresh {
		loginErr = client.Reauthenticate(ctx)
	} else {
		loginErr = client.EnsureLoggedIn(ctx)
	}
	effect, code := confirmedEffect, "authenticated"
	if loginErr != nil {
		effect, code = string(eversports.MutationNoEffect), string(eversports.MutationCodeAuthenticationFailed)
		var authErr *eversports.AuthenticationError
		if errors.As(loginErr, &authErr) && authErr.Rejected {
			code = string(eversports.MutationCodeCredentialRejected)
		}
	}
	if _, err := r.steps.AppendStepEvidence(ctx, execution.Operation.OperationID, bookingapi.PhaseLogin, attempt, effect, code, struct{}{}, execution.ExecutorID); err != nil {
		return journalExecutionError(bookingapi.PhaseLogin, "evidence_persist_failed", err)
	}
	if loginErr != nil {
		return providerExecutionFailure(bookingapi.PhaseLogin, code, loginErr)
	}
	providerUserID := client.ProviderUserID()
	if providerUserID == "" {
		return providerExecutionFailure(bookingapi.PhaseLogin, "provider_user_missing", nil)
	}
	alias, err := bookingapi.DeriveProviderAlias(r.identityKey, execution.Operation.Scope.Provider, providerUserID)
	if err != nil {
		return providerExecutionFailure(bookingapi.PhaseLogin, "provider_user_invalid", err)
	}
	if err := r.steps.SetProviderUserAlias(ctx, execution.Operation.Scope.Provider, execution.Operation.AccountIdentity, alias); err != nil {
		return journalExecutionError(bookingapi.PhaseLogin, "provider_alias_persist_failed", err)
	}
	return nil
}

func (r *ProviderRunner) runCreate(ctx context.Context, execution Execution, client ProviderClient) (Completion, error) {
	operation := execution.Operation
	reserveAttempt := 1
	reservation, mutationErr := r.reserve(ctx, execution, client, reserveAttempt)
	if mutationErr != nil {
		if !mutationErr.dispatched {
			return notDispatchedAt(bookingapi.PhaseReserve), mutationErr
		}
		if safeAuthenticationRetry(mutationErr) {
			if loginErr := r.login(ctx, execution, client, 2, true); loginErr != nil {
				completion := rejectedAt(bookingapi.PhaseReserve)
				completion.DiscardSession = loginErr.providerErr != nil
				return completion, loginErr
			}
			reserveAttempt = 2
			reservation, mutationErr = r.reserve(ctx, execution, client, reserveAttempt)
		}
		if mutationErr != nil {
			if !mutationErr.dispatched {
				// Attempt one was durably no-effect and attempt two never passed its
				// intent boundary, so this operation cannot have created a booking.
				return rejectedAt(bookingapi.PhaseReserve), mutationErr
			}
			if mutationErr.effect == string(eversports.MutationNoEffect) && mutationErr.evidenceStored {
				completion := rejectedAt(bookingapi.PhaseReserve)
				completion.DiscardSession = mutationErr.code == string(eversports.MutationCodeAuthenticationExpired)
				return completion, mutationErr
			}
			return unknownAt(bookingapi.PhaseReserve, true), mutationErr
		}
	}

	bookingEvidence := storage.ProviderBooking{
		CreateOperationID: operation.OperationID,
		Provider:          operation.Scope.Provider,
		FacilityUUID:      operation.Scope.FacilityUUID,
		BookingUUID:       reservation.BookingUUID,
		BookingID:         int64Pointer(reservation.BookingID),
		PaymentID:         int64Pointer(reservation.PaymentID),
		PaymentState:      "unknown",
		BookingState:      "reserved",
	}
	reserveEvidence := bookingapi.ProviderEvidence{
		BookingUUID: reservation.BookingUUID, BookingID: reservation.BookingID,
		PaymentID: reservation.PaymentID, State: "RESERVED",
	}
	stepErr := r.appendEvidence(ctx, execution, bookingapi.PhaseReserve, reserveAttempt, confirmedEffect, "reserved", reserveEvidence)
	_, bookingErr := r.upsertProviderBooking(bookingEvidence, execution.ExecutorID)
	if stepErr != nil || bookingErr != nil {
		return unknownAt(bookingapi.PhaseReserve, true), persistenceExecutionError(bookingapi.PhaseReserve, stepErr, bookingErr)
	}

	if err := r.intent(ctx, execution, bookingapi.PhasePayment, 1); err != nil {
		return unknownAt(bookingapi.PhasePayment, true), err
	}
	paymentErr := client.PayOffline(ctx, reservation.PaymentID)
	if paymentErr != nil {
		effect, code, _ := mutationDetails(paymentErr, eversports.MutationPhasePayment)
		if err := r.appendEvidence(ctx, execution, bookingapi.PhasePayment, 1, effect, code, reserveEvidence); err != nil {
			return unknownAt(bookingapi.PhasePayment, true), err
		}
		return unknownAt(bookingapi.PhasePayment, true), providerExecutionFailure(bookingapi.PhasePayment, code, paymentErr)
	}
	bookingEvidence.PaymentState = "confirmed"
	bookingEvidence.BookingState = "confirmed"
	stepErr = r.appendEvidence(ctx, execution, bookingapi.PhasePayment, 1, confirmedEffect, "payment_acknowledged", reserveEvidence)
	_, bookingErr = r.upsertProviderBooking(bookingEvidence, execution.ExecutorID)
	if stepErr != nil || bookingErr != nil {
		return unknownAt(bookingapi.PhasePayment, true), persistenceExecutionError(bookingapi.PhasePayment, stepErr, bookingErr)
	}

	if err := r.intent(ctx, execution, bookingapi.PhaseMatchAttachment, 1); err != nil {
		return succeededWithWarningAt(bookingapi.PhaseMatchAttachment, false), err
	}
	matchID, attachErr := client.AttachMatch(ctx, reservation.BookingUUID)
	if attachErr != nil {
		effect, code, _ := mutationDetails(attachErr, eversports.MutationPhaseMatchAttachment)
		journalErr := r.appendEvidence(ctx, execution, bookingapi.PhaseMatchAttachment, 1, effect, code, reserveEvidence)
		return succeededWithWarningAt(bookingapi.PhaseMatchAttachment, effect == string(eversports.MutationUnknown)),
			persistenceOrProviderError(bookingapi.PhaseMatchAttachment, code, journalErr, attachErr)
	}
	bookingEvidence.MatchID = matchID
	attachmentEvidence := reserveEvidence
	attachmentEvidence.MatchID = matchID
	stepErr = r.appendEvidence(ctx, execution, bookingapi.PhaseMatchAttachment, 1, confirmedEffect, "match_attached", attachmentEvidence)
	_, bookingErr = r.upsertProviderBooking(bookingEvidence, execution.ExecutorID)
	if stepErr != nil || bookingErr != nil {
		return succeededWithWarningAt(bookingapi.PhaseMatchAttachment, false), persistenceExecutionError(bookingapi.PhaseMatchAttachment, stepErr, bookingErr)
	}
	if ctx.Err() != nil {
		return succeededWithWarningAt(bookingapi.PhaseMatchAttachment, true), sanitizedExecutionError(bookingapi.PhaseMatchAttachment, "tracking_skipped")
	}
	if err := client.CompleteBookingTracking(ctx, reservation.BookingID, operation.Scope.FacilityUUID); err != nil {
		return succeededWithWarningAt(bookingapi.PhaseMatchAttachment, true), sanitizedExecutionError(bookingapi.PhaseMatchAttachment, "tracking_failed")
	}
	return Completion{State: bookingapi.StateSucceeded, Outcome: bookingapi.OutcomeSuccess, Phase: bookingapi.PhaseMatchAttachment}, nil
}

func (r *ProviderRunner) reserve(ctx context.Context, execution Execution, client ProviderClient, attempt int) (*eversports.ReservationResult, *providerExecutionError) {
	if err := r.intent(ctx, execution, bookingapi.PhaseReserve, attempt); err != nil {
		return nil, err
	}
	operation := execution.Operation
	result, err := client.ReserveCourtProviderTime(ctx, operation.Scope.FacilityUUID, operation.CourtUUID,
		r.sportUUID, operation.StartProvider, operation.EndProvider)
	if err == nil && result != nil {
		return result, nil
	}
	if err == nil {
		err = &eversports.MutationError{
			Phase: eversports.MutationPhaseReserve, Effect: eversports.MutationUnknown,
			Code: eversports.MutationCodeInvalidResponse, Err: errors.New("empty reservation result"),
		}
	}
	effect, code, retrySafe := mutationDetails(err, eversports.MutationPhaseReserve)
	journalErr := r.appendEvidence(ctx, execution, bookingapi.PhaseReserve, attempt, effect, code, struct{}{})
	if journalErr != nil {
		return nil, &providerExecutionError{phase: bookingapi.PhaseReserve, code: "evidence_persist_failed", effect: effect, retrySafe: false, dispatched: true, journalErr: journalErr, providerErr: err}
	}
	return nil, &providerExecutionError{phase: bookingapi.PhaseReserve, code: code, effect: effect, retrySafe: retrySafe, dispatched: true, evidenceStored: true, providerErr: err}
}

func (r *ProviderRunner) runCancel(ctx context.Context, execution Execution, client ProviderClient) (Completion, error) {
	cancelAttempt := 1
	result, mutationErr := r.cancel(ctx, execution, client, cancelAttempt)
	if mutationErr != nil && !mutationErr.dispatched {
		return notDispatchedAt(bookingapi.PhaseCancellation), mutationErr
	}
	if mutationErr != nil && safeAuthenticationRetry(mutationErr) {
		if loginErr := r.login(ctx, execution, client, 2, true); loginErr != nil {
			completion := rejectedAt(bookingapi.PhaseCancellation)
			completion.DiscardSession = loginErr.providerErr != nil
			return completion, loginErr
		}
		cancelAttempt = 2
		result, mutationErr = r.cancel(ctx, execution, client, cancelAttempt)
	}
	if mutationErr != nil {
		if !mutationErr.dispatched {
			return rejectedAt(bookingapi.PhaseCancellation), mutationErr
		}
		if mutationErr.effect == string(eversports.MutationNoEffect) && mutationErr.evidenceStored {
			completion := rejectedAt(bookingapi.PhaseCancellation)
			completion.DiscardSession = mutationErr.code == string(eversports.MutationCodeAuthenticationExpired)
			return completion, mutationErr
		}
		return unknownAt(bookingapi.PhaseCancellation, true), mutationErr
	}
	evidence := bookingapi.ProviderEvidence{MatchID: result.ID, State: result.State}
	if err := r.appendEvidence(ctx, execution, bookingapi.PhaseCancellation, cancelAttempt, confirmedEffect, "cancelled", evidence); err != nil {
		return unknownAt(bookingapi.PhaseCancellation, true), err
	}
	return Completion{State: bookingapi.StateSucceeded, Outcome: bookingapi.OutcomeSuccess, Phase: bookingapi.PhaseCancellation}, nil
}

func (r *ProviderRunner) cancel(ctx context.Context, execution Execution, client ProviderClient, attempt int) (*eversports.CancellationResult, *providerExecutionError) {
	if err := r.intent(ctx, execution, bookingapi.PhaseCancellation, attempt); err != nil {
		return nil, err
	}
	result, err := client.CancelMatchOnce(ctx, execution.Operation.TargetMatchID)
	if err == nil && result != nil {
		return result, nil
	}
	if err == nil {
		err = &eversports.MutationError{
			Phase: eversports.MutationPhaseCancellation, Effect: eversports.MutationUnknown,
			Code: eversports.MutationCodeInvalidResponse, Err: errors.New("empty cancellation result"),
		}
	}
	effect, code, retrySafe := mutationDetails(err, eversports.MutationPhaseCancellation)
	journalErr := r.appendEvidence(ctx, execution, bookingapi.PhaseCancellation, attempt, effect, code, struct{}{})
	if journalErr != nil {
		return nil, &providerExecutionError{phase: bookingapi.PhaseCancellation, code: "evidence_persist_failed", effect: effect, dispatched: true, journalErr: journalErr, providerErr: err}
	}
	return nil, &providerExecutionError{phase: bookingapi.PhaseCancellation, code: code, effect: effect, retrySafe: retrySafe, dispatched: true, evidenceStored: true, providerErr: err}
}

func (r *ProviderRunner) intent(ctx context.Context, execution Execution, phase bookingapi.OperationPhase, attempt int) *providerExecutionError {
	if _, err := r.steps.AppendStepIntent(ctx, execution.Operation.OperationID, phase, attempt, execution.ExecutorID); err != nil {
		return journalExecutionError(phase, "intent_persist_failed", err)
	}
	return nil
}

func (r *ProviderRunner) appendEvidence(_ context.Context, execution Execution, phase bookingapi.OperationPhase, attempt int, effect, code string, evidence any) *providerExecutionError {
	// Cancellation must stop later provider phases, but it must not discard a
	// response already received. Repository executor fencing still rejects stale
	// evidence after recovery or another ownership decision.
	ctx, cancel := context.WithTimeout(context.Background(), r.persistenceTimeout)
	defer cancel()
	if _, err := r.steps.AppendStepEvidence(ctx, execution.Operation.OperationID, phase, attempt, effect, code, evidence, execution.ExecutorID); err != nil {
		return journalExecutionError(phase, "evidence_persist_failed", err)
	}
	return nil
}

func (r *ProviderRunner) upsertProviderBooking(booking storage.ProviderBooking, executorID bookingapi.OperationID) (*storage.ProviderBooking, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.persistenceTimeout)
	defer cancel()
	return r.evidence.UpsertProviderBooking(ctx, booking, executorID)
}

func safeAuthenticationRetry(err *providerExecutionError) bool {
	return err != nil && err.dispatched && err.evidenceStored && err.retrySafe &&
		err.effect == string(eversports.MutationNoEffect) &&
		err.code == string(eversports.MutationCodeAuthenticationExpired)
}

func mutationDetails(err error, expectedPhase eversports.MutationPhase) (effect, code string, retrySafe bool) {
	var mutationErr *eversports.MutationError
	if errors.As(err, &mutationErr) {
		if mutationErr.Phase != expectedPhase {
			return string(eversports.MutationUnknown), "provider_phase_mismatch", false
		}
		return string(mutationErr.Effect), string(mutationErr.Code), mutationErr.RetrySafe
	}
	return string(eversports.MutationUnknown), "unclassified_provider_error", false
}

func int64Pointer(value int) *int64 {
	converted := int64(value)
	return &converted
}

func notDispatchedAt(phase bookingapi.OperationPhase) Completion {
	return Completion{State: bookingapi.StateNotDispatched, Outcome: bookingapi.OutcomeNoEffect, Phase: phase}
}
func rejectedAt(phase bookingapi.OperationPhase) Completion {
	return Completion{State: bookingapi.StateRejectedNoEffect, Outcome: bookingapi.OutcomeNoEffect, Phase: phase}
}
func unknownAt(phase bookingapi.OperationPhase, discard bool) Completion {
	return Completion{State: bookingapi.StateOutcomeUnknown, Outcome: bookingapi.OutcomeUnknown, Phase: phase, DiscardSession: discard}
}
func succeededWithWarningAt(phase bookingapi.OperationPhase, discard bool) Completion {
	return Completion{State: bookingapi.StateSucceeded, Outcome: bookingapi.OutcomeSuccessWithWarning, Phase: phase, DiscardSession: discard}
}

type providerExecutionError struct {
	phase          bookingapi.OperationPhase
	code           string
	effect         string
	retrySafe      bool
	dispatched     bool
	evidenceStored bool
	providerErr    error
	journalErr     error
}

func (e *providerExecutionError) Error() string {
	return fmt.Sprintf("booking provider execution %s: %s", e.phase, e.code)
}

func providerExecutionFailure(phase bookingapi.OperationPhase, code string, providerErr error) *providerExecutionError {
	return &providerExecutionError{phase: phase, code: code, providerErr: providerErr}
}
func journalExecutionError(phase bookingapi.OperationPhase, code string, journalErr error) *providerExecutionError {
	return &providerExecutionError{phase: phase, code: code, journalErr: journalErr}
}
func sanitizedExecutionError(phase bookingapi.OperationPhase, code string) *providerExecutionError {
	return &providerExecutionError{phase: phase, code: code}
}
func persistenceExecutionError(phase bookingapi.OperationPhase, stepErr *providerExecutionError, bookingErr error) *providerExecutionError {
	if stepErr != nil {
		return stepErr
	}
	return journalExecutionError(phase, "provider_evidence_persist_failed", bookingErr)
}
func persistenceOrProviderError(phase bookingapi.OperationPhase, code string, journalErr *providerExecutionError, providerErr error) *providerExecutionError {
	if journalErr != nil {
		return journalErr
	}
	return providerExecutionFailure(phase, code, providerErr)
}

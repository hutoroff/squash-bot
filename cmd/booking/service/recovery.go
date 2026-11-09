package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
)

type RecoveryOperationStore interface {
	ListUnfinished(context.Context) ([]*storage.Operation, error)
	ListSteps(context.Context, bookingapi.OperationID) ([]*storage.Step, error)
	Finalize(context.Context, bookingapi.OperationID, bookingapi.OperationID, bookingapi.OperationState, bookingapi.OutcomeCategory, bookingapi.OperationPhase) (*storage.Operation, error)
}

type RecoveryEvidenceStore interface {
	GetProviderBooking(context.Context, bookingapi.OperationID) (*storage.ProviderBooking, error)
}

type RecoveryResult struct {
	OperationID bookingapi.OperationID
	State       bookingapi.OperationState
	Outcome     bookingapi.OutcomeCategory
	Phase       bookingapi.OperationPhase
}

// Recovery classifies unfinished operations from durable evidence only. It is
// run after acquiring singleton authority and before constructing a coordinator;
// it never creates a provider session or dispatches provider work.
type Recovery struct {
	operations RecoveryOperationStore
	evidence   RecoveryEvidenceStore
	authority  ExecutionAuthority
}

// NewRecovery constructs startup recovery. Authority must be the process-wide
// singleton guard: recovery may finalize a previous executor's journal, but it
// must never overlap that executor or run after this process loses authority.
func NewRecovery(operations RecoveryOperationStore, evidence RecoveryEvidenceStore, authority ExecutionAuthority) (*Recovery, error) {
	if operations == nil || evidence == nil || authority == nil {
		return nil, fmt.Errorf("booking recovery stores and execution authority are required")
	}
	return &Recovery{operations: operations, evidence: evidence, authority: authority}, nil
}

func (r *Recovery) Run(ctx context.Context) ([]RecoveryResult, error) {
	if !r.authority.Healthy() {
		return nil, ErrCoordinatorClosed
	}
	recoveryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-r.authority.Lost():
			cancel()
		case <-recoveryCtx.Done():
		}
	}()
	unfinished, err := r.operations.ListUnfinished(recoveryCtx)
	if err != nil {
		return nil, fmt.Errorf("list unfinished booking operations: %w", err)
	}
	results := make([]RecoveryResult, 0, len(unfinished))
	for _, operation := range unfinished {
		if !r.authority.Healthy() {
			return results, ErrCoordinatorClosed
		}
		result, err := r.recoverOperation(recoveryCtx, operation)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (r *Recovery) recoverOperation(ctx context.Context, operation *storage.Operation) (RecoveryResult, error) {
	if operation == nil {
		return RecoveryResult{}, fmt.Errorf("recover booking operation: nil operation")
	}
	executorID, err := bookingapi.ParseOperationID(operation.ExecutorID)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("recover booking operation %s: invalid executor: %w", operation.OperationID, err)
	}
	steps, err := r.operations.ListSteps(ctx, operation.OperationID)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("list steps for booking operation %s: %w", operation.OperationID, err)
	}

	state, outcome := bookingapi.StateOutcomeUnknown, bookingapi.OutcomeUnknown
	if confirmed, warning, err := r.confirmedSuccess(ctx, operation, steps); err != nil {
		return RecoveryResult{}, err
	} else if confirmed {
		state, outcome = bookingapi.StateSucceeded, bookingapi.OutcomeSuccess
		if warning {
			outcome = bookingapi.OutcomeSuccessWithWarning
		}
	} else {
		intents, allNoEffect := mutationEvidence(operation.Kind, steps)
		providerEvidenceExists := false
		if operation.Kind == bookingapi.OperationCreate {
			if _, evidenceErr := r.evidence.GetProviderBooking(ctx, operation.OperationID); evidenceErr == nil {
				providerEvidenceExists = true
			} else if !errors.Is(evidenceErr, pgx.ErrNoRows) {
				return RecoveryResult{}, fmt.Errorf("read provider evidence for booking operation %s: %w", operation.OperationID, evidenceErr)
			}
		}
		switch {
		case intents == 0 && !providerEvidenceExists:
			state, outcome = bookingapi.StateNotDispatched, bookingapi.OutcomeNoEffect
		case allNoEffect && !providerEvidenceExists:
			state, outcome = bookingapi.StateRejectedNoEffect, bookingapi.OutcomeNoEffect
		}
	}

	phase := operation.Phase
	if phase == "" {
		phase = bookingapi.PhaseAdmission
	}
	finalized, err := r.operations.Finalize(ctx, operation.OperationID, executorID, state, outcome, phase)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("finalize recovered booking operation %s: %w", operation.OperationID, err)
	}
	return RecoveryResult{OperationID: finalized.OperationID, State: finalized.State, Outcome: finalized.Outcome, Phase: finalized.Phase}, nil
}

func (r *Recovery) confirmedSuccess(ctx context.Context, operation *storage.Operation, steps []*storage.Step) (confirmed, warning bool, err error) {
	if operation.Kind == bookingapi.OperationCancel {
		return hasConfirmedEvidence(steps, bookingapi.PhaseCancellation), false, nil
	}
	if operation.Kind != bookingapi.OperationCreate ||
		!hasConfirmedEvidence(steps, bookingapi.PhaseReserve) ||
		!hasConfirmedEvidence(steps, bookingapi.PhasePayment) {
		return false, false, nil
	}
	providerBooking, err := r.evidence.GetProviderBooking(ctx, operation.OperationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("read provider evidence for booking operation %s: %w", operation.OperationID, err)
	}
	confirmed = providerBooking.BookingUUID != "" && providerBooking.BookingID != nil && providerBooking.PaymentID != nil &&
		providerBooking.BookingState == "confirmed" && providerBooking.PaymentState == "confirmed"
	return confirmed, confirmed && !hasConfirmedEvidence(steps, bookingapi.PhaseMatchAttachment), nil
}

func hasConfirmedEvidence(steps []*storage.Step, phase bookingapi.OperationPhase) bool {
	for _, step := range steps {
		if step.Phase == phase && step.Status == "evidence" && step.Effect == "confirmed" {
			return true
		}
	}
	return false
}

func mutationEvidence(kind bookingapi.OperationKind, steps []*storage.Step) (intents int, allNoEffect bool) {
	evidence := make(map[string]string)
	for _, step := range steps {
		if step.Status == "evidence" && isMutationPhase(kind, step.Phase) {
			evidence[stepIdentity(step)] = step.Effect
		}
	}
	allNoEffect = true
	for _, step := range steps {
		if step.Status != "intent" || !isMutationPhase(kind, step.Phase) {
			continue
		}
		intents++
		if evidence[stepIdentity(step)] != "no_effect" {
			allNoEffect = false
		}
	}
	return intents, allNoEffect && intents > 0
}

func isMutationPhase(kind bookingapi.OperationKind, phase bookingapi.OperationPhase) bool {
	if kind == bookingapi.OperationCreate {
		return phase == bookingapi.PhaseReserve || phase == bookingapi.PhasePayment
	}
	return kind == bookingapi.OperationCancel && phase == bookingapi.PhaseCancellation
}

func stepIdentity(step *storage.Step) string {
	return fmt.Sprintf("%s:%d", step.Phase, step.Attempt)
}

package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
)

const (
	WarningOptionalProviderStepIncomplete = "optional_provider_step_incomplete"
	WarningDuplicateRiskAccepted          = "duplicate_risk_accepted"
)

type StatusOperationStore interface {
	Get(context.Context, bookingapi.OperationID) (*storage.Operation, error)
	ListSteps(context.Context, bookingapi.OperationID) ([]*storage.Step, error)
}

type StatusEvidenceStore interface {
	GetProviderBooking(context.Context, bookingapi.OperationID) (*storage.ProviderBooking, error)
}

type StatusService struct {
	operations StatusOperationStore
	evidence   StatusEvidenceStore
}

type ResultExpiredError struct {
	Status *bookingapi.OperationStatus
}

func (e *ResultExpiredError) Error() string { return string(bookingapi.ErrorOperationResultExpired) }

func NewStatusService(operations StatusOperationStore, evidence StatusEvidenceStore) (*StatusService, error) {
	if operations == nil || evidence == nil {
		return nil, fmt.Errorf("booking status stores are required")
	}
	return &StatusService{operations: operations, evidence: evidence}, nil
}

func (s *StatusService) GetStatus(ctx context.Context, operationID bookingapi.OperationID) (*bookingapi.OperationStatus, error) {
	operation, err := s.operations.Get(ctx, operationID)
	if err != nil {
		return nil, err
	}
	return s.StatusFromOperation(ctx, operation)
}

func (s *StatusService) StatusFromOperation(ctx context.Context, operation *storage.Operation) (*bookingapi.OperationStatus, error) {
	if operation == nil {
		return nil, fmt.Errorf("booking status operation is required")
	}
	status := &bookingapi.OperationStatus{
		OperationID: string(operation.OperationID), Kind: operation.Kind,
		State: operation.State, Phase: operation.Phase, Outcome: operation.Outcome,
		Revision: operation.Revision, StatusURL: OperationStatusURL(operation.OperationID),
		CreatedAt: operation.CreatedAt, UpdatedAt: operation.UpdatedAt,
	}
	if operation.ParentOperationID != "" {
		status.Warnings = append(status.Warnings, WarningDuplicateRiskAccepted)
	}
	if operation.Outcome == bookingapi.OutcomeSuccessWithWarning {
		status.Warnings = append(status.Warnings, WarningOptionalProviderStepIncomplete)
	}
	// Tombstones preserve identity and terminal summary but intentionally stop
	// querying or exposing detailed step/provider evidence.
	if operation.ResultExpiredAt != nil {
		status.Code = bookingapi.ErrorOperationResultExpired
		status.ResultExpired = true
		return status, &ResultExpiredError{Status: status}
	}
	if operation.State == bookingapi.StateOutcomeUnknown {
		status.Code = bookingapi.ErrorOperationOutcomeUnknown
	} else if operation.State == bookingapi.StateRejectedNoEffect || operation.State == bookingapi.StateNotDispatched {
		steps, err := s.operations.ListSteps(ctx, operation.OperationID)
		if err != nil {
			return nil, fmt.Errorf("read booking operation steps for status: %w", err)
		}
		status.Code = noEffectStatusCode(steps)
	}
	if operation.Kind == bookingapi.OperationCancel {
		if operation.State == bookingapi.StateSucceeded {
			status.Evidence = &bookingapi.ProviderEvidence{MatchID: operation.TargetMatchID, State: "CANCELLED"}
		}
		return status, nil
	}
	providerBooking, err := s.evidence.GetProviderBooking(ctx, operation.OperationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return status, nil
		}
		return nil, fmt.Errorf("read booking provider status evidence: %w", err)
	}
	evidence := &bookingapi.ProviderEvidence{
		BookingUUID: providerBooking.BookingUUID,
		MatchID:     providerBooking.MatchID,
		State:       providerBooking.BookingState,
	}
	maxInt := int64(^uint(0) >> 1)
	if providerBooking.BookingID != nil {
		if *providerBooking.BookingID > maxInt {
			return nil, fmt.Errorf("provider booking ID exceeds API range")
		}
		evidence.BookingID = int(*providerBooking.BookingID)
	}
	if providerBooking.PaymentID != nil {
		if *providerBooking.PaymentID > maxInt {
			return nil, fmt.Errorf("provider payment ID exceeds API range")
		}
		evidence.PaymentID = int(*providerBooking.PaymentID)
	}
	status.Evidence = evidence
	return status, nil
}

func noEffectStatusCode(steps []*storage.Step) bookingapi.ErrorCode {
	for index := len(steps) - 1; index >= 0; index-- {
		step := steps[index]
		if step == nil || step.Status != "evidence" {
			continue
		}
		switch step.Code {
		case "credential_rejected":
			return bookingapi.ErrorCredentialRejected
		case "provider_rejected":
			return bookingapi.ErrorBookingRejected
		}
	}
	return bookingapi.ErrorDependencyUnavailable
}

func OperationStatusURL(operationID bookingapi.OperationID) string {
	return "/api/v2/eversports/operations/" + string(operationID)
}

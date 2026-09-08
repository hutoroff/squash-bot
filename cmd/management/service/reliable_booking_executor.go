package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
)

type bookingOperationProjectionRepository interface {
	GetOperation(context.Context, bookingapi.OperationID) (*models.ManagementBookingOperation, error)
	ApplyOperationStatus(context.Context, *bookingapi.OperationStatus) (*models.ManagementBookingOperation, bool, error)
}

type ReliableBookingExecutor struct {
	client       ReliableBookingServiceClient
	repository   bookingOperationProjectionRepository
	waitTimeout  time.Duration
	pollInterval time.Duration
	scope        bookingapi.Scope
}

func NewReliableBookingExecutor(client ReliableBookingServiceClient, repository bookingOperationProjectionRepository, scope bookingapi.Scope, waitTimeout, pollInterval time.Duration) (*ReliableBookingExecutor, error) {
	if client == nil || repository == nil || scope.ServiceKey == "" {
		return nil, fmt.Errorf("reliable booking client and operation repository are required")
	}
	if waitTimeout <= 0 || pollInterval <= 0 || pollInterval > waitTimeout {
		return nil, fmt.Errorf("reliable booking wait timeout and poll interval are invalid")
	}
	return &ReliableBookingExecutor{client: client, repository: repository, scope: scope, waitTimeout: waitTimeout, pollInterval: pollInterval}, nil
}

func (e *ReliableBookingExecutor) ExecuteCreate(ctx context.Context, operation *models.ManagementBookingOperation, credentials bookingapi.Credentials) (*models.ManagementBookingOperation, error) {
	if operation == nil || operation.Kind != bookingapi.OperationCreate {
		return nil, fmt.Errorf("planned management create operation is required")
	}
	request := bookingapi.CreateRequest{
		CourtUUID: operation.CourtUUID, Start: operation.StartProvider, End: operation.EndProvider,
		Email: credentials.Email, Password: credentials.Password,
	}
	if operation.ParentOperationID != "" {
		request.Risk = &bookingapi.RiskAuthorization{
			ReplacesOperationID: string(operation.ParentOperationID), AcceptDuplicateRisk: true, Reason: operation.RiskReason,
		}
	}
	return e.execute(ctx, operation, func(callCtx context.Context) (*bookingapi.OperationStatus, error) {
		return e.client.SubmitCreateOperation(callCtx, operation.OperationID, request)
	})
}

func (e *ReliableBookingExecutor) ExecuteCancel(ctx context.Context, operation *models.ManagementBookingOperation, credentials bookingapi.Credentials) (*models.ManagementBookingOperation, error) {
	if operation == nil || operation.Kind != bookingapi.OperationCancel || operation.TargetMatchID == "" {
		return nil, fmt.Errorf("planned management cancellation operation is required")
	}
	request := bookingapi.CancelRequest{Email: credentials.Email, Password: credentials.Password}
	return e.execute(ctx, operation, func(callCtx context.Context) (*bookingapi.OperationStatus, error) {
		return e.client.SubmitCancelOperation(callCtx, operation.OperationID, operation.TargetMatchID, request)
	})
}

func (e *ReliableBookingExecutor) Observe(ctx context.Context, operationID bookingapi.OperationID) (*models.ManagementBookingOperation, error) {
	if err := e.client.VerifyBinding(ctx, e.scope); err != nil {
		return nil, err
	}
	status, err := e.client.GetOperationStatus(ctx, operationID)
	if err != nil {
		return nil, err
	}
	operation, _, err := e.repository.ApplyOperationStatus(ctx, status)
	if err != nil {
		return nil, fmt.Errorf("persist observed booking operation: %w", err)
	}
	return operation, nil
}

func (e *ReliableBookingExecutor) Reconcile(ctx context.Context, operationID bookingapi.OperationID, credentials bookingapi.Credentials) (*models.ManagementBookingOperation, error) {
	if err := e.client.VerifyBinding(ctx, e.scope); err != nil {
		return nil, err
	}
	status, err := e.client.ReconcileOperation(ctx, operationID, bookingapi.ReconcileRequest{Email: credentials.Email, Password: credentials.Password})
	if err != nil {
		return nil, err
	}
	operation, _, err := e.repository.ApplyOperationStatus(ctx, status)
	if err != nil {
		return nil, fmt.Errorf("persist reconciled booking operation: %w", err)
	}
	return operation, nil
}

func (e *ReliableBookingExecutor) Resolve(ctx context.Context, operationID, eventKey bookingapi.OperationID, request bookingapi.ResolutionRequest, credentials bookingapi.Credentials) (*models.ManagementBookingOperation, error) {
	if err := e.client.VerifyBinding(ctx, e.scope); err != nil {
		return nil, err
	}
	request.Email = credentials.Email
	request.Password = credentials.Password
	status, err := e.client.ResolveOperation(ctx, operationID, eventKey, request)
	if err != nil {
		return nil, err
	}
	operation, _, err := e.repository.ApplyOperationStatus(ctx, status)
	if err != nil {
		return nil, fmt.Errorf("persist resolved booking operation: %w", err)
	}
	return operation, nil
}

func (e *ReliableBookingExecutor) execute(ctx context.Context, operation *models.ManagementBookingOperation, submit func(context.Context) (*bookingapi.OperationStatus, error)) (*models.ManagementBookingOperation, error) {
	if operation.State != models.ManagementBookingOperationPlanned {
		return e.Observe(ctx, operation.OperationID)
	}
	if err := e.client.VerifyBinding(ctx, e.scope); err != nil {
		return operation, fmt.Errorf("verify booking binding before dispatch: %w", err)
	}
	status, err := submit(ctx)
	if err != nil {
		var contractError *BookingContractError
		if errors.As(err, &contractError) && bookingContractProvesNotDispatched(contractError.Code) {
			status = &bookingapi.OperationStatus{
				OperationID: string(operation.OperationID), Kind: operation.Kind,
				State: bookingapi.StateNotDispatched, Phase: bookingapi.PhaseAdmission,
				Outcome: bookingapi.OutcomeNoEffect, Code: contractError.Code, Revision: operation.Revision,
			}
			persisted, _, persistErr := e.repository.ApplyOperationStatus(ctx, status)
			if persistErr != nil {
				return operation, fmt.Errorf("persist non-dispatched booking rejection: %w", persistErr)
			}
			return persisted, nil
		}
		// The HTTP result does not establish whether booking admitted the key. Use a
		// fresh bounded ledger read; never switch account, payload, facility, or key.
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.waitTimeout)
		defer cancel()
		observed, observeErr := e.Observe(readCtx, operation.OperationID)
		if observeErr == nil {
			return observed, nil
		}
		if errors.As(observeErr, &contractError) && contractError.Code == bookingapi.ErrorOperationNotFound {
			return operation, err
		}
		return operation, fmt.Errorf("submit booking operation: %w (same-key status unavailable: %v)", err, observeErr)
	}
	persisted, _, err := e.repository.ApplyOperationStatus(ctx, status)
	if err != nil {
		return operation, fmt.Errorf("persist submitted booking operation: %w", err)
	}
	if !managementBookingOperationPending(persisted) {
		return persisted, nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, e.waitTimeout)
	defer cancel()
	ticker := time.NewTicker(e.pollInterval)
	defer ticker.Stop()
	for managementBookingOperationPending(persisted) {
		select {
		case <-waitCtx.Done():
			return persisted, nil
		case <-ticker.C:
			observed, observeErr := e.Observe(waitCtx, operation.OperationID)
			if observeErr != nil {
				return persisted, fmt.Errorf("observe accepted booking operation: %w", observeErr)
			}
			persisted = observed
		}
	}
	return persisted, nil
}

func bookingContractProvesNotDispatched(code bookingapi.ErrorCode) bool {
	switch code {
	case bookingapi.ErrorInvalidRequest, bookingapi.ErrorBookingConflict, bookingapi.ErrorAccountBlocked:
		return true
	default:
		return false
	}
}

func managementBookingOperationPending(operation *models.ManagementBookingOperation) bool {
	return operation != nil && (operation.State == models.ManagementBookingOperationPlanned || operation.State == models.ManagementBookingOperationAccepted || operation.State == models.ManagementBookingOperationRunning)
}

func ManagementBookingSucceeded(operation *models.ManagementBookingOperation) bool {
	return operation != nil && operation.State == models.ManagementBookingOperationSucceeded &&
		(operation.Outcome == bookingapi.OutcomeSuccess || operation.Outcome == bookingapi.OutcomeSuccessWithWarning)
}

func ManagementBookingCredentialRejected(operation *models.ManagementBookingOperation) bool {
	return operation != nil && operation.State == models.ManagementBookingOperationRejectedNoEffect &&
		operation.Outcome == bookingapi.OutcomeNoEffect && operation.Code == bookingapi.ErrorCredentialRejected
}

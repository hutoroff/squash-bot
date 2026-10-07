package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
)

type unfinishedBookingOperationRepository interface {
	ListRecoverableOperations(context.Context, int) ([]*models.ManagementBookingOperation, error)
	MarkRecoveryAttempt(context.Context, bookingapi.OperationID) error
}

type ReliableBookingRecovery struct {
	repository    unfinishedBookingOperationRepository
	intents       *BookingIntentService
	executor      *ReliableBookingExecutor
	scope         bookingapi.Scope
	credentials   *VenueCredentialService
	courtBookings CourtBookingRepository
	logger        *slog.Logger
	limit         int
	runMu         sync.Mutex
}

func NewReliableBookingRecovery(repository unfinishedBookingOperationRepository, intents *BookingIntentService, executor *ReliableBookingExecutor, credentials *VenueCredentialService, courtBookings CourtBookingRepository, scope bookingapi.Scope, logger *slog.Logger, limit int) (*ReliableBookingRecovery, error) {
	if repository == nil || intents == nil || executor == nil || credentials == nil || courtBookings == nil || scope.ServiceKey == "" || logger == nil || limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("valid reliable booking recovery dependencies are required")
	}
	return &ReliableBookingRecovery{repository: repository, intents: intents, executor: executor, credentials: credentials, courtBookings: courtBookings, scope: scope, logger: logger, limit: limit}, nil
}

// RunOnce observes accepted/running/unknown operations and re-submits only
// durable planned operations with their original key, payload, credential, and
// provider scope. It never allocates replacement operations.
func (r *ReliableBookingRecovery) RunOnce(ctx context.Context) error {
	if !r.runMu.TryLock() {
		return nil
	}
	defer r.runMu.Unlock()
	operations, err := r.repository.ListRecoverableOperations(ctx, r.limit)
	if err != nil {
		return err
	}
	var recoveryErrors []error
	for _, operation := range operations {
		if operation == nil {
			continue
		}
		var recoverErr error
		var recovered *models.ManagementBookingOperation
		intent, intentErr := r.intents.GetIntent(ctx, operation.IntentID)
		if intentErr != nil {
			recoverErr = intentErr
		} else if intent.Scope != r.scope {
			recoverErr = fmt.Errorf("operation origin %q does not match configured booking binding", intent.Scope.ServiceKey)
		}
		if recoverErr == nil && operation.State == models.ManagementBookingOperationPlanned && operation.ParentOperationID != "" {
			// A risk child can only be dispatched during its explicit operator
			// request and scheduling window. Recovery observes the same key but
			// never initiates a late provider mutation.
			recovered, recoverErr = r.executor.Observe(ctx, operation.OperationID)
		} else if recoverErr == nil && operation.State == models.ManagementBookingOperationPlanned {
			if operation.CredentialID == nil {
				recoverErr = fmt.Errorf("planned operation has no credential reference")
			} else {
				credential, credentialErr := r.credentials.GetDecryptedByID(ctx, *operation.CredentialID)
				if credentialErr != nil {
					recoverErr = credentialErr
				} else if operation.Kind == bookingapi.OperationCreate {
					recovered, recoverErr = r.executor.ExecuteCreate(ctx, operation, bookingapi.Credentials{Email: credential.Login, Password: credential.Password})
				} else {
					recovered, recoverErr = r.executor.ExecuteCancel(ctx, operation, bookingapi.Credentials{Email: credential.Login, Password: credential.Password})
				}
			}
		} else if recoverErr == nil && ManagementBookingSucceeded(operation) {
			recovered = operation
		} else if recoverErr == nil {
			recovered, recoverErr = r.executor.Observe(ctx, operation.OperationID)
		}
		if recoverErr == nil && recovered != nil && recovered.Kind == bookingapi.OperationCreate && ManagementBookingSucceeded(recovered) && recovered.MatchID != "" {
			recoverErr = r.courtBookings.Save(ctx, &models.CourtBooking{
				VenueID: intent.VenueID, GameDate: intent.GameDate, GameTime: intent.GameTime,
				CourtUUID: recovered.CourtUUID, CourtLabel: recovered.CourtLabel,
				MatchID: recovered.MatchID, BookingUUID: recovered.BookingUUID, CredentialID: recovered.CredentialID,
				CreateOperationID: recovered.OperationID, OriginScope: intent.Scope,
			})
		}
		if markErr := r.repository.MarkRecoveryAttempt(ctx, operation.OperationID); markErr != nil {
			recoverErr = errors.Join(recoverErr, markErr)
		}
		if recoverErr != nil {
			r.logger.Warn("reliable booking recovery deferred", "operation_id", operation.OperationID, "kind", operation.Kind, "err", recoverErr)
			recoveryErrors = append(recoveryErrors, fmt.Errorf("operation %s: %w", operation.OperationID, recoverErr))
		}
	}
	return errors.Join(recoveryErrors...)
}

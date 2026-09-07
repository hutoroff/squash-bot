package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Finalize records one terminal operation outcome and updates account/resource
// claims in the same transaction. It is conditional on executor ownership;
// stale executors cannot overwrite recovery or a later decision.
func (r *OperationRepo) Finalize(
	ctx context.Context,
	operationID, executorID bookingapi.OperationID,
	state bookingapi.OperationState,
	outcome bookingapi.OutcomeCategory,
	phase bookingapi.OperationPhase,
) (*Operation, error) {
	if !validTerminal(state, outcome) {
		return nil, fmt.Errorf("invalid terminal booking state/outcome %q/%q", state, outcome)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin booking finalization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	operation, err := getOperationForUpdate(ctx, tx, operationID)
	if err != nil {
		return nil, fmt.Errorf("lock booking operation for finalization: %w", err)
	}
	if operation.ExecutorID != string(executorID) ||
		(operation.State != bookingapi.StateAccepted && operation.State != bookingapi.StateRunning) {
		return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: operation.ExecutorID}
	}
	switch state {
	case bookingapi.StateSucceeded:
		confirmed, err := hasConfirmedSuccessEvidence(ctx, tx, operation)
		if err != nil {
			return nil, err
		}
		if !confirmed {
			return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(operationID)}
		}
	case bookingapi.StateNotDispatched:
		dispatched, err := hasMutationIntent(ctx, tx, operation)
		if err != nil {
			return nil, err
		}
		if dispatched {
			return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(operationID)}
		}
	case bookingapi.StateRejectedNoEffect:
		noEffect, err := hasNoEffectEvidence(ctx, tx, operation)
		if err != nil {
			return nil, err
		}
		if !noEffect {
			return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(operationID)}
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE booking_operations
		SET state = $2, outcome = $3, phase = $4, executor_id = NULL,
		    revision = revision + 1, completed_at = NOW(), updated_at = NOW()
		WHERE operation_id = $1`, operationID, state, outcome, phase); err != nil {
		return nil, fmt.Errorf("finalize booking operation: %w", err)
	}
	accountResult, err := tx.Exec(ctx, `
		UPDATE booking_accounts
		SET active_operation_id = NULL,
		    state = CASE WHEN $3 = 'outcome_unknown' THEN 'quarantined' ELSE state END,
		    quarantine_operation_id = CASE
		        WHEN $3 = 'outcome_unknown' THEN COALESCE(quarantine_operation_id, $2)
		        ELSE quarantine_operation_id
		    END,
		    updated_at = NOW()
		WHERE id = $1 AND active_operation_id = $2`, operation.AccountID, operationID, state)
	if err != nil {
		return nil, fmt.Errorf("release booking account executor: %w", err)
	}
	if accountResult.RowsAffected() != 1 {
		return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(operationID)}
	}
	if state == bookingapi.StateSucceeded && operation.Kind == bookingapi.OperationCancel {
		var createOperationID string
		err := tx.QueryRow(ctx, `
			UPDATE booking_provider_bookings
			SET booking_state = 'cancelled',
			    cancellation_operation_id = COALESCE(cancellation_operation_id, $1),
			    updated_at = NOW()
			WHERE provider = $2 AND facility_uuid = $3 AND match_id = $4
			RETURNING create_operation_id::text`,
			operationID, operation.Scope.Provider, operation.Scope.FacilityUUID, operation.TargetMatchID,
		).Scan(&createOperationID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("record confirmed provider cancellation: %w", err)
		}
		if createOperationID != "" {
			if _, err := tx.Exec(ctx, `
				UPDATE booking_resource_claims
				SET state = 'released', released_at = NOW()
				WHERE operation_id = $1 AND claim_type = 'occupancy' AND state = 'active'`, createOperationID); err != nil {
				return nil, fmt.Errorf("release confirmed booking occupancy: %w", err)
			}
		}
	}
	if state == bookingapi.StateNotDispatched || state == bookingapi.StateRejectedNoEffect ||
		(state == bookingapi.StateSucceeded && operation.Kind == bookingapi.OperationCancel) {
		if _, err := tx.Exec(ctx, `
			UPDATE booking_resource_claims
			SET state = 'released', released_at = NOW()
			WHERE operation_id = $1 AND state = 'active'`, operationID); err != nil {
			return nil, fmt.Errorf("release booking operation claims: %w", err)
		}
	}

	finalized, err := getOperationTx(ctx, tx, operationID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit booking finalization: %w", err)
	}
	return finalized, nil
}

func (r *OperationRepo) ListUnfinished(ctx context.Context) ([]*Operation, error) {
	rows, err := r.pool.Query(ctx, operationSelect+`
		WHERE o.state IN ('accepted', 'running')
		ORDER BY o.created_at, o.operation_id`)
	if err != nil {
		return nil, fmt.Errorf("list unfinished booking operations: %w", err)
	}
	defer rows.Close()
	var operations []*Operation
	for rows.Next() {
		operation, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

// MarkResultExpired retains the operation key/fingerprint and terminal summary
// while marking detailed result retention as expired. Detailed evidence cleanup
// is a later policy step and must never delete active or unresolved records.
func (r *OperationRepo) MarkResultExpired(ctx context.Context, operationID bookingapi.OperationID, expectedRevision int64) (*Operation, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin result expiration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	operation, err := getOperationForUpdate(ctx, tx, operationID)
	if err != nil {
		return nil, err
	}
	if operation.ResultExpiredAt != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit booking result expiration replay: %w", err)
		}
		return operation, nil
	}
	if operation.Revision != expectedRevision || operation.State == bookingapi.StateAccepted || operation.State == bookingapi.StateRunning || operation.State == bookingapi.StateOutcomeUnknown {
		return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(operationID)}
	}
	result, err := tx.Exec(ctx, `
		UPDATE booking_operations
		SET result_expired_at = NOW(), revision = revision + 1, updated_at = NOW()
		WHERE operation_id = $1 AND revision = $2`, operationID, expectedRevision)
	if err != nil {
		return nil, fmt.Errorf("mark booking result expired: %w", err)
	}
	if result.RowsAffected() != 1 {
		return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(operationID)}
	}
	expired, err := getOperationTx(ctx, tx, operationID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit booking result expiration: %w", err)
	}
	return expired, nil
}

func hasMutationIntent(ctx context.Context, tx pgx.Tx, operation *Operation) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM booking_operation_steps
			WHERE operation_id = $1 AND status = 'intent'
			  AND (($2 = 'create' AND phase IN ('reserve', 'payment'))
			       OR ($2 = 'cancel' AND phase = 'cancellation'))
		)`, operation.OperationID, operation.Kind).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check booking mutation intent: %w", err)
	}
	return exists, nil
}

func hasNoEffectEvidence(ctx context.Context, tx pgx.Tx, operation *Operation) (bool, error) {
	var established bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM booking_operation_steps
			WHERE operation_id = $1 AND status = 'evidence' AND effect = 'no_effect'
		)
		AND NOT EXISTS (
			SELECT 1 FROM booking_provider_bookings WHERE create_operation_id = $1
		)
		AND NOT EXISTS (
			SELECT 1
			FROM booking_operation_steps intent
			WHERE intent.operation_id = $1 AND intent.status = 'intent'
			  AND (($2 = 'create' AND intent.phase IN ('reserve', 'payment'))
			       OR ($2 = 'cancel' AND intent.phase = 'cancellation'))
			  AND NOT EXISTS (
				SELECT 1 FROM booking_operation_steps evidence
				WHERE evidence.operation_id = intent.operation_id
				  AND evidence.phase = intent.phase AND evidence.attempt = intent.attempt
				  AND evidence.status = 'evidence' AND evidence.effect = 'no_effect'
			  )
		)
		AND NOT EXISTS (
			SELECT 1 FROM booking_operation_steps
			WHERE operation_id = $1 AND status = 'evidence' AND effect <> 'no_effect'
			  AND (($2 = 'create' AND phase IN ('reserve', 'payment'))
			       OR ($2 = 'cancel' AND phase = 'cancellation'))
		)`, operation.OperationID, operation.Kind).Scan(&established)
	if err != nil {
		return false, fmt.Errorf("check booking no-effect evidence: %w", err)
	}
	return established, nil
}

func hasConfirmedSuccessEvidence(ctx context.Context, tx pgx.Tx, operation *Operation) (bool, error) {
	var confirmed bool
	var err error
	if operation.Kind == bookingapi.OperationCreate {
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM booking_provider_bookings
				WHERE create_operation_id = $1
				  AND booking_uuid IS NOT NULL AND booking_id IS NOT NULL AND payment_id IS NOT NULL
				  AND booking_state = 'confirmed' AND payment_state = 'confirmed'
			) AND EXISTS (
				SELECT 1 FROM booking_operation_steps
				WHERE operation_id = $1 AND phase = 'reserve' AND status = 'evidence' AND effect = 'confirmed'
			) AND EXISTS (
				SELECT 1 FROM booking_operation_steps
				WHERE operation_id = $1 AND phase = 'payment' AND status = 'evidence' AND effect = 'confirmed'
			)`, operation.OperationID).Scan(&confirmed)
	} else {
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM booking_operation_steps
				WHERE operation_id = $1 AND phase = 'cancellation' AND status = 'evidence' AND effect = 'confirmed'
			)`, operation.OperationID).Scan(&confirmed)
	}
	if err != nil {
		return false, fmt.Errorf("check confirmed booking evidence: %w", err)
	}
	return confirmed, nil
}

func validTerminal(state bookingapi.OperationState, outcome bookingapi.OutcomeCategory) bool {
	switch state {
	case bookingapi.StateNotDispatched, bookingapi.StateRejectedNoEffect:
		return outcome == bookingapi.OutcomeNoEffect
	case bookingapi.StateSucceeded:
		return outcome == bookingapi.OutcomeSuccess || outcome == bookingapi.OutcomeSuccessWithWarning
	case bookingapi.StateOutcomeUnknown:
		return outcome == bookingapi.OutcomeUnknown
	default:
		return false
	}
}

func (r *OperationRepo) SetProviderUserAlias(ctx context.Context, provider, identity, alias string) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE booking_accounts
		SET provider_user_alias = $3, updated_at = NOW()
		WHERE provider = $1 AND identity = $2
		  AND (provider_user_alias IS NULL OR provider_user_alias = $3)`, provider, identity, alias)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return &ConflictError{Code: bookingapi.ErrorAccountBlocked}
		}
		return fmt.Errorf("set booking provider user alias: %w", err)
	}
	if result.RowsAffected() != 1 {
		return &ConflictError{Code: bookingapi.ErrorAccountBlocked}
	}
	return nil
}

func (r *OperationRepo) ListSteps(ctx context.Context, operationID bookingapi.OperationID) ([]*Step, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, operation_id::text, phase, attempt, status,
		       COALESCE(effect, ''), COALESCE(code, ''), evidence, executor_id::text, created_at
		FROM booking_operation_steps
		WHERE operation_id = $1
		ORDER BY id`, operationID)
	if err != nil {
		return nil, fmt.Errorf("list booking operation steps: %w", err)
	}
	defer rows.Close()
	var steps []*Step
	for rows.Next() {
		var step Step
		var stepOperationID, phase string
		if err := rows.Scan(&step.ID, &stepOperationID, &phase, &step.Attempt, &step.Status,
			&step.Effect, &step.Code, &step.Evidence, &step.ExecutorID, &step.CreatedAt); err != nil {
			return nil, err
		}
		step.OperationID = bookingapi.OperationID(stepOperationID)
		step.Phase = bookingapi.OperationPhase(phase)
		steps = append(steps, &step)
	}
	return steps, rows.Err()
}

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxAdmissionAttempts = 3

type OperationRepo struct {
	pool *pgxpool.Pool
}

func NewOperationRepo(pool *pgxpool.Pool) *OperationRepo { return &OperationRepo{pool: pool} }

func (r *OperationRepo) Get(ctx context.Context, operationID bookingapi.OperationID) (*Operation, error) {
	return scanOperation(r.pool.QueryRow(ctx, operationSelect+` WHERE o.operation_id = $1`, operationID))
}

func (r *OperationRepo) AdmitCreate(
	ctx context.Context,
	request bookingapi.ValidatedCreate,
	deadline time.Time,
	executorID bookingapi.OperationID,
) (*Admission, error) {
	return r.withAdmissionRetry(ctx, func(tx pgx.Tx) (*Admission, error) {
		return admitCreate(ctx, tx, request, deadline, executorID)
	})
}

func (r *OperationRepo) AdmitCancel(
	ctx context.Context,
	request bookingapi.ValidatedCancel,
	deadline time.Time,
	executorID bookingapi.OperationID,
) (*Admission, error) {
	return r.withAdmissionRetry(ctx, func(tx pgx.Tx) (*Admission, error) {
		return admitCancel(ctx, tx, request, deadline, executorID)
	})
}

func (r *OperationRepo) withAdmissionRetry(ctx context.Context, fn func(pgx.Tx) (*Admission, error)) (*Admission, error) {
	var lastErr error
	for attempt := 0; attempt < maxAdmissionAttempts; attempt++ {
		tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			return nil, fmt.Errorf("begin booking admission: %w", err)
		}
		admission, err := fn(tx)
		if err == nil {
			err = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if err == nil {
			return admission, nil
		}
		lastErr = err
		if !retryableTransactionError(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("booking admission retries exhausted: %w", lastErr)
}

func admitCreate(ctx context.Context, tx pgx.Tx, request bookingapi.ValidatedCreate, deadline time.Time, executorID bookingapi.OperationID) (*Admission, error) {
	if err := lockKey(ctx, tx, "ledger-key:"+string(request.OperationID)); err != nil {
		return nil, err
	}
	fingerprint := request.Fingerprint()
	if existing, err := getOperationTx(ctx, tx, request.OperationID); err == nil {
		if existing.RequestFingerprint != fingerprint {
			return nil, &ConflictError{Code: bookingapi.ErrorIdempotencyKeyConflict, ConflictingOperationID: string(request.OperationID)}
		}
		return &Admission{Disposition: AdmissionReplay, Operation: existing}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err := rejectImportKeyCollision(ctx, tx, request.OperationID); err != nil {
		return nil, err
	}

	accountID, accountState, activeOperation, quarantineOperation, err := lockAccount(ctx, tx, request.Scope.Provider, request.AccountIdentity)
	if err != nil {
		return nil, err
	}
	if activeOperation != "" {
		return nil, &ConflictError{Code: bookingapi.ErrorAccountBlocked, ConflictingOperationID: activeOperation}
	}

	rootID := request.OperationID
	parentID, riskReason := "", ""
	if request.Risk != nil {
		parentID = string(request.Risk.ReplacesOperationID)
		parent, err := getOperationForUpdate(ctx, tx, request.Risk.ReplacesOperationID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: parentID}
			}
			return nil, err
		}
		if parent.Kind != bookingapi.OperationCreate || parent.State != bookingapi.StateOutcomeUnknown || parent.ExecutorID != "" ||
			parent.Scope != request.Scope || parent.CourtUUID != request.CourtUUID || parent.Start == nil || parent.End == nil ||
			!parent.Start.Equal(request.Start) || !parent.End.Equal(request.End) {
			return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: parentID}
		}
		rootID = bookingapi.OperationID(parent.RootOperationID)
		riskReason = request.Risk.Reason
		if accountState == "quarantined" && quarantineOperation != parentID {
			return nil, &ConflictError{Code: bookingapi.ErrorAccountBlocked, ConflictingOperationID: quarantineOperation}
		}
	} else if accountState == "quarantined" {
		return nil, &ConflictError{Code: bookingapi.ErrorAccountBlocked, ConflictingOperationID: quarantineOperation}
	}

	resourceKey := "occupancy:" + request.Scope.Provider + ":" + request.Scope.FacilityUUID + ":" + request.CourtUUID
	if err := lockKey(ctx, tx, resourceKey); err != nil {
		return nil, err
	}
	conflictID, conflictKind, err := findOccupancyConflict(ctx, tx, request, parentID)
	if err != nil {
		return nil, err
	}
	if conflictID != "" {
		return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: conflictID, ConflictingResourceKind: conflictKind}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO booking_operations (
			operation_id, request_fingerprint, kind, service_key, provider,
			facility_id, facility_uuid, facility_slug, sport, account_id,
			court_uuid, start_at, end_at, start_provider, end_provider,
			state, phase, outcome, executor_id, deadline_at,
			parent_operation_id, root_operation_id, risk_reason
		) VALUES (
			$1, $2, 'create', $3, $4, $5, $6, $7, $8, $9,
			$10, $11, $12, $13, $14,
			'accepted', 'admission', 'pending', $15, $16,
			NULLIF($17, '')::uuid, $18, NULLIF($19, '')
		)`,
		request.OperationID, fingerprint, request.Scope.ServiceKey, request.Scope.Provider,
		request.Scope.FacilityID, request.Scope.FacilityUUID, request.Scope.FacilitySlug, request.Scope.Sport,
		accountID, request.CourtUUID, request.Start, request.End, request.StartProvider, request.EndProvider,
		executorID, deadline, parentID, rootID, riskReason,
	)
	if err != nil {
		return nil, fmt.Errorf("insert create operation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO booking_resource_claims (
			operation_id, claim_type, provider, facility_uuid, court_uuid, start_at, end_at
		) VALUES ($1, 'occupancy', $2, $3, $4, $5, $6)`,
		request.OperationID, request.Scope.Provider, request.Scope.FacilityUUID, request.CourtUUID, request.Start, request.End,
	); err != nil {
		return nil, fmt.Errorf("insert occupancy claim: %w", err)
	}
	accountResult, err := tx.Exec(ctx, `
		UPDATE booking_accounts
		SET active_operation_id = $2, updated_at = NOW()
		WHERE id = $1 AND active_operation_id IS NULL`, accountID, request.OperationID)
	if err != nil {
		return nil, fmt.Errorf("activate booking account: %w", err)
	}
	if accountResult.RowsAffected() != 1 {
		return nil, &ConflictError{Code: bookingapi.ErrorAccountBlocked}
	}

	operation, err := getOperationTx(ctx, tx, request.OperationID)
	if err != nil {
		return nil, err
	}
	return &Admission{Disposition: AdmissionAccepted, Operation: operation}, nil
}

func admitCancel(ctx context.Context, tx pgx.Tx, request bookingapi.ValidatedCancel, deadline time.Time, executorID bookingapi.OperationID) (*Admission, error) {
	if err := lockKey(ctx, tx, "ledger-key:"+string(request.OperationID)); err != nil {
		return nil, err
	}
	fingerprint := request.Fingerprint()
	if existing, err := getOperationTx(ctx, tx, request.OperationID); err == nil {
		if existing.RequestFingerprint != fingerprint {
			return nil, &ConflictError{Code: bookingapi.ErrorIdempotencyKeyConflict, ConflictingOperationID: string(request.OperationID)}
		}
		return &Admission{Disposition: AdmissionReplay, Operation: existing}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err := rejectImportKeyCollision(ctx, tx, request.OperationID); err != nil {
		return nil, err
	}

	accountID, accountState, activeOperation, quarantineOperation, err := lockAccount(ctx, tx, request.Scope.Provider, request.AccountIdentity)
	if err != nil {
		return nil, err
	}
	resourceKey := "cancellation:" + request.Scope.Provider + ":" + request.Scope.FacilityUUID + ":" + request.MatchID
	if err := lockKey(ctx, tx, resourceKey); err != nil {
		return nil, err
	}
	var targetState, targetAccountIdentity, targetOperationID, targetKind string
	err = tx.QueryRow(ctx, `
		SELECT b.booking_state, a.identity, COALESCE(b.create_operation_id::text, b.import_id::text),
		       CASE WHEN b.import_id IS NULL THEN 'operation' ELSE 'import' END
		FROM booking_provider_bookings b
		LEFT JOIN booking_operations o ON o.operation_id = b.create_operation_id
		LEFT JOIN booking_imports i ON i.import_id = b.import_id
		JOIN booking_accounts a ON a.id = COALESCE(o.account_id, i.account_id)
		WHERE b.provider = $1 AND b.facility_uuid = $2 AND b.match_id = $3
		FOR UPDATE OF b`, request.Scope.Provider, request.Scope.FacilityUUID, request.MatchID,
	).Scan(&targetState, &targetAccountIdentity, &targetOperationID, &targetKind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict}
	}
	if err != nil {
		return nil, fmt.Errorf("lock cancellation target: %w", err)
	}
	if targetAccountIdentity != request.AccountIdentity {
		return nil, &ConflictError{Code: bookingapi.ErrorAccountBlocked, ConflictingOperationID: targetOperationID, ConflictingResourceKind: targetKind}
	}
	if targetState == "cancelled" {
		return admitResolvedCancellation(ctx, tx, request, fingerprint, accountID, deadline)
	}
	if activeOperation != "" || accountState == "quarantined" {
		conflictID := activeOperation
		if conflictID == "" {
			conflictID = quarantineOperation
		}
		return nil, &ConflictError{Code: bookingapi.ErrorAccountBlocked, ConflictingOperationID: conflictID}
	}
	var conflictID string
	err = tx.QueryRow(ctx, `
		SELECT operation_id::text
		FROM booking_resource_claims
		WHERE claim_type = 'cancellation' AND state = 'active'
		  AND provider = $1 AND facility_uuid = $2 AND match_id = $3
		LIMIT 1`, request.Scope.Provider, request.Scope.FacilityUUID, request.MatchID).Scan(&conflictID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("find cancellation conflict: %w", err)
	}
	if conflictID != "" {
		return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: conflictID}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO booking_operations (
			operation_id, request_fingerprint, kind, service_key, provider,
			facility_id, facility_uuid, facility_slug, sport, account_id,
			target_match_id, state, phase, outcome, executor_id, deadline_at, root_operation_id
		) VALUES (
			$1, $2, 'cancel', $3, $4, $5, $6, $7, $8, $9,
			$10, 'accepted', 'admission', 'pending', $11, $12, $1
		)`,
		request.OperationID, fingerprint, request.Scope.ServiceKey, request.Scope.Provider,
		request.Scope.FacilityID, request.Scope.FacilityUUID, request.Scope.FacilitySlug, request.Scope.Sport,
		accountID, request.MatchID, executorID, deadline,
	)
	if err != nil {
		return nil, fmt.Errorf("insert cancel operation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO booking_resource_claims (
			operation_id, claim_type, provider, facility_uuid, match_id
		) VALUES ($1, 'cancellation', $2, $3, $4)`,
		request.OperationID, request.Scope.Provider, request.Scope.FacilityUUID, request.MatchID,
	); err != nil {
		return nil, fmt.Errorf("insert cancellation claim: %w", err)
	}
	accountResult, err := tx.Exec(ctx, `
		UPDATE booking_accounts SET active_operation_id = $2, updated_at = NOW()
		WHERE id = $1 AND active_operation_id IS NULL`, accountID, request.OperationID)
	if err != nil {
		return nil, fmt.Errorf("activate booking account: %w", err)
	}
	if accountResult.RowsAffected() != 1 {
		return nil, &ConflictError{Code: bookingapi.ErrorAccountBlocked}
	}

	operation, err := getOperationTx(ctx, tx, request.OperationID)
	if err != nil {
		return nil, err
	}
	return &Admission{Disposition: AdmissionAccepted, Operation: operation}, nil
}

func admitResolvedCancellation(ctx context.Context, tx pgx.Tx, request bookingapi.ValidatedCancel, fingerprint string, accountID int64, deadline time.Time) (*Admission, error) {
	_, err := tx.Exec(ctx, `
		INSERT INTO booking_operations (
			operation_id, request_fingerprint, kind, service_key, provider,
			facility_id, facility_uuid, facility_slug, sport, account_id,
			target_match_id, state, phase, outcome, deadline_at, root_operation_id,
			completed_at
		) VALUES (
			$1, $2, 'cancel', $3, $4, $5, $6, $7, $8, $9,
			$10, 'succeeded', 'finalization', 'success', $11, $1, NOW()
		)`, request.OperationID, fingerprint, request.Scope.ServiceKey, request.Scope.Provider,
		request.Scope.FacilityID, request.Scope.FacilityUUID, request.Scope.FacilitySlug,
		request.Scope.Sport, accountID, request.MatchID, deadline)
	if err != nil {
		return nil, fmt.Errorf("insert resolved cancellation operation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO booking_resource_claims (
			operation_id, claim_type, provider, facility_uuid, match_id, state, released_at
		) VALUES ($1, 'cancellation', $2, $3, $4, 'released', NOW())`,
		request.OperationID, request.Scope.Provider, request.Scope.FacilityUUID, request.MatchID,
	); err != nil {
		return nil, fmt.Errorf("insert resolved cancellation claim: %w", err)
	}
	operation, err := getOperationTx(ctx, tx, request.OperationID)
	if err != nil {
		return nil, err
	}
	return &Admission{Disposition: AdmissionResolved, Operation: operation}, nil
}

func rejectImportKeyCollision(ctx context.Context, tx pgx.Tx, operationID bookingapi.OperationID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM booking_imports WHERE import_id = $1)`, operationID).Scan(&exists); err != nil {
		return fmt.Errorf("check booking ledger key collision: %w", err)
	}
	if exists {
		return &ConflictError{Code: bookingapi.ErrorIdempotencyKeyConflict, ConflictingOperationID: string(operationID), ConflictingResourceKind: "import"}
	}
	return nil
}

func lockAccount(ctx context.Context, tx pgx.Tx, provider, identity string) (id int64, state, activeOperation, quarantineOperation string, err error) {
	if _, err = tx.Exec(ctx, `
		INSERT INTO booking_accounts (provider, identity)
		VALUES ($1, $2)
		ON CONFLICT (provider, identity) DO NOTHING`, provider, identity); err != nil {
		return 0, "", "", "", fmt.Errorf("ensure booking account: %w", err)
	}
	err = tx.QueryRow(ctx, `
		SELECT id, state, COALESCE(active_operation_id::text, ''), COALESCE(quarantine_operation_id::text, '')
		FROM booking_accounts
		WHERE provider = $1 AND identity = $2
		FOR UPDATE`, provider, identity).Scan(&id, &state, &activeOperation, &quarantineOperation)
	if err != nil {
		return 0, "", "", "", fmt.Errorf("lock booking account: %w", err)
	}
	return id, state, activeOperation, quarantineOperation, nil
}

func findOccupancyConflict(ctx context.Context, tx pgx.Tx, request bookingapi.ValidatedCreate, allowedParent string) (string, string, error) {
	var ownerID, ownerKind string
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(operation_id::text, import_id::text),
		       CASE WHEN import_id IS NULL THEN 'operation' ELSE 'import' END
		FROM booking_resource_claims
		WHERE claim_type = 'occupancy' AND state = 'active'
		  AND provider = $1 AND facility_uuid = $2 AND court_uuid = $3
		  AND start_at < $5 AND end_at > $4
		  AND (operation_id IS NULL OR operation_id <> COALESCE(NULLIF($6, '')::uuid, '00000000-0000-0000-0000-000000000000'::uuid))
		ORDER BY id
		LIMIT 1`,
		request.Scope.Provider, request.Scope.FacilityUUID, request.CourtUUID, request.Start, request.End, allowedParent,
	).Scan(&ownerID, &ownerKind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("find occupancy conflict: %w", err)
	}
	return ownerID, ownerKind, nil
}

func lockKey(ctx context.Context, tx pgx.Tx, key string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
		return fmt.Errorf("lock booking admission key: %w", err)
	}
	return nil
}

func retryableTransactionError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	// A unique violation can be the database-enforced winner of concurrent
	// operation/root admission. A fresh snapshot turns it into replay or a
	// typed business conflict; retries remain entirely before provider work.
	return pgErr.Code == "40001" || pgErr.Code == "40P01" || pgErr.Code == "23505"
}

const operationSelect = `
	SELECT o.operation_id::text, o.request_fingerprint, o.kind,
	       o.service_key, o.provider, o.facility_id, o.facility_uuid, o.facility_slug, o.sport,
	       o.account_id, a.identity,
	       COALESCE(o.court_uuid, ''), o.start_at, o.end_at,
	       COALESCE(o.start_provider, ''), COALESCE(o.end_provider, ''), COALESCE(o.target_match_id, ''),
	       o.state, o.phase, o.outcome, COALESCE(o.executor_id::text, ''), o.revision, o.deadline_at,
	       COALESCE(o.parent_operation_id::text, ''), o.root_operation_id::text, COALESCE(o.risk_reason, ''),
	       o.result_expired_at, o.completed_at, o.created_at, o.updated_at
	FROM booking_operations o
	JOIN booking_accounts a ON a.id = o.account_id`

func getOperationTx(ctx context.Context, tx pgx.Tx, operationID bookingapi.OperationID) (*Operation, error) {
	return scanOperation(tx.QueryRow(ctx, operationSelect+` WHERE o.operation_id = $1`, operationID))
}

func getOperationForUpdate(ctx context.Context, tx pgx.Tx, operationID bookingapi.OperationID) (*Operation, error) {
	return scanOperation(tx.QueryRow(ctx, operationSelect+` WHERE o.operation_id = $1 FOR UPDATE OF o`, operationID))
}

func scanOperation(row pgx.Row) (*Operation, error) {
	var operation Operation
	var operationID, kind, state, phase, outcome string
	if err := row.Scan(
		&operationID, &operation.RequestFingerprint, &kind,
		&operation.Scope.ServiceKey, &operation.Scope.Provider, &operation.Scope.FacilityID,
		&operation.Scope.FacilityUUID, &operation.Scope.FacilitySlug, &operation.Scope.Sport,
		&operation.AccountID, &operation.AccountIdentity,
		&operation.CourtUUID, &operation.Start, &operation.End,
		&operation.StartProvider, &operation.EndProvider, &operation.TargetMatchID,
		&state, &phase, &outcome, &operation.ExecutorID, &operation.Revision, &operation.DeadlineAt,
		&operation.ParentOperationID, &operation.RootOperationID, &operation.RiskReason,
		&operation.ResultExpiredAt, &operation.CompletedAt, &operation.CreatedAt, &operation.UpdatedAt,
	); err != nil {
		return nil, err
	}
	operation.OperationID = bookingapi.OperationID(operationID)
	operation.Kind = bookingapi.OperationKind(kind)
	operation.State = bookingapi.OperationState(state)
	operation.Phase = bookingapi.OperationPhase(phase)
	operation.Outcome = bookingapi.OutcomeCategory(outcome)
	return &operation, nil
}

// AppendStepIntent records the durable boundary before a provider mutation.
// Repeating the exact step is idempotent and does not advance the revision.
func (r *OperationRepo) AppendStepIntent(ctx context.Context, operationID bookingapi.OperationID, phase bookingapi.OperationPhase, attempt int, executorID bookingapi.OperationID) (*Step, error) {
	return r.appendStep(ctx, operationID, phase, attempt, "intent", "", "", nil, executorID)
}

// AppendStepEvidence records sanitized provider evidence after a mutation.
func (r *OperationRepo) AppendStepEvidence(ctx context.Context, operationID bookingapi.OperationID, phase bookingapi.OperationPhase, attempt int, effect, code string, evidence any, executorID bookingapi.OperationID) (*Step, error) {
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return nil, fmt.Errorf("marshal booking step evidence: %w", err)
	}
	return r.appendStep(ctx, operationID, phase, attempt, "evidence", effect, code, encoded, executorID)
}

func (r *OperationRepo) appendStep(ctx context.Context, operationID bookingapi.OperationID, phase bookingapi.OperationPhase, attempt int, status, effect, code string, evidence []byte, executorID bookingapi.OperationID) (*Step, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var activeExecutor, activeKind string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(executor_id::text, ''), kind
		FROM booking_operations
		WHERE operation_id = $1 AND state IN ('accepted', 'running')
		FOR UPDATE`, operationID).Scan(&activeExecutor, &activeKind); err != nil {
		return nil, fmt.Errorf("lock operation for step: %w", err)
	}
	if activeExecutor != string(executorID) {
		return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: activeExecutor}
	}
	kind := bookingapi.OperationKind(activeKind)
	if (kind == bookingapi.OperationCreate && phase != bookingapi.PhaseLogin && phase != bookingapi.PhaseReserve && phase != bookingapi.PhasePayment && phase != bookingapi.PhaseMatchAttachment) ||
		(kind == bookingapi.OperationCancel && phase != bookingapi.PhaseLogin && phase != bookingapi.PhaseCancellation) {
		return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(operationID)}
	}
	if status == "intent" && attempt > 1 {
		if attempt != 2 || (phase != bookingapi.PhaseLogin && phase != bookingapi.PhaseReserve && phase != bookingapi.PhaseCancellation) {
			return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(operationID)}
		}
		var priorNoEffect bool
		if phase == bookingapi.PhaseLogin {
			// A fresh login is allowed only after this operation's first mutation
			// attempt was durably classified as no-effect. A previous successful
			// login alone never authorizes another provider mutation.
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM booking_operation_steps
					WHERE operation_id = $1 AND attempt = 1
					  AND phase = CASE WHEN $2 = 'create' THEN 'reserve' ELSE 'cancellation' END
					  AND status = 'evidence' AND effect = 'no_effect'
					  AND code = 'authentication_expired'
				)`, operationID, kind).Scan(&priorNoEffect); err != nil {
				return nil, fmt.Errorf("check prior booking authentication retry: %w", err)
			}
		} else if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM booking_operation_steps
				WHERE operation_id = $1 AND phase = $2 AND attempt = 1
				  AND status = 'evidence' AND effect = 'no_effect'
				  AND code = 'authentication_expired'
			)`, operationID, phase).Scan(&priorNoEffect); err != nil {
			return nil, fmt.Errorf("check prior booking operation attempt: %w", err)
		}
		if !priorNoEffect {
			return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(operationID)}
		}
	}
	if status == "evidence" {
		var intentExists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM booking_operation_steps
				WHERE operation_id = $1 AND phase = $2 AND attempt = $3
				  AND status = 'intent' AND executor_id = $4
			)`, operationID, phase, attempt, executorID).Scan(&intentExists); err != nil {
			return nil, fmt.Errorf("check booking operation intent: %w", err)
		}
		if !intentExists {
			return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(operationID)}
		}
	}
	if len(evidence) == 0 {
		evidence = []byte(`{}`)
	}
	var step Step
	var stepOperationID, stepPhase string
	inserted := true
	err = tx.QueryRow(ctx, `
		INSERT INTO booking_operation_steps (
			operation_id, phase, attempt, status, effect, code, evidence, executor_id
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7, $8)
		ON CONFLICT (operation_id, phase, attempt, status) DO NOTHING
		RETURNING id, operation_id::text, phase, attempt, status,
		          COALESCE(effect, ''), COALESCE(code, ''), evidence, executor_id::text, created_at`,
		operationID, phase, attempt, status, effect, code, evidence, executorID,
	).Scan(&step.ID, &stepOperationID, &stepPhase, &step.Attempt, &step.Status,
		&step.Effect, &step.Code, &step.Evidence, &step.ExecutorID, &step.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		inserted = false
		err = tx.QueryRow(ctx, `
			SELECT id, operation_id::text, phase, attempt, status,
			       COALESCE(effect, ''), COALESCE(code, ''), evidence, executor_id::text, created_at
			FROM booking_operation_steps
			WHERE operation_id = $1 AND phase = $2 AND attempt = $3 AND status = $4`,
			operationID, phase, attempt, status,
		).Scan(&step.ID, &stepOperationID, &stepPhase, &step.Attempt, &step.Status,
			&step.Effect, &step.Code, &step.Evidence, &step.ExecutorID, &step.CreatedAt)
	}
	if err != nil {
		return nil, fmt.Errorf("record booking operation step: %w", err)
	}
	step.OperationID = bookingapi.OperationID(stepOperationID)
	step.Phase = bookingapi.OperationPhase(stepPhase)
	if !inserted && (step.ExecutorID != string(executorID) || step.Effect != effect || step.Code != code || !equalJSON(step.Evidence, evidence)) {
		return nil, &ConflictError{Code: bookingapi.ErrorIdempotencyKeyConflict, ConflictingOperationID: string(operationID)}
	}
	if inserted {
		result, err := tx.Exec(ctx, `
			UPDATE booking_operations
			SET state = 'running', phase = $2, revision = revision + 1, updated_at = NOW()
			WHERE operation_id = $1 AND executor_id = $3`, operationID, phase, executorID)
		if err != nil {
			return nil, fmt.Errorf("advance booking operation phase: %w", err)
		}
		if result.RowsAffected() != 1 {
			return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(operationID)}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit booking operation step: %w", err)
	}
	return &step, nil
}

func equalJSON(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

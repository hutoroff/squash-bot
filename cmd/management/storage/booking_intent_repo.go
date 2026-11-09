package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BookingIntentConflictError struct {
	Key string
}

func (e *BookingIntentConflictError) Error() string {
	return fmt.Sprintf("management booking intent key %q conflicts with an existing request", e.Key)
}

type BookingIntentRepo struct {
	pool *pgxpool.Pool
}

func NewBookingIntentRepo(pool *pgxpool.Pool) *BookingIntentRepo {
	return &BookingIntentRepo{pool: pool}
}

func (r *BookingIntentRepo) AdmitIntent(ctx context.Context, candidate *models.BookingIntent) (*models.BookingIntent, bool, error) {
	if candidate == nil {
		return nil, false, fmt.Errorf("management booking intent is required")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("begin management booking intent admission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var venueGroupID int64
	if err := tx.QueryRow(ctx, `SELECT group_id FROM venues WHERE id = $1 FOR KEY SHARE`, candidate.VenueID).Scan(&venueGroupID); err != nil {
		return nil, false, fmt.Errorf("lock management booking intent venue: %w", err)
	}
	if venueGroupID != candidate.GroupID {
		return nil, false, fmt.Errorf("management booking intent venue does not belong to group")
	}
	intent, err := scanBookingIntent(tx.QueryRow(ctx, `
		INSERT INTO management_booking_intents (
			request_key, request_fingerprint, action, source,
			service_key, provider, facility_id, facility_uuid, facility_slug, sport,
			group_id, venue_id, game_id, game_date, game_time, requested_count, generation, state
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (request_key) DO NOTHING
		RETURNING id, request_key, request_fingerprint, action, source,
			service_key, provider, facility_id, facility_uuid, facility_slug, sport,
			group_id, venue_id, game_id, game_date, game_time, requested_count, generation, state,
			created_at, updated_at`,
		candidate.RequestKey, candidate.RequestFingerprint, candidate.Action, candidate.Source,
		candidate.Scope.ServiceKey, candidate.Scope.Provider, candidate.Scope.FacilityID,
		candidate.Scope.FacilityUUID, candidate.Scope.FacilitySlug, candidate.Scope.Sport,
		candidate.GroupID, candidate.VenueID, candidate.GameID, candidate.GameDate, candidate.GameTime,
		candidate.RequestedCount, candidate.Generation, candidate.State,
	))
	created := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		intent, err = scanBookingIntent(tx.QueryRow(ctx, `
			SELECT id, request_key, request_fingerprint, action, source,
				service_key, provider, facility_id, facility_uuid, facility_slug, sport,
				group_id, venue_id, game_id, game_date, game_time, requested_count, generation, state,
				created_at, updated_at
			FROM management_booking_intents WHERE request_key = $1`, candidate.RequestKey))
		if err != nil {
			return nil, false, fmt.Errorf("read management booking intent replay: %w", err)
		}
		if intent.RequestFingerprint != candidate.RequestFingerprint {
			return nil, false, &BookingIntentConflictError{Key: candidate.RequestKey}
		}
	} else if err != nil {
		return nil, false, fmt.Errorf("admit management booking intent: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit management booking intent admission: %w", err)
	}
	return intent, created, nil
}

func (r *BookingIntentRepo) GetIntentByRequestKey(ctx context.Context, requestKey string) (*models.BookingIntent, error) {
	return scanBookingIntent(r.pool.QueryRow(ctx, `
		SELECT id, request_key, request_fingerprint, action, source,
			service_key, provider, facility_id, facility_uuid, facility_slug, sport,
			group_id, venue_id, game_id, game_date, game_time, requested_count, generation, state,
			created_at, updated_at
		FROM management_booking_intents WHERE request_key = $1`, requestKey))
}

func (r *BookingIntentRepo) GetIntent(ctx context.Context, intentID int64) (*models.BookingIntent, error) {
	return scanBookingIntent(r.pool.QueryRow(ctx, `
		SELECT id, request_key, request_fingerprint, action, source,
			service_key, provider, facility_id, facility_uuid, facility_slug, sport,
			group_id, venue_id, game_id, game_date, game_time, requested_count, generation, state,
			created_at, updated_at
		FROM management_booking_intents WHERE id = $1`, intentID))
}

func (r *BookingIntentRepo) AdmitOperation(ctx context.Context, candidate *models.ManagementBookingOperation) (*models.ManagementBookingOperation, bool, error) {
	if candidate == nil {
		return nil, false, fmt.Errorf("management booking operation is required")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("begin management booking operation admission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var action bookingapi.OperationKind
	var venueID int64
	var intentState models.BookingIntentState
	if err := tx.QueryRow(ctx, `SELECT action, venue_id, state FROM management_booking_intents WHERE id = $1 FOR UPDATE`, candidate.IntentID).Scan(&action, &venueID, &intentState); err != nil {
		return nil, false, fmt.Errorf("lock management booking intent: %w", err)
	}
	if action != candidate.Kind {
		return nil, false, fmt.Errorf("management booking operation kind does not match its business intent")
	}
	var existingFingerprint string
	existingErr := tx.QueryRow(ctx, `
		SELECT request_fingerprint FROM management_booking_operations
		WHERE intent_id = $1 AND dedup_key = $2`, candidate.IntentID, candidate.DedupKey).Scan(&existingFingerprint)
	if existingErr != nil && !errors.Is(existingErr, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("read existing management booking operation: %w", existingErr)
	}
	operationExists := existingErr == nil
	if operationExists && existingFingerprint != candidate.RequestFingerprint {
		return nil, false, &BookingIntentConflictError{Key: candidate.DedupKey}
	}
	if !operationExists && candidate.ParentOperationID == "" && intentState != models.BookingIntentPlanned && intentState != models.BookingIntentActive {
		return nil, false, fmt.Errorf("management booking intent is not open for ordinary operations")
	}
	if !operationExists && candidate.ParentOperationID != "" && intentState != models.BookingIntentUnknown {
		return nil, false, fmt.Errorf("management booking intent is not eligible for duplicate-risk replacement")
	}
	var credentialVenueID int64
	if err := tx.QueryRow(ctx, `SELECT venue_id FROM venue_credentials WHERE id = $1 FOR KEY SHARE`, candidate.CredentialID).Scan(&credentialVenueID); err != nil {
		return nil, false, fmt.Errorf("lock management booking operation credential: %w", err)
	}
	if credentialVenueID != venueID {
		return nil, false, fmt.Errorf("management booking operation credential does not belong to intent venue")
	}
	if candidate.ParentOperationID != "" {
		var parentIntentID int64
		var parentKind bookingapi.OperationKind
		var parentState models.ManagementBookingOperationState
		var parentParentID *string
		var parentRootID *string
		if err := tx.QueryRow(ctx, `
			SELECT intent_id, kind, state, parent_operation_id::text, root_operation_id::text
			FROM management_booking_operations WHERE operation_id = $1 FOR UPDATE`, candidate.ParentOperationID,
		).Scan(&parentIntentID, &parentKind, &parentState, &parentParentID, &parentRootID); err != nil {
			return nil, false, fmt.Errorf("lock management booking parent operation: %w", err)
		}
		if parentIntentID != candidate.IntentID || parentKind != bookingapi.OperationCreate || parentState != models.ManagementBookingOperationOutcomeUnknown || parentParentID != nil || parentRootID == nil || *parentRootID != string(candidate.RootOperationID) {
			return nil, false, fmt.Errorf("invalid management booking duplicate-risk lineage")
		}
	}

	operation, err := scanManagementBookingOperation(tx.QueryRow(ctx, `
		INSERT INTO management_booking_operations (
			operation_id, intent_id, dedup_key, request_fingerprint, kind,
			court_uuid, court_label, start_provider, end_provider, target_match_id, credential_id,
			parent_operation_id, root_operation_id, risk_reason, state, phase, outcome
		) VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7,NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),$11,
			NULLIF($12,'')::uuid,NULLIF($13,'')::uuid,$14,$15,$16,$17)
		ON CONFLICT (intent_id, dedup_key) DO NOTHING
		RETURNING operation_id::text, intent_id, dedup_key, request_fingerprint, kind,
			COALESCE(court_uuid,''), court_label, COALESCE(start_provider,''), COALESCE(end_provider,''),
			COALESCE(target_match_id,''), credential_id, COALESCE(parent_operation_id::text,''),
			COALESCE(root_operation_id::text,''), risk_reason, state, phase, outcome,
			code, warnings, result_expired, provider_state,
			booking_uuid, booking_id, payment_id, match_id, revision, completed_at, created_at, updated_at`,
		candidate.OperationID, candidate.IntentID, candidate.DedupKey, candidate.RequestFingerprint,
		candidate.Kind, candidate.CourtUUID, candidate.CourtLabel, candidate.StartProvider, candidate.EndProvider,
		candidate.TargetMatchID, candidate.CredentialID, candidate.ParentOperationID, candidate.RootOperationID,
		candidate.RiskReason, candidate.State, candidate.Phase, candidate.Outcome,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		operation, err = scanManagementBookingOperation(tx.QueryRow(ctx, `
			SELECT operation_id::text, intent_id, dedup_key, request_fingerprint, kind,
				COALESCE(court_uuid,''), court_label, COALESCE(start_provider,''), COALESCE(end_provider,''),
				COALESCE(target_match_id,''), credential_id, COALESCE(parent_operation_id::text,''),
				COALESCE(root_operation_id::text,''), risk_reason, state, phase, outcome,
				code, warnings, result_expired, provider_state,
				booking_uuid, booking_id, payment_id, match_id, revision, completed_at, created_at, updated_at
			FROM management_booking_operations WHERE intent_id = $1 AND dedup_key = $2`, candidate.IntentID, candidate.DedupKey))
		if err != nil {
			return nil, false, fmt.Errorf("read management booking operation replay: %w", err)
		}
		if operation.RequestFingerprint != candidate.RequestFingerprint {
			return nil, false, &BookingIntentConflictError{Key: candidate.DedupKey}
		}
		if _, err := tx.Exec(ctx, `UPDATE management_booking_intents SET state = 'active', updated_at = NOW() WHERE id = $1 AND state = 'planned'`, candidate.IntentID); err != nil {
			return nil, false, fmt.Errorf("activate management booking intent replay: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit management booking operation replay: %w", err)
		}
		return operation, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("admit management booking operation: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE management_booking_intents SET state = 'active', updated_at = NOW() WHERE id = $1 AND state = 'planned'`, candidate.IntentID); err != nil {
		return nil, false, fmt.Errorf("activate management booking intent: %w", err)
	}
	if candidate.ParentOperationID != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO management_booking_notifications (operation_id, revision, kind) VALUES ($1, $2, 'possible_duplicate') ON CONFLICT DO NOTHING`, operation.OperationID, operation.Revision); err != nil {
			return nil, false, fmt.Errorf("enqueue duplicate-risk notification: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit management booking operation admission: %w", err)
	}
	return operation, true, nil
}

func (r *BookingIntentRepo) GetOperation(ctx context.Context, operationID bookingapi.OperationID) (*models.ManagementBookingOperation, error) {
	return scanManagementBookingOperation(r.pool.QueryRow(ctx, `
		SELECT operation_id::text, intent_id, dedup_key, request_fingerprint, kind,
			COALESCE(court_uuid,''), court_label, COALESCE(start_provider,''), COALESCE(end_provider,''),
			COALESCE(target_match_id,''), credential_id, COALESCE(parent_operation_id::text,''),
			COALESCE(root_operation_id::text,''), risk_reason, state, phase, outcome,
			code, warnings, result_expired, provider_state,
			booking_uuid, booking_id, payment_id, match_id, revision, completed_at, created_at, updated_at
		FROM management_booking_operations WHERE operation_id = $1`, operationID))
}

func (r *BookingIntentRepo) GetOperationByIntentDedup(ctx context.Context, intentID int64, dedupKey string) (*models.ManagementBookingOperation, error) {
	return scanManagementBookingOperation(r.pool.QueryRow(ctx, `
		SELECT operation_id::text, intent_id, dedup_key, request_fingerprint, kind,
			COALESCE(court_uuid,''), court_label, COALESCE(start_provider,''), COALESCE(end_provider,''),
			COALESCE(target_match_id,''), credential_id, COALESCE(parent_operation_id::text,''),
			COALESCE(root_operation_id::text,''), risk_reason, state, phase, outcome,
			code, warnings, result_expired, provider_state,
			booking_uuid, booking_id, payment_id, match_id, revision, completed_at, created_at, updated_at
		FROM management_booking_operations WHERE intent_id = $1 AND dedup_key = $2`, intentID, dedupKey))
}

func (r *BookingIntentRepo) ListOperationsByIntent(ctx context.Context, intentID int64) ([]*models.ManagementBookingOperation, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT operation_id::text, intent_id, dedup_key, request_fingerprint, kind,
			COALESCE(court_uuid,''), court_label, COALESCE(start_provider,''), COALESCE(end_provider,''),
			COALESCE(target_match_id,''), credential_id, COALESCE(parent_operation_id::text,''),
			COALESCE(root_operation_id::text,''), risk_reason, state, phase, outcome,
			code, warnings, result_expired, provider_state,
			booking_uuid, booking_id, payment_id, match_id, revision, completed_at, created_at, updated_at
		FROM management_booking_operations WHERE intent_id = $1 ORDER BY created_at, operation_id`, intentID)
	if err != nil {
		return nil, fmt.Errorf("list management booking operations: %w", err)
	}
	defer rows.Close()
	var operations []*models.ManagementBookingOperation
	for rows.Next() {
		operation, scanErr := scanManagementBookingOperation(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan management booking operation: %w", scanErr)
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

func (r *BookingIntentRepo) ListRecoverableOperations(ctx context.Context, limit int) ([]*models.ManagementBookingOperation, error) {
	if limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("management booking operation recovery limit must be between 1 and 1000")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT operation_id::text, intent_id, dedup_key, request_fingerprint, kind,
			COALESCE(court_uuid,''), court_label, COALESCE(start_provider,''), COALESCE(end_provider,''),
			COALESCE(target_match_id,''), credential_id, COALESCE(parent_operation_id::text,''),
			COALESCE(root_operation_id::text,''), risk_reason, state, phase, outcome,
			code, warnings, result_expired, provider_state,
			booking_uuid, booking_id, payment_id, match_id, revision, completed_at, created_at, updated_at
		FROM management_booking_operations o
		WHERE o.state IN ('planned','accepted','running','outcome_unknown')
		   OR (o.kind = 'create' AND o.state = 'succeeded' AND o.match_id <> '' AND NOT EXISTS (
			SELECT 1 FROM court_bookings cb WHERE cb.management_create_operation_id = o.operation_id
		   ))
		ORDER BY o.last_recovery_at NULLS FIRST, o.updated_at, o.operation_id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list unfinished management booking operations: %w", err)
	}
	defer rows.Close()
	var operations []*models.ManagementBookingOperation
	for rows.Next() {
		operation, scanErr := scanManagementBookingOperation(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan unfinished management booking operation: %w", scanErr)
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

func (r *BookingIntentRepo) MarkRecoveryAttempt(ctx context.Context, operationID bookingapi.OperationID) error {
	if _, err := r.pool.Exec(ctx, `UPDATE management_booking_operations SET last_recovery_at = NOW() WHERE operation_id = $1`, operationID); err != nil {
		return fmt.Errorf("mark management booking recovery attempt: %w", err)
	}
	return nil
}

func (r *BookingIntentRepo) FinalizeIntent(ctx context.Context, intentID int64, state models.BookingIntentState) error {
	if state != models.BookingIntentCompleted && state != models.BookingIntentPartial && state != models.BookingIntentUnknown {
		return fmt.Errorf("unsupported management booking intent terminal state %q", state)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin management booking intent finalization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var updatedID int64
	err = tx.QueryRow(ctx, `
		UPDATE management_booking_intents i SET state = $2, updated_at = NOW()
		WHERE i.id = $1
		  AND NOT EXISTS (
			SELECT 1 FROM management_booking_operations o
			WHERE o.intent_id = i.id AND o.state IN ('planned','accepted','running')
		  )
		  AND (
			($2 = 'unknown' AND EXISTS (SELECT 1 FROM management_booking_operations o WHERE o.intent_id = i.id AND o.state = 'outcome_unknown'))
			OR
			($2 IN ('completed','partial') AND NOT EXISTS (SELECT 1 FROM management_booking_operations o WHERE o.intent_id = i.id AND o.state = 'outcome_unknown'))
		  )
		RETURNING id`, intentID, state).Scan(&updatedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("management booking intent cannot be finalized while operations are unresolved")
	}
	if err != nil {
		return fmt.Errorf("finalize management booking intent: %w", err)
	}
	if err := materializeScheduledBooking(ctx, tx, intentID); err != nil {
		return err
	}
	if state == models.BookingIntentPartial {
		if _, err := tx.Exec(ctx, `
			INSERT INTO management_booking_notifications (operation_id, revision, kind)
			SELECT operation_id, revision, 'partial' FROM management_booking_operations
			WHERE intent_id = $1 ORDER BY created_at DESC LIMIT 1
			ON CONFLICT DO NOTHING`, intentID); err != nil {
			return fmt.Errorf("enqueue partial booking notification: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit management booking intent finalization: %w", err)
	}
	return nil
}

func (r *BookingIntentRepo) ApplyOperationStatus(ctx context.Context, status *bookingapi.OperationStatus) (*models.ManagementBookingOperation, bool, error) {
	if status == nil || status.Revision < 0 {
		return nil, false, fmt.Errorf("valid booking operation status is required")
	}
	operationID, err := bookingapi.ParseOperationID(status.OperationID)
	if err != nil {
		return nil, false, fmt.Errorf("invalid booking operation status ID: %w", err)
	}
	state, err := managementOperationState(status.State)
	if err != nil {
		return nil, false, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("begin booking operation status update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := scanManagementBookingOperation(tx.QueryRow(ctx, `
		SELECT operation_id::text, intent_id, dedup_key, request_fingerprint, kind,
			COALESCE(court_uuid,''), court_label, COALESCE(start_provider,''), COALESCE(end_provider,''),
			COALESCE(target_match_id,''), credential_id, COALESCE(parent_operation_id::text,''),
			COALESCE(root_operation_id::text,''), risk_reason, state, phase, outcome,
			code, warnings, result_expired, provider_state,
			booking_uuid, booking_id, payment_id, match_id, revision, completed_at, created_at, updated_at
		FROM management_booking_operations WHERE operation_id = $1 FOR UPDATE`, operationID))
	if err != nil {
		return nil, false, fmt.Errorf("lock management booking operation status: %w", err)
	}
	if current.Kind != status.Kind {
		return nil, false, fmt.Errorf("booking operation status kind mismatch")
	}
	if status.Revision < current.Revision {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit stale booking operation status read: %w", err)
		}
		return current, false, nil
	}

	bookingUUID, bookingID, paymentID, matchID, providerState := current.BookingUUID, current.BookingID, current.PaymentID, current.MatchID, current.ProviderState
	if status.Evidence != nil {
		if status.Evidence.BookingUUID != "" {
			bookingUUID = status.Evidence.BookingUUID
		}
		if status.Evidence.BookingID > 0 {
			value := int64(status.Evidence.BookingID)
			bookingID = &value
		}
		if status.Evidence.PaymentID > 0 {
			value := int64(status.Evidence.PaymentID)
			paymentID = &value
		}
		if status.Evidence.MatchID != "" {
			matchID = status.Evidence.MatchID
		}
		if status.Evidence.State != "" {
			providerState = status.Evidence.State
		}
	}
	expired := current.ResultExpired || status.ResultExpired
	if current.State != models.ManagementBookingOperationPlanned && status.Revision == current.Revision {
		if current.State == state && current.Phase == status.Phase && current.Outcome == status.Outcome && current.Code == status.Code &&
			current.ResultExpired == expired && current.ProviderState == providerState && current.BookingUUID == bookingUUID &&
			equalOptionalInt64(current.BookingID, bookingID) && equalOptionalInt64(current.PaymentID, paymentID) && current.MatchID == matchID &&
			slices.Equal(current.Warnings, status.Warnings) {
			if err := tx.Commit(ctx); err != nil {
				return nil, false, fmt.Errorf("commit booking operation status replay: %w", err)
			}
			return current, false, nil
		}
		return nil, false, fmt.Errorf("booking operation status revision %d changed immutable projection", status.Revision)
	}
	statusWarnings := status.Warnings
	if statusWarnings == nil {
		statusWarnings = []string{}
	}
	warnings, err := json.Marshal(statusWarnings)
	if err != nil {
		return nil, false, fmt.Errorf("encode booking operation warnings: %w", err)
	}
	var completedAt *time.Time
	if state == models.ManagementBookingOperationSucceeded || state == models.ManagementBookingOperationNotDispatched ||
		state == models.ManagementBookingOperationRejectedNoEffect || state == models.ManagementBookingOperationOutcomeUnknown {
		completed := status.UpdatedAt
		if completed.IsZero() {
			completed = time.Now().UTC()
		}
		completedAt = &completed
	}
	updated, err := scanManagementBookingOperation(tx.QueryRow(ctx, `
		UPDATE management_booking_operations SET
			state = $2, phase = $3, outcome = $4, code = $5, warnings = $6,
			result_expired = $7, provider_state = $8, booking_uuid = $9, booking_id = $10,
			payment_id = $11, match_id = $12, revision = $13,
			completed_at = COALESCE($14, completed_at), updated_at = NOW()
		WHERE operation_id = $1
		RETURNING operation_id::text, intent_id, dedup_key, request_fingerprint, kind,
			COALESCE(court_uuid,''), court_label, COALESCE(start_provider,''), COALESCE(end_provider,''),
			COALESCE(target_match_id,''), credential_id, COALESCE(parent_operation_id::text,''),
			COALESCE(root_operation_id::text,''), risk_reason, state, phase, outcome,
			code, warnings, result_expired, provider_state,
			booking_uuid, booking_id, payment_id, match_id, revision, completed_at, created_at, updated_at`,
		operationID, state, status.Phase, status.Outcome, status.Code, string(warnings), expired, providerState,
		bookingUUID, bookingID, paymentID, matchID, status.Revision, completedAt))
	if err != nil {
		return nil, false, fmt.Errorf("update management booking operation status: %w", err)
	}
	var notificationKind models.ManagementBookingNotificationKind
	switch {
	case state == models.ManagementBookingOperationOutcomeUnknown:
		notificationKind = models.ManagementBookingNotificationOutcomeUnknown
	case state == models.ManagementBookingOperationRejectedNoEffect || state == models.ManagementBookingOperationNotDispatched:
		notificationKind = models.ManagementBookingNotificationMissedAction
	case state == models.ManagementBookingOperationSucceeded && (status.Outcome == bookingapi.OutcomeSuccessWithWarning || len(statusWarnings) > 0 || (status.Kind == bookingapi.OperationCreate && matchID == "")):
		notificationKind = models.ManagementBookingNotificationSuccessWarning
	}
	if notificationKind != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO management_booking_notifications (operation_id, revision, kind) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, operationID, status.Revision, notificationKind); err != nil {
			return nil, false, fmt.Errorf("enqueue management booking notification: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE management_booking_intents i SET state = CASE
			WHEN EXISTS (SELECT 1 FROM management_booking_operations o WHERE o.intent_id = i.id AND o.state = 'outcome_unknown') THEN 'unknown'
			WHEN (SELECT COUNT(*) FROM management_booking_operations o WHERE o.intent_id = i.id AND o.state = 'succeeded') >= i.requested_count THEN 'completed'
			ELSE 'active'
		END, updated_at = NOW()
		WHERE i.id = $1`, current.IntentID); err != nil {
		return nil, false, fmt.Errorf("update management booking intent status: %w", err)
	}
	if err := materializeScheduledBooking(ctx, tx, current.IntentID); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit management booking operation status: %w", err)
	}
	return updated, true, nil
}

func materializeScheduledBooking(ctx context.Context, tx pgx.Tx, intentID int64) error {
	var resultID int64
	err := tx.QueryRow(ctx, `
		INSERT INTO auto_booking_results (venue_id, game_date, game_time, courts, courts_count, management_booking_intent_id)
		SELECT i.venue_id, i.game_date, i.game_time,
			string_agg(o.court_label, ',' ORDER BY o.created_at, o.operation_id), COUNT(*)::int, i.id
		FROM management_booking_intents i
		JOIN management_booking_operations o ON o.intent_id = i.id AND o.kind = 'create' AND o.state = 'succeeded'
		WHERE i.id = $1 AND i.source = 'scheduled' AND i.state IN ('active','completed','partial')
		  AND NOT EXISTS (SELECT 1 FROM management_booking_operations pending WHERE pending.intent_id = i.id AND pending.state IN ('planned','accepted','running','outcome_unknown'))
		GROUP BY i.id
		ON CONFLICT (management_booking_intent_id) WHERE management_booking_intent_id IS NOT NULL
		DO UPDATE SET courts = EXCLUDED.courts, courts_count = EXCLUDED.courts_count
		RETURNING id`, intentID).Scan(&resultID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("materialize scheduled auto-booking result: %w", err)
	}
	var gameID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO games (chat_id, game_date, courts_count, courts, sport, players_per_court, venue_id, management_booking_intent_id)
		SELECT i.group_id, (i.game_date + i.game_time::time) AT TIME ZONE COALESCE(g.timezone, 'UTC'),
			r.courts_count, r.courts, i.sport, COALESCE(vs.players_per_court, 2), i.venue_id, i.id
		FROM management_booking_intents i
		JOIN auto_booking_results r ON r.management_booking_intent_id = i.id
		LEFT JOIN bot_groups g ON g.chat_id = i.group_id
		LEFT JOIN venue_sports vs ON vs.venue_id = i.venue_id AND vs.sport = i.sport
		WHERE i.id = $1
		ON CONFLICT (management_booking_intent_id) WHERE management_booking_intent_id IS NOT NULL
		DO UPDATE SET courts = EXCLUDED.courts, courts_count = EXCLUDED.courts_count
		RETURNING id`, intentID).Scan(&gameID)
	if err != nil {
		return fmt.Errorf("materialize scheduled booking game: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE auto_booking_results SET game_id = $2 WHERE id = $1`, resultID, gameID); err != nil {
		return fmt.Errorf("link scheduled auto-booking result game: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE management_booking_intents SET game_id = $2, updated_at = NOW() WHERE id = $1`, intentID, gameID); err != nil {
		return fmt.Errorf("link scheduled booking intent game: %w", err)
	}
	return nil
}

func (r *BookingIntentRepo) ClaimPendingNotifications(ctx context.Context, limit int) ([]*models.ManagementBookingNotification, error) {
	if limit <= 0 || limit > 100 {
		return nil, fmt.Errorf("notification claim limit must be between 1 and 100")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin management booking notification claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		WITH selected AS (
			SELECT id FROM management_booking_notifications WHERE claimed_at IS NULL
			ORDER BY created_at, id LIMIT $1 FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE management_booking_notifications n SET claimed_at = NOW()
			FROM selected s WHERE n.id = s.id
			RETURNING n.id, n.operation_id, n.revision, n.kind, n.created_at
		)
		SELECT c.id, c.operation_id::text, c.revision, c.kind, i.group_id, i.venue_id, i.game_date, i.game_time, c.created_at
		FROM claimed c
		JOIN management_booking_operations o ON o.operation_id = c.operation_id
		JOIN management_booking_intents i ON i.id = o.intent_id
		ORDER BY c.created_at, c.id`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim management booking notifications: %w", err)
	}
	defer rows.Close()
	var notifications []*models.ManagementBookingNotification
	for rows.Next() {
		var notification models.ManagementBookingNotification
		if err := rows.Scan(&notification.ID, &notification.OperationID, &notification.Revision, &notification.Kind,
			&notification.GroupID, &notification.VenueID, &notification.GameDate, &notification.GameTime, &notification.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan management booking notification: %w", err)
		}
		notifications = append(notifications, &notification)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate management booking notifications: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit management booking notification claim: %w", err)
	}
	return notifications, nil
}

func (r *BookingIntentRepo) MarkNotificationDelivered(ctx context.Context, id int64) error {
	if _, err := r.pool.Exec(ctx, `UPDATE management_booking_notifications SET delivered_at = NOW() WHERE id = $1 AND claimed_at IS NOT NULL`, id); err != nil {
		return fmt.Errorf("mark management booking notification delivered: %w", err)
	}
	return nil
}

func (r *BookingIntentRepo) GetMetrics(ctx context.Context) (*models.ManagementBookingMetrics, error) {
	var metrics models.ManagementBookingMetrics
	err := r.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE state = 'planned'), COUNT(*) FILTER (WHERE state = 'accepted'),
			COUNT(*) FILTER (WHERE state = 'running'), COUNT(*) FILTER (WHERE state = 'outcome_unknown'),
			COUNT(*) FILTER (WHERE state = 'succeeded'), COUNT(*) FILTER (WHERE state = 'rejected_no_effect'),
			COUNT(*) FILTER (WHERE parent_operation_id IS NOT NULL),
			(SELECT COUNT(*) FROM management_booking_notifications WHERE claimed_at IS NULL),
			MIN(created_at) FILTER (WHERE state IN ('planned','accepted','running','outcome_unknown'))
		FROM management_booking_operations`).Scan(
		&metrics.Planned, &metrics.Accepted, &metrics.Running, &metrics.OutcomeUnknown,
		&metrics.Succeeded, &metrics.RejectedNoEffect, &metrics.RiskAuthorized,
		&metrics.PendingNotices, &metrics.OldestPendingAt,
	)
	if err != nil {
		return nil, fmt.Errorf("read management booking metrics: %w", err)
	}
	return &metrics, nil
}

func managementOperationState(state bookingapi.OperationState) (models.ManagementBookingOperationState, error) {
	switch state {
	case bookingapi.StateAccepted:
		return models.ManagementBookingOperationAccepted, nil
	case bookingapi.StateRunning:
		return models.ManagementBookingOperationRunning, nil
	case bookingapi.StateNotDispatched:
		return models.ManagementBookingOperationNotDispatched, nil
	case bookingapi.StateRejectedNoEffect:
		return models.ManagementBookingOperationRejectedNoEffect, nil
	case bookingapi.StateSucceeded:
		return models.ManagementBookingOperationSucceeded, nil
	case bookingapi.StateOutcomeUnknown:
		return models.ManagementBookingOperationOutcomeUnknown, nil
	default:
		return "", fmt.Errorf("unsupported booking operation state %q", state)
	}
}

func equalOptionalInt64(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

type rowScanner interface {
	Scan(...any) error
}

func scanBookingIntent(row rowScanner) (*models.BookingIntent, error) {
	var intent models.BookingIntent
	err := row.Scan(
		&intent.ID, &intent.RequestKey, &intent.RequestFingerprint, &intent.Action, &intent.Source,
		&intent.Scope.ServiceKey, &intent.Scope.Provider, &intent.Scope.FacilityID, &intent.Scope.FacilityUUID,
		&intent.Scope.FacilitySlug, &intent.Scope.Sport, &intent.GroupID, &intent.VenueID, &intent.GameID,
		&intent.GameDate, &intent.GameTime, &intent.RequestedCount, &intent.Generation, &intent.State,
		&intent.CreatedAt, &intent.UpdatedAt,
	)
	return &intent, err
}

func scanManagementBookingOperation(row rowScanner) (*models.ManagementBookingOperation, error) {
	var operation models.ManagementBookingOperation
	err := row.Scan(
		&operation.OperationID, &operation.IntentID, &operation.DedupKey, &operation.RequestFingerprint,
		&operation.Kind, &operation.CourtUUID, &operation.CourtLabel, &operation.StartProvider,
		&operation.EndProvider, &operation.TargetMatchID, &operation.CredentialID,
		&operation.ParentOperationID, &operation.RootOperationID, &operation.RiskReason,
		&operation.State, &operation.Phase, &operation.Outcome, &operation.Code, &operation.Warnings,
		&operation.ResultExpired, &operation.ProviderState, &operation.BookingUUID,
		&operation.BookingID, &operation.PaymentID, &operation.MatchID, &operation.Revision,
		&operation.CompletedAt, &operation.CreatedAt, &operation.UpdatedAt,
	)
	return &operation, err
}

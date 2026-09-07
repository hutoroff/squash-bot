package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EvidenceRepo struct {
	pool *pgxpool.Pool
}

func NewEvidenceRepo(pool *pgxpool.Pool) *EvidenceRepo { return &EvidenceRepo{pool: pool} }

// VerifyIdentityKey pins the durable account-identity namespace. Startup must
// fail rather than accept mutations when a different key is presented later.
func (r *EvidenceRepo) VerifyIdentityKey(ctx context.Context, key []byte) error {
	marker, err := IdentityKeyMarker(key)
	if err != nil {
		return err
	}
	return r.PutMetadataOnce(ctx, "account_identity_key_marker_v1", marker)
}

// PutMetadataOnce stores immutable ledger metadata such as the account identity
// key verification marker. Repeating the same value is accepted; changing it is
// rejected so a deployment cannot silently split the identity namespace.
func (r *EvidenceRepo) PutMetadataOnce(ctx context.Context, key, value string) error {
	var stored string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO booking_metadata (key, value)
		VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = booking_metadata.value
		RETURNING value`, key, value).Scan(&stored)
	if err != nil {
		return fmt.Errorf("store booking metadata: %w", err)
	}
	if stored != value {
		return &ConflictError{Code: bookingapi.ErrorContractUpgradeRequired}
	}
	return nil
}

func (r *EvidenceRepo) GetMetadata(ctx context.Context, key string) (string, error) {
	var value string
	if err := r.pool.QueryRow(ctx, `SELECT value FROM booking_metadata WHERE key = $1`, key).Scan(&value); err != nil {
		return "", err
	}
	return value, nil
}

// UpsertProviderBooking fills creation evidence while the matching executor
// still owns the operation. It refuses stale ownership, cancellation state and
// contradictory identifiers instead of overwriting history.
func (r *EvidenceRepo) UpsertProviderBooking(ctx context.Context, booking ProviderBooking, executorID bookingapi.OperationID) (*ProviderBooking, error) {
	if booking.BookingState == "cancelled" || booking.CancellationOperationID != "" {
		return nil, fmt.Errorf("cancellation evidence requires an atomic cancellation transition")
	}
	if (booking.BookingState != "unknown" && booking.BookingState != "reserved" && booking.BookingState != "confirmed") ||
		(booking.PaymentState != "unknown" && booking.PaymentState != "confirmed" && booking.PaymentState != "rejected") {
		return nil, fmt.Errorf("invalid provider booking evidence state")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin booking provider evidence: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	operation, err := getOperationForUpdate(ctx, tx, booking.CreateOperationID)
	if err != nil {
		return nil, fmt.Errorf("lock operation for provider evidence: %w", err)
	}
	if operation.Kind != bookingapi.OperationCreate || operation.ExecutorID != string(executorID) ||
		(operation.State != bookingapi.StateAccepted && operation.State != bookingapi.StateRunning) ||
		operation.Scope.Provider != booking.Provider || operation.Scope.FacilityUUID != booking.FacilityUUID {
		return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(booking.CreateOperationID)}
	}
	if existing, err := getProviderBookingTx(ctx, tx, booking.CreateOperationID); err == nil {
		changed, conflict := providerBookingChange(existing, &booking)
		if conflict {
			return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: string(booking.CreateOperationID)}
		}
		if !changed {
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("commit booking provider evidence replay: %w", err)
			}
			return existing, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("read booking provider evidence: %w", err)
	}

	var result ProviderBooking
	var operationID, importID, cancellationOperationID string
	err = tx.QueryRow(ctx, `
		INSERT INTO booking_provider_bookings (
			create_operation_id, provider, facility_uuid, booking_uuid, booking_id,
			payment_id, match_id, payment_state, booking_state, cancellation_operation_id
		) VALUES (
			$1, $2, $3, NULLIF($4, ''), $5, $6, NULLIF($7, ''), $8, $9, NULLIF($10, '')::uuid
		)
		ON CONFLICT (create_operation_id) DO UPDATE SET
			booking_uuid = COALESCE(booking_provider_bookings.booking_uuid, EXCLUDED.booking_uuid),
			booking_id = COALESCE(booking_provider_bookings.booking_id, EXCLUDED.booking_id),
			payment_id = COALESCE(booking_provider_bookings.payment_id, EXCLUDED.payment_id),
			match_id = COALESCE(booking_provider_bookings.match_id, EXCLUDED.match_id),
			payment_state = CASE
				WHEN booking_provider_bookings.payment_state = 'unknown' THEN EXCLUDED.payment_state
				ELSE booking_provider_bookings.payment_state
			END,
			booking_state = CASE
				WHEN booking_provider_bookings.booking_state = 'unknown' THEN EXCLUDED.booking_state
				WHEN booking_provider_bookings.booking_state = 'reserved' AND EXCLUDED.booking_state IN ('confirmed', 'cancelled') THEN EXCLUDED.booking_state
				WHEN booking_provider_bookings.booking_state = 'confirmed' AND EXCLUDED.booking_state = 'cancelled' THEN EXCLUDED.booking_state
				ELSE booking_provider_bookings.booking_state
			END,
			cancellation_operation_id = COALESCE(booking_provider_bookings.cancellation_operation_id, EXCLUDED.cancellation_operation_id),
			updated_at = NOW()
		WHERE booking_provider_bookings.provider = EXCLUDED.provider
		  AND booking_provider_bookings.facility_uuid = EXCLUDED.facility_uuid
		  AND (booking_provider_bookings.booking_uuid IS NULL OR EXCLUDED.booking_uuid IS NULL OR booking_provider_bookings.booking_uuid = EXCLUDED.booking_uuid)
		  AND (booking_provider_bookings.booking_id IS NULL OR EXCLUDED.booking_id IS NULL OR booking_provider_bookings.booking_id = EXCLUDED.booking_id)
		  AND (booking_provider_bookings.payment_id IS NULL OR EXCLUDED.payment_id IS NULL OR booking_provider_bookings.payment_id = EXCLUDED.payment_id)
		  AND (booking_provider_bookings.match_id IS NULL OR EXCLUDED.match_id IS NULL OR booking_provider_bookings.match_id = EXCLUDED.match_id)
		  AND (booking_provider_bookings.cancellation_operation_id IS NULL OR EXCLUDED.cancellation_operation_id IS NULL OR booking_provider_bookings.cancellation_operation_id = EXCLUDED.cancellation_operation_id)
		  AND NOT (booking_provider_bookings.payment_state IN ('confirmed', 'rejected') AND EXCLUDED.payment_state IN ('confirmed', 'rejected')
		           AND booking_provider_bookings.payment_state <> EXCLUDED.payment_state)
		  AND NOT (booking_provider_bookings.booking_state = 'cancelled' AND EXCLUDED.booking_state <> 'cancelled')
		RETURNING id, COALESCE(create_operation_id::text, ''), COALESCE(import_id::text, ''), provider, facility_uuid,
		          COALESCE(booking_uuid, ''), booking_id, payment_id, COALESCE(match_id, ''),
		          payment_state, booking_state, COALESCE(cancellation_operation_id::text, ''), created_at, updated_at`,
		booking.CreateOperationID, booking.Provider, booking.FacilityUUID, booking.BookingUUID,
		booking.BookingID, booking.PaymentID, booking.MatchID, booking.PaymentState,
		booking.BookingState, booking.CancellationOperationID,
	).Scan(&result.ID, &operationID, &importID, &result.Provider, &result.FacilityUUID,
		&result.BookingUUID, &result.BookingID, &result.PaymentID, &result.MatchID,
		&result.PaymentState, &result.BookingState, &cancellationOperationID,
		&result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: string(booking.CreateOperationID)}
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: string(booking.CreateOperationID)}
		}
		return nil, fmt.Errorf("upsert booking provider evidence: %w", err)
	}
	result.CreateOperationID = bookingapi.OperationID(operationID)
	result.ImportID = bookingapi.OperationID(importID)
	result.CancellationOperationID = cancellationOperationID
	updateResult, err := tx.Exec(ctx, `
		UPDATE booking_operations
		SET revision = revision + 1, updated_at = NOW()
		WHERE operation_id = $1 AND executor_id = $2`, booking.CreateOperationID, executorID)
	if err != nil {
		return nil, fmt.Errorf("advance booking provider evidence revision: %w", err)
	}
	if updateResult.RowsAffected() != 1 {
		return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(booking.CreateOperationID)}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit booking provider evidence: %w", err)
	}
	return &result, nil
}

func (r *EvidenceRepo) GetProviderBooking(ctx context.Context, createOperationID bookingapi.OperationID) (*ProviderBooking, error) {
	return getProviderBookingRow(r.pool.QueryRow(ctx, providerBookingSelect, createOperationID))
}

func getProviderBookingTx(ctx context.Context, tx pgx.Tx, createOperationID bookingapi.OperationID) (*ProviderBooking, error) {
	return getProviderBookingRow(tx.QueryRow(ctx, providerBookingSelect+` FOR UPDATE`, createOperationID))
}

const providerBookingSelect = `
	SELECT id, COALESCE(create_operation_id::text, ''), COALESCE(import_id::text, ''), provider, facility_uuid,
	       COALESCE(booking_uuid, ''), booking_id, payment_id, COALESCE(match_id, ''),
	       payment_state, booking_state, COALESCE(cancellation_operation_id::text, ''), created_at, updated_at
	FROM booking_provider_bookings
	WHERE create_operation_id = $1`

func getProviderBookingRow(row pgx.Row) (*ProviderBooking, error) {
	var result ProviderBooking
	var operationID, importID, cancellationOperationID string
	err := row.Scan(
		&result.ID, &operationID, &importID, &result.Provider, &result.FacilityUUID,
		&result.BookingUUID, &result.BookingID, &result.PaymentID, &result.MatchID,
		&result.PaymentState, &result.BookingState, &cancellationOperationID,
		&result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return nil, err
	}
	result.CreateOperationID = bookingapi.OperationID(operationID)
	result.ImportID = bookingapi.OperationID(importID)
	result.CancellationOperationID = cancellationOperationID
	return &result, nil
}

func providerBookingChange(existing, requested *ProviderBooking) (changed, conflict bool) {
	if existing.Provider != requested.Provider || existing.FacilityUUID != requested.FacilityUUID {
		return false, true
	}
	for _, values := range [][2]string{
		{existing.BookingUUID, requested.BookingUUID},
		{existing.MatchID, requested.MatchID},
	} {
		if values[1] == "" {
			continue
		}
		if values[0] == "" {
			changed = true
		} else if values[0] != values[1] {
			return false, true
		}
	}
	for _, values := range [][2]*int64{
		{existing.BookingID, requested.BookingID},
		{existing.PaymentID, requested.PaymentID},
	} {
		if values[1] == nil {
			continue
		}
		if values[0] == nil {
			changed = true
		} else if *values[0] != *values[1] {
			return false, true
		}
	}
	validPaymentState := map[string]bool{"unknown": true, "confirmed": true, "rejected": true}
	if !validPaymentState[requested.PaymentState] {
		return false, true
	}
	if requested.PaymentState != "unknown" {
		if existing.PaymentState == "unknown" {
			changed = true
		} else if existing.PaymentState != requested.PaymentState {
			return false, true
		}
	}
	bookingRank := map[string]int{"unknown": 0, "reserved": 1, "confirmed": 2, "cancelled": 3}
	existingRank, existingValid := bookingRank[existing.BookingState]
	requestedRank, requestedValid := bookingRank[requested.BookingState]
	if !existingValid || !requestedValid {
		return false, true
	}
	if requestedRank > existingRank {
		changed = true
	}
	return changed, false
}

// ApplyVerifiedResolution atomically records credentialed exact-match evidence,
// resolves an unknown operation, and updates account/resource blockers. It does
// not dispatch provider work. Reusing an event key is accepted only for exactly
// the same immutable resolution.
func (r *EvidenceRepo) ApplyVerifiedResolution(ctx context.Context, resolution VerifiedResolution) (*Operation, error) {
	if resolution.ExpectedRevision <= 0 || (resolution.EventType != "reconciliation" && resolution.EventType != "resolution") ||
		resolution.MatchID == "" || (resolution.ProviderState != "active" && resolution.ProviderState != "cancelled") ||
		len(resolution.Actor) == 0 || len(resolution.Actor) > 128 || len(resolution.Reason) == 0 || len(resolution.Reason) > 256 {
		return nil, fmt.Errorf("invalid verified booking resolution")
	}
	evidence, err := json.Marshal(struct {
		MatchID       string `json:"match_id"`
		ProviderState string `json:"provider_state"`
		Actor         string `json:"actor"`
	}{MatchID: resolution.MatchID, ProviderState: resolution.ProviderState, Actor: resolution.Actor})
	if err != nil {
		return nil, fmt.Errorf("marshal verified booking resolution: %w", err)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin verified booking resolution: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	operation, err := getOperationForUpdate(ctx, tx, resolution.OperationID)
	if err != nil {
		return nil, fmt.Errorf("lock operation for verified resolution: %w", err)
	}
	expectedRevision := resolution.ExpectedRevision
	event := OperationEvent{
		OperationID: resolution.OperationID, EventKey: resolution.EventKey, EventType: resolution.EventType,
		ExpectedRevision: &expectedRevision, ResultingRevision: expectedRevision + 1,
		Reason: resolution.Reason, Evidence: evidence,
	}
	if stored, eventErr := getEventTx(ctx, tx, resolution.OperationID, resolution.EventKey); eventErr == nil {
		if !sameVerifiedResolutionEvent(stored, event, resolution.MatchID, resolution.Actor) {
			return nil, &ConflictError{Code: bookingapi.ErrorIdempotencyKeyConflict, ConflictingOperationID: string(resolution.OperationID)}
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit verified resolution replay: %w", err)
		}
		return operation, nil
	} else if !errors.Is(eventErr, pgx.ErrNoRows) {
		return nil, fmt.Errorf("read verified resolution event: %w", eventErr)
	}
	if operation.State != bookingapi.StateOutcomeUnknown || operation.ExecutorID != "" || operation.Revision != resolution.ExpectedRevision {
		return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(resolution.OperationID)}
	}

	if operation.Kind == bookingapi.OperationCreate {
		bookingState := "confirmed"
		if resolution.ProviderState == "cancelled" {
			bookingState = "cancelled"
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO booking_provider_bookings (
				create_operation_id, provider, facility_uuid, match_id, payment_state, booking_state
			) VALUES ($1, $2, $3, $4, 'confirmed', $5)
			ON CONFLICT (create_operation_id) DO UPDATE SET
				match_id = COALESCE(booking_provider_bookings.match_id, EXCLUDED.match_id),
				payment_state = CASE WHEN booking_provider_bookings.payment_state = 'unknown' THEN 'confirmed' ELSE booking_provider_bookings.payment_state END,
				booking_state = CASE
					WHEN EXCLUDED.booking_state = 'cancelled' THEN 'cancelled'
					WHEN booking_provider_bookings.booking_state IN ('unknown', 'reserved') THEN 'confirmed'
					ELSE booking_provider_bookings.booking_state
				END,
				updated_at = NOW()
			WHERE booking_provider_bookings.provider = EXCLUDED.provider
			  AND booking_provider_bookings.facility_uuid = EXCLUDED.facility_uuid
			  AND (booking_provider_bookings.match_id IS NULL OR booking_provider_bookings.match_id = EXCLUDED.match_id)
			  AND booking_provider_bookings.payment_state <> 'rejected'
			  AND NOT (booking_provider_bookings.booking_state = 'cancelled' AND EXCLUDED.booking_state <> 'cancelled')`,
			operation.OperationID, operation.Scope.Provider, operation.Scope.FacilityUUID, resolution.MatchID, bookingState)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: string(operation.OperationID)}
			}
			return nil, fmt.Errorf("record verified provider booking: %w", err)
		}
		var storedMatchID, paymentState, storedBookingState string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(match_id, ''), payment_state, booking_state
			FROM booking_provider_bookings WHERE create_operation_id = $1`, operation.OperationID,
		).Scan(&storedMatchID, &paymentState, &storedBookingState); err != nil {
			return nil, fmt.Errorf("verify resolved provider booking: %w", err)
		}
		if storedMatchID != resolution.MatchID || paymentState != "confirmed" ||
			(resolution.ProviderState == "active" && storedBookingState == "cancelled") {
			return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: string(operation.OperationID)}
		}
		if resolution.ProviderState == "cancelled" {
			if _, err := tx.Exec(ctx, `
				UPDATE booking_resource_claims SET state = 'released', released_at = NOW()
				WHERE operation_id = $1 AND claim_type = 'occupancy' AND state = 'active'`, operation.OperationID); err != nil {
				return nil, fmt.Errorf("release resolved cancelled occupancy: %w", err)
			}
		}
	} else {
		if operation.TargetMatchID != resolution.MatchID || resolution.ProviderState != "cancelled" {
			return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(operation.OperationID)}
		}
		var createOperationID, importID string
		err := tx.QueryRow(ctx, `
			UPDATE booking_provider_bookings b
			SET booking_state = 'cancelled', cancellation_operation_id = COALESCE(cancellation_operation_id, $1), updated_at = NOW()
			WHERE b.provider = $2 AND b.facility_uuid = $3 AND b.match_id = $4
			  AND (
				EXISTS (SELECT 1 FROM booking_operations o WHERE o.operation_id = b.create_operation_id AND o.account_id = $5)
				OR EXISTS (SELECT 1 FROM booking_imports i WHERE i.import_id = b.import_id AND i.account_id = $5)
			  )
			RETURNING COALESCE(b.create_operation_id::text, ''), COALESCE(b.import_id::text, '')`, operation.OperationID, operation.Scope.Provider,
			operation.Scope.FacilityUUID, resolution.MatchID, operation.AccountID,
		).Scan(&createOperationID, &importID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: string(operation.OperationID)}
		}
		if err != nil {
			return nil, fmt.Errorf("record verified provider cancellation: %w", err)
		}
		if importID != "" {
			if _, err := tx.Exec(ctx, `UPDATE booking_imports SET provider_state = 'cancelled', revision = revision + 1, updated_at = NOW() WHERE import_id = $1`, importID); err != nil {
				return nil, fmt.Errorf("update verified cancelled import: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking_resource_claims SET state = 'released', released_at = NOW()
			WHERE (
				(operation_id = NULLIF($1, '')::uuid AND claim_type = 'occupancy')
				OR (import_id = NULLIF($2, '')::uuid AND claim_type = 'occupancy')
				OR (operation_id = $3 AND claim_type = 'cancellation')
			) AND state = 'active'`, createOperationID, importID, operation.OperationID); err != nil {
			return nil, fmt.Errorf("release verified cancellation claims: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE booking_operations
		SET state = 'succeeded', outcome = 'success', phase = 'finalization',
		    revision = $2, updated_at = NOW()
		WHERE operation_id = $1`, operation.OperationID, resolution.ExpectedRevision+1); err != nil {
		return nil, fmt.Errorf("finalize verified booking resolution: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking_accounts
		SET state = 'available', quarantine_operation_id = NULL, updated_at = NOW()
		WHERE id = $1 AND quarantine_operation_id = $2`, operation.AccountID, operation.OperationID); err != nil {
		return nil, fmt.Errorf("clear resolved booking account quarantine: %w", err)
	}
	var storedOperationID, storedEventKey string
	if err := tx.QueryRow(ctx, `
		INSERT INTO booking_operation_events (
			operation_id, event_key, event_type, expected_revision, resulting_revision, reason, evidence
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING operation_id::text, event_key::text`, operation.OperationID, resolution.EventKey,
		resolution.EventType, resolution.ExpectedRevision, resolution.ExpectedRevision+1, resolution.Reason, evidence,
	).Scan(&storedOperationID, &storedEventKey); err != nil {
		return nil, fmt.Errorf("append verified booking resolution event: %w", err)
	}
	resolved, err := getOperationTx(ctx, tx, operation.OperationID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit verified booking resolution: %w", err)
	}
	return resolved, nil
}

// AppendEvent is idempotent by operation/event key and atomically advances the
// operation revision. A reused key with different immutable contents or a stale
// expected revision is rejected.
func (r *EvidenceRepo) AppendEvent(ctx context.Context, event OperationEvent) (*OperationEvent, error) {
	evidence := event.Evidence
	if len(evidence) == 0 {
		evidence = json.RawMessage(`{}`)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin booking operation event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	operation, err := getOperationForUpdate(ctx, tx, event.OperationID)
	if err != nil {
		return nil, fmt.Errorf("lock booking operation for event: %w", err)
	}

	result, err := getEventTx(ctx, tx, event.OperationID, event.EventKey)
	if err == nil {
		if !sameEvent(result, event, evidence) {
			return nil, &ConflictError{Code: bookingapi.ErrorIdempotencyKeyConflict, ConflictingOperationID: string(event.OperationID)}
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit booking operation event replay: %w", err)
		}
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if event.ExpectedRevision == nil || *event.ExpectedRevision != operation.Revision || event.ResultingRevision != operation.Revision+1 {
		return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(event.OperationID)}
	}

	var operationID, eventKey string
	result = &OperationEvent{}
	err = tx.QueryRow(ctx, `
		INSERT INTO booking_operation_events (
			operation_id, event_key, event_type, expected_revision,
			resulting_revision, reason, evidence
		) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7)
		RETURNING id, operation_id::text, event_key::text, event_type,
		          expected_revision, resulting_revision, COALESCE(reason, ''), evidence, created_at`,
		event.OperationID, event.EventKey, event.EventType, event.ExpectedRevision,
		event.ResultingRevision, event.Reason, evidence,
	).Scan(&result.ID, &operationID, &eventKey, &result.EventType,
		&result.ExpectedRevision, &result.ResultingRevision, &result.Reason, &result.Evidence, &result.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("append booking operation event: %w", err)
	}
	result.OperationID = bookingapi.OperationID(operationID)
	result.EventKey = bookingapi.OperationID(eventKey)
	updateResult, err := tx.Exec(ctx, `
		UPDATE booking_operations
		SET revision = $3, updated_at = NOW()
		WHERE operation_id = $1 AND revision = $2`, event.OperationID, event.ExpectedRevision, event.ResultingRevision)
	if err != nil {
		return nil, fmt.Errorf("advance booking operation event revision: %w", err)
	}
	if updateResult.RowsAffected() != 1 {
		return nil, &ConflictError{Code: bookingapi.ErrorOperationOutcomeUnknown, ConflictingOperationID: string(event.OperationID)}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit booking operation event: %w", err)
	}
	return result, nil
}

func (r *EvidenceRepo) GetEvent(ctx context.Context, operationID, eventKey bookingapi.OperationID) (*OperationEvent, error) {
	return getEventRow(r.pool.QueryRow(ctx, `
		SELECT id, operation_id::text, event_key::text, event_type,
		       expected_revision, resulting_revision, COALESCE(reason, ''), evidence, created_at
		FROM booking_operation_events
		WHERE operation_id = $1 AND event_key = $2`, operationID, eventKey))
}

func getEventTx(ctx context.Context, tx pgx.Tx, operationID, eventKey bookingapi.OperationID) (*OperationEvent, error) {
	return getEventRow(tx.QueryRow(ctx, `
		SELECT id, operation_id::text, event_key::text, event_type,
		       expected_revision, resulting_revision, COALESCE(reason, ''), evidence, created_at
		FROM booking_operation_events
		WHERE operation_id = $1 AND event_key = $2`, operationID, eventKey))
}

func getEventRow(row pgx.Row) (*OperationEvent, error) {
	var result OperationEvent
	var storedOperationID, storedEventKey string
	err := row.Scan(
		&result.ID, &storedOperationID, &storedEventKey, &result.EventType,
		&result.ExpectedRevision, &result.ResultingRevision, &result.Reason, &result.Evidence, &result.CreatedAt)
	if err != nil {
		return nil, err
	}
	result.OperationID = bookingapi.OperationID(storedOperationID)
	result.EventKey = bookingapi.OperationID(storedEventKey)
	return &result, nil
}

func sameVerifiedResolutionEvent(stored *OperationEvent, requested OperationEvent, matchID, actor string) bool {
	var evidence struct {
		MatchID string `json:"match_id"`
		Actor   string `json:"actor"`
	}
	if json.Unmarshal(stored.Evidence, &evidence) != nil {
		return false
	}
	return stored.EventType == requested.EventType && equalOptionalInt64(stored.ExpectedRevision, requested.ExpectedRevision) &&
		stored.ResultingRevision == requested.ResultingRevision && stored.Reason == requested.Reason &&
		evidence.MatchID == matchID && evidence.Actor == actor
}

func sameEvent(stored *OperationEvent, requested OperationEvent, evidence []byte) bool {
	return stored.EventType == requested.EventType && equalOptionalInt64(stored.ExpectedRevision, requested.ExpectedRevision) &&
		stored.ResultingRevision == requested.ResultingRevision && stored.Reason == requested.Reason && equalJSON(stored.Evidence, evidence)
}

func equalOptionalInt64(left, right *int64) bool {
	return reflect.DeepEqual(left, right)
}

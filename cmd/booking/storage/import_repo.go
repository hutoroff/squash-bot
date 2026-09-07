package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ImportRepo struct {
	pool *pgxpool.Pool
}

func NewImportRepo(pool *pgxpool.Pool) *ImportRepo { return &ImportRepo{pool: pool} }

func (r *ImportRepo) AdmitVerified(ctx context.Context, request bookingapi.ValidatedImport, start, end time.Time, startProvider, endProvider, providerState string) (*ImportResult, error) {
	if !end.After(start) || !validImportedProviderTime(startProvider, start) || !validImportedProviderTime(endProvider, end) ||
		(providerState != "active" && providerState != "cancelled") {
		return nil, fmt.Errorf("invalid verified import evidence")
	}
	fingerprint := request.Fingerprint()
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin booking import: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockKey(ctx, tx, "ledger-key:"+string(request.ImportID)); err != nil {
		return nil, err
	}
	if existing, err := getImportTx(ctx, tx, request.ImportID); err == nil {
		if existing.RequestFingerprint != fingerprint {
			return nil, &ConflictError{Code: bookingapi.ErrorIdempotencyKeyConflict, ConflictingOperationID: string(request.ImportID), ConflictingResourceKind: "import"}
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit booking import replay: %w", err)
		}
		return &ImportResult{Disposition: ImportReplay, Import: existing}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("read booking import replay: %w", err)
	}
	var operationExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM booking_operations WHERE operation_id = $1)`, request.ImportID).Scan(&operationExists); err != nil {
		return nil, fmt.Errorf("check booking ledger key collision: %w", err)
	}
	if operationExists {
		return nil, &ConflictError{Code: bookingapi.ErrorIdempotencyKeyConflict, ConflictingOperationID: string(request.ImportID), ConflictingResourceKind: "operation"}
	}

	accountID, _, activeOperation, _, err := lockAccount(ctx, tx, request.Scope.Provider, request.AccountIdentity)
	if err != nil {
		return nil, err
	}
	if activeOperation != "" {
		return nil, &ConflictError{Code: bookingapi.ErrorAccountBlocked, ConflictingOperationID: activeOperation}
	}
	resourceKey := "occupancy:" + request.Scope.Provider + ":" + request.Scope.FacilityUUID + ":" + request.CourtUUID
	if err := lockKey(ctx, tx, resourceKey); err != nil {
		return nil, err
	}
	var conflictingOwner, conflictingKind string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(operation_id::text, import_id::text),
		       CASE WHEN import_id IS NULL THEN 'operation' ELSE 'import' END
		FROM booking_resource_claims
		WHERE claim_type = 'occupancy' AND state = 'active'
		  AND provider = $1 AND facility_uuid = $2 AND court_uuid = $3
		  AND start_at < $5 AND end_at > $4
		LIMIT 1`, request.Scope.Provider, request.Scope.FacilityUUID, request.CourtUUID, start, end).Scan(&conflictingOwner, &conflictingKind)
	if err == nil {
		return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: conflictingOwner, ConflictingResourceKind: conflictingKind}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check booking import occupancy: %w", err)
	}
	var existingProviderOwner, existingProviderKind string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(create_operation_id::text, import_id::text),
		       CASE WHEN import_id IS NULL THEN 'operation' ELSE 'import' END
		FROM booking_provider_bookings
		WHERE provider = $1 AND facility_uuid = $2 AND match_id = $3
		FOR UPDATE`, request.Scope.Provider, request.Scope.FacilityUUID, request.MatchID).Scan(&existingProviderOwner, &existingProviderKind)
	if err == nil {
		return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: existingProviderOwner, ConflictingResourceKind: existingProviderKind}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check imported provider ownership: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO booking_imports (
			import_id, request_fingerprint, service_key, provider, facility_id,
			facility_uuid, facility_slug, sport, account_id, court_uuid,
			start_at, end_at, start_provider, end_provider, match_id, provider_state, actor, reason
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
		request.ImportID, fingerprint, request.Scope.ServiceKey, request.Scope.Provider, request.Scope.FacilityID,
		request.Scope.FacilityUUID, request.Scope.FacilitySlug, request.Scope.Sport, accountID, request.CourtUUID,
		start, end, startProvider, endProvider, request.MatchID, providerState, request.Actor, request.Reason)
	if err != nil {
		return nil, fmt.Errorf("insert booking import: %w", err)
	}
	bookingState := "confirmed"
	if providerState == "cancelled" {
		bookingState = "cancelled"
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO booking_provider_bookings (
			import_id, provider, facility_uuid, match_id, payment_state, booking_state
		) VALUES ($1, $2, $3, $4, 'confirmed', $5)`,
		request.ImportID, request.Scope.Provider, request.Scope.FacilityUUID, request.MatchID, bookingState)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, &ConflictError{Code: bookingapi.ErrorBookingConflict, ConflictingOperationID: string(request.ImportID)}
		}
		return nil, fmt.Errorf("insert imported provider booking: %w", err)
	}
	claimState := "active"
	var releasedAt *time.Time
	if providerState == "cancelled" {
		claimState = "released"
		now := time.Now()
		releasedAt = &now
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO booking_resource_claims (
			import_id, claim_type, provider, facility_uuid, court_uuid,
			start_at, end_at, state, released_at
		) VALUES ($1, 'occupancy', $2, $3, $4, $5, $6, $7, $8)`,
		request.ImportID, request.Scope.Provider, request.Scope.FacilityUUID, request.CourtUUID,
		start, end, claimState, releasedAt)
	if err != nil {
		return nil, fmt.Errorf("insert booking import occupancy: %w", err)
	}
	imported, err := getImportTx(ctx, tx, request.ImportID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit booking import: %w", err)
	}
	return &ImportResult{Disposition: ImportAccepted, Import: imported}, nil
}

func validImportedProviderTime(value string, instant time.Time) bool {
	if value == "" || value != strings.TrimSpace(value) || strings.HasSuffix(value, "Z") || len(value) > 100 {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && parsed.Equal(instant)
}

func (r *ImportRepo) Get(ctx context.Context, importID bookingapi.OperationID) (*Import, error) {
	imported, err := getImportRow(r.pool.QueryRow(ctx, importSelect, importID))
	if !errors.Is(err, pgx.ErrNoRows) {
		return imported, err
	}
	var operationExists bool
	if queryErr := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM booking_operations WHERE operation_id = $1)`, importID).Scan(&operationExists); queryErr != nil {
		return nil, fmt.Errorf("check booking ledger key collision: %w", queryErr)
	}
	if operationExists {
		return nil, &ConflictError{Code: bookingapi.ErrorIdempotencyKeyConflict, ConflictingOperationID: string(importID), ConflictingResourceKind: "operation"}
	}
	return nil, pgx.ErrNoRows
}

func getImportTx(ctx context.Context, tx pgx.Tx, importID bookingapi.OperationID) (*Import, error) {
	return getImportRow(tx.QueryRow(ctx, importSelect+` FOR UPDATE`, importID))
}

const importSelect = `
	SELECT i.import_id::text, i.request_fingerprint, i.service_key, i.provider,
	       i.facility_id, i.facility_uuid, i.facility_slug, i.sport,
	       i.account_id, a.identity, i.court_uuid, i.start_at, i.end_at,
	       i.start_provider, i.end_provider, i.match_id, i.provider_state, i.actor, i.reason, i.revision,
	       i.created_at, i.updated_at
	FROM booking_imports i
	JOIN booking_accounts a ON a.id = i.account_id
	WHERE i.import_id = $1`

func getImportRow(row pgx.Row) (*Import, error) {
	var result Import
	var importID string
	if err := row.Scan(
		&importID, &result.RequestFingerprint, &result.Scope.ServiceKey, &result.Scope.Provider,
		&result.Scope.FacilityID, &result.Scope.FacilityUUID, &result.Scope.FacilitySlug, &result.Scope.Sport,
		&result.AccountID, &result.AccountIdentity, &result.CourtUUID, &result.Start, &result.End,
		&result.StartProvider, &result.EndProvider, &result.MatchID, &result.ProviderState, &result.Actor, &result.Reason, &result.Revision,
		&result.CreatedAt, &result.UpdatedAt,
	); err != nil {
		return nil, err
	}
	result.ImportID = bookingapi.OperationID(importID)
	return &result, nil
}

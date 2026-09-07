// Package bookingapi defines the versioned contract shared by booking and its callers.
// It contains wire DTOs and deterministic validation only; provider execution and
// persistence belong to the booking service.
package bookingapi

import "time"

const (
	ContractName  = "eversports-booking"
	ContractMajor = 2
	ContractMinor = 0
)

type OperationKind string

const (
	OperationCreate OperationKind = "create"
	OperationCancel OperationKind = "cancel"
)

type OperationState string

const (
	StateAccepted         OperationState = "accepted"
	StateRunning          OperationState = "running"
	StateNotDispatched    OperationState = "not_dispatched"
	StateRejectedNoEffect OperationState = "rejected_no_effect"
	StateSucceeded        OperationState = "succeeded"
	StateOutcomeUnknown   OperationState = "outcome_unknown"
)

type OperationPhase string

const (
	PhaseAdmission       OperationPhase = "admission"
	PhaseLogin           OperationPhase = "login"
	PhaseReserve         OperationPhase = "reserve"
	PhasePayment         OperationPhase = "payment"
	PhaseMatchAttachment OperationPhase = "match_attachment"
	PhaseCancellation    OperationPhase = "cancellation"
	PhaseFinalization    OperationPhase = "finalization"
)

type OutcomeCategory string

const (
	OutcomePending            OutcomeCategory = "pending"
	OutcomeSuccess            OutcomeCategory = "success"
	OutcomeSuccessWithWarning OutcomeCategory = "success_with_warning"
	OutcomeNoEffect           OutcomeCategory = "no_effect"
	OutcomeUnknown            OutcomeCategory = "unknown"
)

// Scope is the immutable provider/facility pool addressed by an operation.
// It is supplied by trusted service configuration, not caller-selected metadata.
type Scope struct {
	ServiceKey   string `json:"service_key"`
	Provider     string `json:"provider"`
	FacilityID   string `json:"facility_id"`
	FacilityUUID string `json:"facility_uuid"`
	FacilitySlug string `json:"facility_slug"`
	Sport        string `json:"sport"`
}

type Credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// RiskAuthorization authorizes a new creation operation that may duplicate an
// earlier unknown operation. It never authorizes replay of the earlier key.
type RiskAuthorization struct {
	ReplacesOperationID string `json:"replaces_operation_id"`
	AcceptDuplicateRisk bool   `json:"accept_duplicate_risk"`
	Reason              string `json:"reason"`
}

type CreateRequest struct {
	CourtUUID string             `json:"courtUuid"`
	Start     string             `json:"start"`
	End       string             `json:"end"`
	Email     string             `json:"email"`
	Password  string             `json:"password"`
	Risk      *RiskAuthorization `json:"risk,omitempty"`
}

type CancelRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// ReconcileRequest authorizes one bounded provider read for an existing
// unknown operation. Credentials are used in memory only.
type ReconcileRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// ResolutionRequest explicitly associates a verified provider match with an
// unknown operation. The event key is carried by Idempotency-Key.
type ResolutionRequest struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	MatchID          string `json:"matchId"`
	Email            string `json:"email"`
	Password         string `json:"password"`
	Actor            string `json:"actor"`
	Reason           string `json:"reason"`
}

type ProviderEvidence struct {
	BookingUUID string `json:"booking_uuid,omitempty"`
	BookingID   int    `json:"booking_id,omitempty"`
	PaymentID   int    `json:"payment_id,omitempty"`
	MatchID     string `json:"match_id,omitempty"`
	State       string `json:"state,omitempty"`
}

// OperationStatus is safe to return from the ledger. It deliberately contains
// no provider credentials, cookies, account login, or raw provider response.
type OperationStatus struct {
	OperationID   string            `json:"operation_id"`
	Kind          OperationKind     `json:"kind"`
	State         OperationState    `json:"state"`
	Phase         OperationPhase    `json:"phase"`
	Outcome       OutcomeCategory   `json:"outcome"`
	Code          ErrorCode         `json:"code,omitempty"`
	Revision      int64             `json:"revision"`
	Evidence      *ProviderEvidence `json:"evidence,omitempty"`
	Warnings      []string          `json:"warnings,omitempty"`
	ResultExpired bool              `json:"result_expired,omitempty"`
	StatusURL     string            `json:"status_url"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type ErrorCode string

const (
	ErrorInvalidRequest          ErrorCode = "invalid_request"
	ErrorIdempotencyKeyConflict  ErrorCode = "idempotency_key_conflict"
	ErrorBookingConflict         ErrorCode = "booking_conflict"
	ErrorAccountBlocked          ErrorCode = "account_blocked"
	ErrorCredentialRejected      ErrorCode = "credential_rejected"
	ErrorBookingRejected         ErrorCode = "booking_rejected"
	ErrorDependencyUnavailable   ErrorCode = "dependency_unavailable"
	ErrorOperationOutcomeUnknown ErrorCode = "operation_outcome_unknown"
	ErrorOperationNotFound       ErrorCode = "operation_not_found"
	ErrorOperationResultExpired  ErrorCode = "operation_result_expired"
	ErrorContractUpgradeRequired ErrorCode = "booking_contract_upgrade_required"
)

type ErrorResponse struct {
	Code        ErrorCode `json:"code"`
	Message     string    `json:"message"`
	OperationID string    `json:"operation_id,omitempty"`
	State       string    `json:"state,omitempty"`
	Phase       string    `json:"phase,omitempty"`
	StatusURL   string    `json:"status_url,omitempty"`
}

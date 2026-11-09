package models

import (
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
)

type BookingIntentSource string

type BookingIntentState string

type ManagementBookingOperationState string

type ManagementBookingNotificationKind string

const (
	BookingIntentScheduled BookingIntentSource = "scheduled"
	BookingIntentManual    BookingIntentSource = "manual"
	BookingIntentRecovery  BookingIntentSource = "recovery"

	BookingIntentPlanned   BookingIntentState = "planned"
	BookingIntentActive    BookingIntentState = "active"
	BookingIntentCompleted BookingIntentState = "completed"
	BookingIntentPartial   BookingIntentState = "partial"
	BookingIntentUnknown   BookingIntentState = "unknown"

	ManagementBookingOperationPlanned          ManagementBookingOperationState = "planned"
	ManagementBookingOperationAccepted         ManagementBookingOperationState = "accepted"
	ManagementBookingOperationRunning          ManagementBookingOperationState = "running"
	ManagementBookingOperationNotDispatched    ManagementBookingOperationState = "not_dispatched"
	ManagementBookingOperationSucceeded        ManagementBookingOperationState = "succeeded"
	ManagementBookingOperationRejectedNoEffect ManagementBookingOperationState = "rejected_no_effect"
	ManagementBookingOperationOutcomeUnknown   ManagementBookingOperationState = "outcome_unknown"

	ManagementBookingNotificationOutcomeUnknown    ManagementBookingNotificationKind = "outcome_unknown"
	ManagementBookingNotificationPartial           ManagementBookingNotificationKind = "partial"
	ManagementBookingNotificationPossibleDuplicate ManagementBookingNotificationKind = "possible_duplicate"
	ManagementBookingNotificationSuccessWarning    ManagementBookingNotificationKind = "success_warning"
	ManagementBookingNotificationMissedAction      ManagementBookingNotificationKind = "missed_action"
)

// BookingIntent is management's durable high-level business decision. It is
// distinct from booking's provider-operation ledger and retains requested count
// and origin scope even when no provider operation has completed.
type BookingIntent struct {
	ID                 int64
	RequestKey         string
	RequestFingerprint string
	Action             bookingapi.OperationKind
	Source             BookingIntentSource
	Scope              bookingapi.Scope
	GroupID            int64
	VenueID            int64
	GameID             *int64
	GameDate           time.Time
	GameTime           string
	RequestedCount     int
	Generation         int
	State              BookingIntentState
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ManagementBookingOperation binds one immutable booking operation key to a
// business intent. Passwords and raw provider responses are never stored here.
type ManagementBookingMetrics struct {
	Planned          int64      `json:"planned"`
	Accepted         int64      `json:"accepted"`
	Running          int64      `json:"running"`
	OutcomeUnknown   int64      `json:"outcome_unknown"`
	Succeeded        int64      `json:"succeeded"`
	RejectedNoEffect int64      `json:"rejected_no_effect"`
	RiskAuthorized   int64      `json:"risk_authorized"`
	PendingNotices   int64      `json:"pending_notifications"`
	OldestPendingAt  *time.Time `json:"oldest_pending_at,omitempty"`
}

type ManagementBookingNotification struct {
	ID          int64
	OperationID bookingapi.OperationID
	Revision    int64
	Kind        ManagementBookingNotificationKind
	GroupID     int64
	VenueID     int64
	GameDate    time.Time
	GameTime    string
	CreatedAt   time.Time
}

type ManagementBookingOperation struct {
	OperationID        bookingapi.OperationID
	IntentID           int64
	DedupKey           string
	RequestFingerprint string
	Kind               bookingapi.OperationKind
	CourtUUID          string
	CourtLabel         string
	StartProvider      string
	EndProvider        string
	TargetMatchID      string
	CredentialID       *int64
	ParentOperationID  bookingapi.OperationID
	RootOperationID    bookingapi.OperationID
	RiskReason         string
	State              ManagementBookingOperationState
	Phase              bookingapi.OperationPhase
	Outcome            bookingapi.OutcomeCategory
	Code               bookingapi.ErrorCode
	Warnings           []string
	ResultExpired      bool
	ProviderState      string
	BookingUUID        string
	BookingID          *int64
	PaymentID          *int64
	MatchID            string
	Revision           int64
	CompletedAt        *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

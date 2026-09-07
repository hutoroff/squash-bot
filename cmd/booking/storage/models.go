package storage

import (
	"encoding/json"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
)

type Operation struct {
	OperationID        bookingapi.OperationID
	RequestFingerprint string
	Kind               bookingapi.OperationKind
	Scope              bookingapi.Scope
	AccountID          int64
	AccountIdentity    string
	CourtUUID          string
	Start              *time.Time
	End                *time.Time
	StartProvider      string
	EndProvider        string
	TargetMatchID      string
	State              bookingapi.OperationState
	Phase              bookingapi.OperationPhase
	Outcome            bookingapi.OutcomeCategory
	ExecutorID         string
	Revision           int64
	DeadlineAt         time.Time
	ParentOperationID  string
	RootOperationID    string
	RiskReason         string
	ResultExpiredAt    *time.Time
	CompletedAt        *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type AdmissionDisposition string

const (
	AdmissionAccepted AdmissionDisposition = "accepted"
	AdmissionReplay   AdmissionDisposition = "replay"
	AdmissionResolved AdmissionDisposition = "resolved"
)

type Admission struct {
	Disposition AdmissionDisposition
	Operation   *Operation
}

type ConflictError struct {
	Code                   bookingapi.ErrorCode
	ConflictingOperationID string
}

func (e *ConflictError) Error() string { return string(e.Code) }

type Step struct {
	ID          int64
	OperationID bookingapi.OperationID
	Phase       bookingapi.OperationPhase
	Attempt     int
	Status      string
	Effect      string
	Code        string
	Evidence    json.RawMessage
	ExecutorID  string
	CreatedAt   time.Time
}

type ProviderBooking struct {
	ID                      int64
	CreateOperationID       bookingapi.OperationID
	Provider                string
	FacilityUUID            string
	BookingUUID             string
	BookingID               *int64
	PaymentID               *int64
	MatchID                 string
	PaymentState            string
	BookingState            string
	CancellationOperationID string
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type OperationEvent struct {
	ID                int64
	OperationID       bookingapi.OperationID
	EventKey          bookingapi.OperationID
	EventType         string
	ExpectedRevision  *int64
	ResultingRevision int64
	Reason            string
	Evidence          json.RawMessage
	CreatedAt         time.Time
}

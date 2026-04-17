package models

import "time"

// CourtBooking tracks each individually booked court so cancellation can use
// the same credentials that were used for booking. CanceledAt is set on soft
// delete; nil means the booking is still active.
type CourtBooking struct {
	ID           int64
	VenueID      int64
	GameDate     time.Time
	CourtUUID    string
	CourtLabel   string // name-extracted number, e.g. "7" for "Court 7"
	MatchID      string // Eversports match UUID (used for cancel/get)
	BookingUUID  string
	CredentialID *int64     // nil = booked with env-var credentials
	CanceledAt   *time.Time // nil = active; non-nil = soft-deleted after cancellation
	CreatedAt    time.Time
}

package outbound

import "context"

// BookingSlot is a minimal representation of a court slot returned by the
// booking service GET /api/v1/eversports/matches endpoint.
type BookingSlot struct {
	Court              int          `json:"court"`
	CourtUUID          string       `json:"courtUuid,omitempty"`
	IsUserBookingOwner bool         `json:"isUserBookingOwner"`
	Present            bool         `json:"present"`
	Title              *string      `json:"title"`
	Booking            *int         `json:"booking"`
	Match              *SlotMatchID `json:"match,omitempty"`
}

// BookingCourt is a court returned by the booking service GET /api/v1/eversports/courts endpoint.
type BookingCourt struct {
	ID   string `json:"id"`
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// SlotMatchID holds the UUID needed to cancel a booking.
type SlotMatchID struct {
	UUID string `json:"uuid"`
}

// BookMatchResult is returned by BookMatch.
type BookMatchResult struct {
	BookingUUID string `json:"bookingUuid"`
	BookingID   int    `json:"bookingId"`
	MatchID     string `json:"matchId,omitempty"`
}

// BookingServiceClient is the interface for interacting with the booking service.
type BookingServiceClient interface {
	ListCourts(ctx context.Context, date, login, password string) ([]BookingCourt, error)
	ListMatches(ctx context.Context, date, startTime, endTime string, my bool, login, password string) ([]BookingSlot, error)
	CancelMatch(ctx context.Context, matchUUID, login, password string) error
	BookMatch(ctx context.Context, courtUUID, start, end, login, password string) (*BookMatchResult, error)
}

package models

import "time"

// AutoBookingResult records which courts were successfully booked by AutoBookingJob
// for a given venue on a given game date. Used by BookingReminderJob to create a game
// automatically when booking opens.
type AutoBookingResult struct {
	ID          int64
	VenueID     int64
	GameDate    time.Time
	Courts      string // comma-separated court numbers, e.g. "1,2"
	CourtsCount int
	CreatedAt   time.Time
}

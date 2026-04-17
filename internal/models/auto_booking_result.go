package models

import "time"

// AutoBookingResult records which courts were successfully booked by AutoBookingJob
// for a given venue on a given game date and time slot. Used by BookingReminderJob to
// create a game automatically when booking opens.
type AutoBookingResult struct {
	ID          int64
	VenueID     int64
	GameDate    time.Time
	GameTime    string // HH:MM time slot this booking is for, e.g. "18:00"
	Courts      string // comma-separated court numbers, e.g. "1,2"
	CourtsCount int
	GameID      *int64 // set by BookingReminderJob after game is created; nil = not yet created
	CreatedAt   time.Time
}

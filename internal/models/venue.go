package models

import "time"

// Venue represents a physical location where squash games take place.
// A venue is owned by a specific group and defines available courts and time slots.
type Venue struct {
	ID        int64     `json:"id"`
	GroupID   int64     `json:"group_id"`
	Name      string    `json:"name"`
	Courts    string    `json:"courts"`     // comma-separated court identifiers, e.g. "1,2,3,4,5,6"
	TimeSlots string    `json:"time_slots"` // comma-separated HH:MM times, e.g. "18:00,19:00,20:00"
	Address   string    `json:"address,omitempty"`
	CreatedAt time.Time `json:"created_at"`

	// Scheduling configuration
	GracePeriodHours      int        `json:"grace_period_hours"`                 // hours before game when cancellation window closes (default 24)
	GameDays              string     `json:"game_days"`                          // comma-separated Go time.Weekday ints, e.g. "0,3" = Sunday+Wednesday
	BookingOpensDays      int        `json:"booking_opens_days"`                 // days in advance courts booking becomes available (default 14)
	LastBookingReminderAt *time.Time `json:"last_booking_reminder_at,omitempty"` // dedup: last time booking reminder was sent
	PreferredGameTimes    string     `json:"preferred_game_times"`               // comma-separated HH:MM time slots for auto-booking, each must be one of time_slots (empty = no preference)
	LastAutoBookingAt     *time.Time `json:"last_auto_booking_at,omitempty"`     // dedup: last time auto-booking was performed
	AutoBookingEnabled    bool       `json:"auto_booking_enabled"`               // whether automatic court booking is enabled for this venue
	AutoBookingCourts     string     `json:"auto_booking_courts"`                // ordered comma-separated court IDs for auto-booking; subset of courts (empty = all courts eligible)
}

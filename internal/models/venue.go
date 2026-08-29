package models

import "time"

const PreventiveCancellationFractionDefault = "1/2"

func IsPreventiveCancellationFraction(value string) bool {
	return value == "1/3" || value == "1/2" || value == "2/3"
}

type VenueSport struct {
	Sport           string `json:"sport"`
	Courts          string `json:"courts"`
	PlayersPerCourt *int   `json:"players_per_court,omitempty"`
}

// Venue represents a physical location where games take place.
type Venue struct {
	ID        int64        `json:"id"`
	GroupID   int64        `json:"group_id"`
	Name      string       `json:"name"`
	Courts    string       `json:"courts"` // derived legacy alias for squash courts
	Sports    []VenueSport `json:"sports"`
	TimeSlots string       `json:"time_slots"` // comma-separated HH:MM times, e.g. "18:00,19:00,20:00"
	Address   string       `json:"address,omitempty"`
	CreatedAt time.Time    `json:"created_at"`

	// Scheduling configuration
	GracePeriodHours               int        `json:"grace_period_hours"`                 // hours before game when cancellation window closes (default 24)
	GameDays                       string     `json:"game_days"`                          // comma-separated Go time.Weekday ints, e.g. "0,3" = Sunday+Wednesday
	BookingOpensDays               int        `json:"booking_opens_days"`                 // days in advance courts booking becomes available (default 14)
	PreventiveCancellationFraction string     `json:"preventive_cancellation_fraction"`   // point in the booking-open-to-grace window for preventive cancellation
	LastBookingReminderAt          *time.Time `json:"last_booking_reminder_at,omitempty"` // dedup: last time booking reminder was sent
	PreferredGameTimes             string     `json:"preferred_game_times"`               // comma-separated HH:MM time slots for auto-booking, each must be one of time_slots (empty = no preference)
	LastAutoBookingAt              *time.Time `json:"last_auto_booking_at,omitempty"`     // dedup: last time auto-booking was performed
	AutoBookingEnabled             bool       `json:"auto_booking_enabled"`               // whether automatic court booking is enabled for this venue
	AutoBookingCourts              string     `json:"auto_booking_courts"`                // ordered comma-separated court IDs for auto-booking; subset of courts (empty = all courts eligible)
	AutoBookingCourtsCount         int        `json:"auto_booking_courts_count"`          // how many courts to book per game; 0 = skip booking (default 3)
}

func (v *Venue) CourtsFor(sport string) string {
	for _, venueSport := range v.Sports {
		if venueSport.Sport == sport {
			return venueSport.Courts
		}
	}
	return ""
}

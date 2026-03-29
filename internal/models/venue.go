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
}

package models

import "time"

// VenueCredential holds a single set of Eversports login credentials for a venue.
// The password is never stored in this struct — it is encrypted in the database
// and decrypted only inside the service layer when needed for booking.
type VenueCredential struct {
	ID        int64     `json:"id"`
	VenueID   int64     `json:"venue_id"`
	Login     string    `json:"login"`
	Priority  int       `json:"priority"`
	CreatedAt time.Time `json:"created_at"`
}

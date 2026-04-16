package models

import "time"

// VenueCredential holds a single set of Eversports login credentials for a venue.
// The password is never exposed via JSON — it is encrypted in the database and
// decrypted only inside the service layer when needed for booking.
type VenueCredential struct {
	ID        int64     `json:"id"`
	VenueID   int64     `json:"venue_id"`
	Login     string    `json:"login"`
	Priority  int       `json:"priority"`
	MaxCourts int       `json:"max_courts"`
	CreatedAt time.Time `json:"created_at"`

	// LastErrorAt is set by the auto-booking job when a booking attempt with
	// this credential fails. Credentials with a recent error are skipped.
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`

	// EncryptedPassword is only populated by ListWithPasswordByVenueID (internal
	// scheduler path). It is never serialised to JSON.
	EncryptedPassword string `json:"-"`
}

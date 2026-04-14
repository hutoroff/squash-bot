package eversports

import "time"

// ─── Public domain types ──────────────────────────────────────────────────────

// Booking is the normalised representation of a single court booking returned
// by the service API. Field names and structure are confirmed from a live
// Eversports GraphQL response.
type Booking struct {
	ID    string    `json:"id"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	State string    `json:"state"` // "ACCEPTED", "CANCELLED", etc.
	Sport struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"sport"`
	Venue struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Address string `json:"address"`
		ShortID string `json:"shortId"`
		Slug    string `json:"slug"`
	} `json:"venue"`
	Court struct {
		Name    string `json:"name"`
		Area    string `json:"area"`    // e.g. "INDOOR"
		Surface string `json:"surface"` // e.g. "PARQUET"
	} `json:"court"`
	Price struct {
		Value    int    `json:"value"`    // in minor currency units (e.g. cents)
		Currency string `json:"currency"` // e.g. "EUR"
	} `json:"price"`
}

// Court represents a bookable court at the facility, as returned by the
// booking calendar update endpoint.
type Court struct {
	ID   string `json:"id"`   // numeric court ID, e.g. "77385"
	UUID string `json:"uuid"` // court UUID, e.g. "32ef2369-cf50-427f-8bdf-d380189584e8"
	Name string `json:"name"` // display name, e.g. "Court 1"
}

// Slot represents a single available court time slot returned by the
// /api/slot endpoint.
type Slot struct {
	Date               string     `json:"date"`  // YYYY-MM-DD
	Start              string     `json:"start"` // HHMM, e.g. "1830"
	Court              int        `json:"court"` // numeric court ID
	Title              *string    `json:"title"`
	Present            bool       `json:"present"`
	IsUserBookingOwner bool       `json:"isUserBookingOwner"`
	Booking            *int       `json:"booking"`
	Match              *SlotMatch `json:"match,omitempty"`
}

// SlotMatch is the match metadata embedded in a Slot when the slot is owned
// by the authenticated user.
type SlotMatch struct {
	UUID            string `json:"uuid"`
	IsPublic        bool   `json:"isPublic"`
	MinParticipants *int   `json:"minParticipants"`
	MaxParticipants *int   `json:"maxParticipants"`
	CurParticipants int    `json:"curParticipants"`
}

// BookingResult is returned by CreateBooking.
type BookingResult struct {
	BookingUUID string `json:"bookingUuid"`
	BookingID   int    `json:"bookingId"`
	MatchID     string `json:"matchId,omitempty"`
}

// CancellationResult is returned by CancelMatch. It contains the minimal
// fields from the BallsportMatch fragment in the CancelMatch mutation response.
type CancellationResult struct {
	ID           string `json:"id"`
	State        string `json:"state"`
	RelativeLink string `json:"relativeLink"`
}

// Facility is the public response type for GET /api/v1/eversports/facility.
// Fields excluded from the raw GraphQL response: social, about, amenities,
// cheapestPrice, cheapestTrialProductPrice, faqs, images, location, logo,
// offerings, ratings, reviews, specialPriceTypes, trainers.
type Facility struct {
	ID          string          `json:"id"`
	Slug        string          `json:"slug"`
	Name        string          `json:"name"`
	Rating      float64         `json:"rating"`
	ReviewCount int             `json:"reviewCount"`
	Address     string          `json:"address"`
	HideAddress bool            `json:"hideAddress"`
	Tags        []FacilityTag   `json:"tags"`
	Contact     FacilityContact `json:"contact"`
	Sports      []FacilitySport `json:"sports"`
	City        FacilityCity    `json:"city"`
	Company     FacilityCompany `json:"company"`
}

// FacilityTag is a tag on a Facility.
type FacilityTag struct {
	Name string `json:"name"`
}

// FacilityContact holds the public contact details of a Facility.
type FacilityContact struct {
	Email     string `json:"email"`
	Facebook  string `json:"facebook"`
	Instagram string `json:"instagram"`
	Website   string `json:"website"`
	Telephone string `json:"telephone"`
}

// FacilitySport is a sport offered at a Facility.
type FacilitySport struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// FacilityCity is the city the Facility belongs to.
type FacilityCity struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

// FacilityCompany groups sibling venues under the same company.
type FacilityCompany struct {
	Venues []FacilityVenueRef `json:"venues"`
}

// FacilityVenueRef is a brief reference to a venue within FacilityCompany.
type FacilityVenueRef struct {
	ID       string                `json:"id"`
	Name     string                `json:"name"`
	Slug     string                `json:"slug"`
	Location FacilityVenueLocation `json:"location"`
}

// FacilityVenueLocation is the geographic location of a FacilityVenueRef.
type FacilityVenueLocation struct {
	City    string `json:"city"`
	Zip     string `json:"zip"`
	Country string `json:"country"`
}

// ─── Shared GraphQL types ─────────────────────────────────────────────────────

// gqlRequest is the generic GraphQL request body sent to /api/checkout.
type gqlRequest struct {
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables"`
	Query         string         `json:"query"`
}

// gqlLoginResponse is the GraphQL response envelope for LoginCredentialLogin.
type gqlLoginResponse struct {
	Data struct {
		CredentialLogin struct {
			Typename string `json:"__typename"`
			// AuthResult fields
			APIToken string `json:"apiToken"`
			User     struct {
				ID string `json:"id"`
			} `json:"user"`
			// ExpectedErrors fields
			Errors []struct {
				ID      string `json:"id"`
				Message string `json:"message"`
			} `json:"errors"`
		} `json:"credentialLogin"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

// parseTime parses an RFC3339 timestamp, tolerating the millisecond variant
// ("2026-03-30T18:45:00.000Z") that Eversports sometimes returns.
func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02T15:04:05.999Z07:00", s)
}

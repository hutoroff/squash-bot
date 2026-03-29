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

// ─── Activities response ──────────────────────────────────────────────────────

// activitiesResponse is the JSON envelope returned by GET /api/user/activities.
// The HTML field contains an HTML fragment with one <li> per booking.
type activitiesResponse struct {
	Status string `json:"status"`
	HTML   string `json:"html"`
}

// ─── GraphQL request envelope ─────────────────────────────────────────────────

// gqlRequest is the generic GraphQL request body sent to /api/checkout.
type gqlRequest struct {
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables"`
	Query         string         `json:"query"`
}

// ─── Login types ──────────────────────────────────────────────────────────────

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

// ─── Match (single booking) GraphQL types ─────────────────────────────────────

// rawMatch is the GraphQL response shape for the Match query.
// Field names are confirmed from a live API capture.
type rawMatch struct {
	ID    string `json:"id"`
	Start string `json:"start"` // RFC3339 / ISO-8601
	End   string `json:"end"`
	State string `json:"state"`
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
		Area    string `json:"area"`
		Surface string `json:"surface"`
	} `json:"court"`
	Price struct {
		Value    int    `json:"value"`
		Currency string `json:"currency"`
	} `json:"price"`
}

func (r rawMatch) toBooking() (Booking, error) {
	start, err := parseTime(r.Start)
	if err != nil {
		return Booking{}, err
	}
	end, err := parseTime(r.End)
	if err != nil {
		return Booking{}, err
	}
	b := Booking{
		ID:    r.ID,
		Start: start,
		End:   end,
		State: r.State,
	}
	b.Sport.Name = r.Sport.Name
	b.Sport.Slug = r.Sport.Slug
	b.Venue.ID = r.Venue.ID
	b.Venue.Name = r.Venue.Name
	b.Venue.Address = r.Venue.Address
	b.Venue.ShortID = r.Venue.ShortID
	b.Venue.Slug = r.Venue.Slug
	b.Court.Name = r.Court.Name
	b.Court.Area = r.Court.Area
	b.Court.Surface = r.Court.Surface
	b.Price.Value = r.Price.Value
	b.Price.Currency = r.Price.Currency
	return b, nil
}

// gqlMatchResponse is the GraphQL response envelope for the Match query.
type gqlMatchResponse struct {
	Data struct {
		Match rawMatch `json:"match"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// ─── Slot (court availability) ───────────────────────────────────────────────

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

// rawSlotsResponse is the JSON envelope returned by GET /api/slot.
type rawSlotsResponse struct {
	Slots []Slot `json:"slots"`
}

// ─── CancelMatch types ────────────────────────────────────────────────────────

// CancellationResult is returned by CancelMatch. It contains the minimal
// fields from the BallsportMatch fragment in the CancelMatch mutation response.
type CancellationResult struct {
	ID           string `json:"id"`
	State        string `json:"state"`
	RelativeLink string `json:"relativeLink"`
}

// gqlCancelMatchResponse is the GraphQL response envelope for the CancelMatch mutation.
type gqlCancelMatchResponse struct {
	Data struct {
		CancelMatch struct {
			Typename string `json:"__typename"`
			// BallsportMatch fields
			ID           string `json:"id"`
			State        string `json:"state"`
			RelativeLink string `json:"relativeLink"`
			// ExpectedErrors fields
			Errors []struct {
				ID      string `json:"id"`
				Message string `json:"message"`
				Path    string `json:"path"`
			} `json:"errors"`
		} `json:"cancelMatch"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// ─── Match list GraphQL types ─────────────────────────────────────────────────

// gqlMatchListResponse is the GraphQL response envelope for the list-of-matches
// query. The exact query name and wrapper field are TBD — update once the page-
// load request from /u is captured in browser DevTools.
//
// TODO: replace placeholder field name "matches" with the real one from the capture.
type gqlMatchListResponse struct {
	Data   map[string]any `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

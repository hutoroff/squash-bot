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

// ─── Court ───────────────────────────────────────────────────────────────────

// Court represents a bookable court at the facility, as returned by the
// booking calendar update endpoint.
type Court struct {
	ID   string `json:"id"`   // numeric court ID, e.g. "77385"
	UUID string `json:"uuid"` // court UUID, e.g. "32ef2369-cf50-427f-8bdf-d380189584e8"
	Name string `json:"name"` // display name, e.g. "Court 1"
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

// ─── CreateBooking types ──────────────────────────────────────────────────────

// BookingResult is returned by CreateBooking.
type BookingResult struct {
	BookingUUID string `json:"bookingUuid"`
	BookingID   int    `json:"bookingId"`
	MatchID     string `json:"matchId,omitempty"`
}

// courtBookingRequest is the payload for POST /checkout/api/payableitem/courtbooking.
type courtBookingRequest struct {
	FacilityUUID               string `json:"facilityUuid"`
	CourtUUID                  string `json:"courtUuid"`
	SportUUID                  string `json:"sportUuid"`
	Start                      string `json:"start"`
	End                        string `json:"end"`
	Origin                     string `json:"origin"`
	UseBudgetIfAvailable       bool   `json:"useBudgetIfAvailable"`
	UseSpecialPriceIfAvailable bool   `json:"useSpecialPriceIfAvailable"`
}

// courtBookingResponse is the JSON response from POST /checkout/api/payableitem/courtbooking.
type courtBookingResponse struct {
	BookingUUID string `json:"bookingUuid"`
	BookingID   int    `json:"bookingId"`
	Payment     struct {
		ID int `json:"id"`
	} `json:"payment"`
	Success bool   `json:"success"`
	Status  string `json:"status"`
}

// createMatchRequest is the payload for POST /checkout/api/match/create-from-booking.
// Despite the field name, bookingId holds the booking UUID string (not a numeric ID).
type createMatchRequest struct {
	BookingID string `json:"bookingId"`
}

// createMatchResponse is the JSON response from POST /checkout/api/match/create-from-booking.
type createMatchResponse struct {
	MatchID string `json:"matchId"`
}

// mpFeeRequest is the payload for POST /checkout/api/tracking/getMPFeeForCourtBooking.
type mpFeeRequest struct {
	BookingID int    `json:"bookingId"`
	VenueUUID string `json:"venueUuid"`
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

// ─── Facility (venue profile) ─────────────────────────────────────────────────

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

// gqlFacilityResponse is the GraphQL response envelope for VenueProfileVenueContext.
type gqlFacilityResponse struct {
	Data struct {
		VenueContext struct {
			Venue struct {
				ID          string  `json:"id"`
				Slug        string  `json:"slug"`
				Name        string  `json:"name"`
				Rating      float64 `json:"rating"`
				ReviewCount int     `json:"reviewCount"`
				Address     string  `json:"address"`
				HideAddress bool    `json:"hideAddress"`
				Tags        []struct {
					Name string `json:"name"`
				} `json:"tags"`
				Contact struct {
					Email     string `json:"email"`
					Facebook  string `json:"facebook"`
					Instagram string `json:"instagram"`
					Website   string `json:"website"`
					Telephone string `json:"telephone"`
				} `json:"contact"`
				Sports []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
					Slug string `json:"slug"`
				} `json:"sports"`
				City struct {
					ID   string `json:"id"`
					Slug string `json:"slug"`
				} `json:"city"`
				Company struct {
					Venues []struct {
						ID       string `json:"id"`
						Name     string `json:"name"`
						Slug     string `json:"slug"`
						Location struct {
							City    string `json:"city"`
							Zip     string `json:"zip"`
							Country string `json:"country"`
						} `json:"location"`
					} `json:"venues"`
				} `json:"company"`
			} `json:"venue"`
		} `json:"venueContext"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

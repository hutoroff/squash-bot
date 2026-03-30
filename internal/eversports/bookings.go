package eversports

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ─── Single match (confirmed) ─────────────────────────────────────────────────

// matchQuery is the GraphQL query for a single booking, confirmed from a live
// browser DevTools capture.
const matchQuery = `query Match($matchId: ID!, $first: Int) {
  match(matchId: $matchId) {
    ... on BallsportMatch {
      id
      start
      end
      state
      sport { id name slug __typename }
      venue {
        id address name shortId slug
        location { latitude longitude __typename }
        __typename
      }
      court { name area surface __typename }
      price { value currency __typename }
      __typename
    }
    __typename
  }
}`

// GetMatchByID fetches the details of a single booking by its UUID.
// It logs in automatically if no session is held, and retries once on HTTP 401.
func (c *Client) GetMatchByID(ctx context.Context, matchID string) (*Booking, error) {
	do := func() (*Booking, error) {
		payload := gqlRequest{
			OperationName: "Match",
			Variables:     map[string]any{"matchId": matchID, "first": 3},
			Query:         matchQuery,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("eversports: marshal Match request: %w", err)
		}
		resp, err := c.doAuthed(ctx, http.MethodPost, baseURL+graphqlEndpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("eversports: Match request: %w", err)
		}
		defer resp.Body.Close()

		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("eversports: read Match response: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w", errUnauthorized)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("eversports: Match HTTP %d: %s", resp.StatusCode, string(respBytes))
		}

		var gqlResp gqlMatchResponse
		if err := json.Unmarshal(respBytes, &gqlResp); err != nil {
			return nil, fmt.Errorf("eversports: decode Match response: %w", err)
		}
		if len(gqlResp.Errors) > 0 {
			return nil, fmt.Errorf("eversports: Match graphql error: %s", gqlResp.Errors[0].Message)
		}
		b, err := gqlResp.Data.Match.toBooking()
		if err != nil {
			return nil, fmt.Errorf("eversports: parse Match times: %w", err)
		}
		return &b, nil
	}

	if err := c.EnsureLoggedIn(ctx); err != nil {
		return nil, err
	}
	b, err := do()
	if err != nil && errors.Is(err, errUnauthorized) {
		c.invalidateSession()
		if loginErr := c.EnsureLoggedIn(ctx); loginErr != nil {
			return nil, loginErr
		}
		return do()
	}
	return b, err
}

// ─── Booking list via activities endpoint ─────────────────────────────────────

const activitiesEndpoint = "/api/user/activities"

// GetBookings returns all upcoming bookings for the authenticated user.
// It calls GET /api/user/activities?userId=<numericUserID>&past=false and
// parses the HTML fragment returned in the response body.
// It logs in automatically if no session is held, and retries once on HTTP 401.
// The numeric user ID is fetched lazily from GET /u/self on the first call.
func (c *Client) GetBookings(ctx context.Context) ([]Booking, error) {
	do := func() ([]Booking, error) {
		userID, _ := c.activitiesUserID.Load().(string)
		rawURL := fmt.Sprintf("%s%s?userId=%s&past=false", baseURL, activitiesEndpoint, userID)
		resp, err := c.doAuthed(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, fmt.Errorf("eversports: GetBookings request: %w", err)
		}
		defer resp.Body.Close()

		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("eversports: GetBookings read response: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w", errUnauthorized)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("eversports: GetBookings HTTP %d: %s", resp.StatusCode, string(respBytes))
		}

		var actResp activitiesResponse
		if err := json.Unmarshal(respBytes, &actResp); err != nil {
			return nil, fmt.Errorf("eversports: GetBookings decode response: %w", err)
		}
		if actResp.Status != "success" {
			return nil, fmt.Errorf("eversports: GetBookings status=%q", actResp.Status)
		}

		bookings := parseActivitiesHTML(actResp.HTML, c.logger)
		c.logger.Info("eversports bookings fetched", "count", len(bookings))
		return bookings, nil
	}

	if err := c.EnsureLoggedIn(ctx); err != nil {
		return nil, err
	}
	// Fetch the numeric user ID lazily on the first GetBookings call.
	// Keeping this out of Login() means unrelated endpoints are not affected
	// if /u/self is unavailable.
	if userID, _ := c.activitiesUserID.Load().(string); userID == "" {
		if err := c.fetchSelfUserID(ctx); err != nil {
			return nil, fmt.Errorf("eversports: GetBookings fetch user ID: %w", err)
		}
	}
	bookings, err := do()
	if err != nil && errors.Is(err, errUnauthorized) {
		c.invalidateSession()
		if loginErr := c.EnsureLoggedIn(ctx); loginErr != nil {
			return nil, loginErr
		}
		return do()
	}
	return bookings, err
}

// ─── HTML parsing for activity items ─────────────────────────────────────────

var (
	// liItemRe splits the activities HTML into individual <li> elements.
	liItemRe = regexp.MustCompile(`(?s)<li[^>]*>.*?</li>`)

	// matchLinkRe extracts the match UUID from data-match-relative-link="/match/<uuid>".
	matchLinkRe = regexp.MustCompile(`data-match-relative-link=["']/match/([^"']+)["']`)

	// inputTagRe matches a complete <input ...> or <input .../> element.
	inputTagRe = regexp.MustCompile(`<input[^>]+>`)

	// idAttrRe extracts the value of an id="..." attribute.
	idAttrRe = regexp.MustCompile(`id="([^"]+)"`)

	// valueAttrRe extracts the value of a value="..." attribute.
	valueAttrRe = regexp.MustCompile(`value="([^"]*)"`)

	// courtRe extracts the court name from <span class="session-info-value">...</span>.
	courtRe = regexp.MustCompile(`<span class="session-info-value">\s*([^<]+?)\s*</span>`)
)

// parseActivitiesHTML splits the HTML returned by the activities endpoint into
// individual booking items and converts each one to a Booking.
func parseActivitiesHTML(html string, logger interface{ Warn(string, ...any) }) []Booking {
	items := liItemRe.FindAllString(html, -1)
	bookings := make([]Booking, 0, len(items))
	for _, item := range items {
		b, err := parseActivityItem(item)
		if err != nil {
			logger.Warn("eversports: skipping activity item with parse error", "err", err)
			continue
		}
		bookings = append(bookings, b)
	}
	return bookings
}

// parseActivityItem extracts a Booking from a single <li> HTML fragment.
// The key data fields are stored in hidden <input> elements and HTML attributes.
func parseActivityItem(item string) (Booking, error) {
	// Build a map of id → value from all <input> elements in this item.
	inputs := make(map[string]string)
	for _, tag := range inputTagRe.FindAllString(item, -1) {
		idMatch := idAttrRe.FindStringSubmatch(tag)
		valMatch := valueAttrRe.FindStringSubmatch(tag)
		if len(idMatch) >= 2 && len(valMatch) >= 2 {
			inputs[idMatch[1]] = valMatch[1]
		}
	}

	startStr := inputs["google-calendar-start"]
	endStr := inputs["google-calendar-end"]
	if startStr == "" || endStr == "" {
		return Booking{}, fmt.Errorf("missing calendar time fields")
	}

	// Times are in "YYYYMMDDTHHmmss" format representing the venue's local time.
	// The venue timezone is not included in the activities response, so we store
	// the wall-clock value with a UTC location. The serialised JSON will therefore
	// show e.g. "18:45:00Z" for a 18:45 local booking — the offset is not known
	// at this layer. Callers that need the true UTC instant should call
	// GetMatchByID, which returns proper RFC 3339 timestamps.
	start, err := time.ParseInLocation("20060102T150405", startStr, time.UTC)
	if err != nil {
		return Booking{}, fmt.Errorf("parse start time %q: %w", startStr, err)
	}
	end, err := time.ParseInLocation("20060102T150405", endStr, time.UTC)
	if err != nil {
		return Booking{}, fmt.Errorf("parse end time %q: %w", endStr, err)
	}

	var b Booking
	b.Start = start
	b.End = end
	b.State = "ACCEPTED" // the activities endpoint only surfaces active/upcoming bookings
	b.Sport.Name = inputs["booking-sport"]
	b.Venue.Name = inputs["facility-name"]

	// Match UUID is present for bookings that have a match page; absent otherwise.
	if m := matchLinkRe.FindStringSubmatch(item); len(m) >= 2 {
		b.ID = m[1]
	}

	// Court name (first match in the item).
	if m := courtRe.FindStringSubmatch(item); len(m) >= 2 {
		b.Court.Name = strings.TrimSpace(m[1])
	}

	return b, nil
}

// ─── Court list ───────────────────────────────────────────────────────────────

const calendarUpdateEndpoint = "/api/booking/calendar/update"

var (
	// calendarCourtRowRe matches each court row in the booking calendar HTML.
	calendarCourtRowRe = regexp.MustCompile(`(?s)<tr[^>]+class="court"[^>]*>(.*?)</tr>`)

	// calendarCourtNameRe extracts the court display name from its <div>.
	calendarCourtNameRe = regexp.MustCompile(`<div class="court-name[^"]*">([^<]+)<`)

	// calendarCourtIDRe extracts the numeric court ID from a data-court attribute.
	calendarCourtIDRe = regexp.MustCompile(`data-court="(\d+)"`)

	// calendarCourtUUIDRe extracts the court UUID from a data-court-uuid attribute.
	calendarCourtUUIDRe = regexp.MustCompile(`data-court-uuid="([^"]+)"`)
)

// GetCourts returns the list of courts at the facility by parsing the booking
// calendar HTML. date must be a YYYY-MM-DD string; the calendar endpoint returns
// courts for that specific date (the venue may show different courts on different
// days, e.g. when closed). Courts are deduplicated (the calendar HTML repeats
// each court row once per date in the response).
// It logs in automatically if no session is held, and retries once on HTTP 401.
func (c *Client) GetCourts(ctx context.Context, facilityID, facilitySlug, sportID, sportSlug, sportName, sportUUID, date string) ([]Court, error) {
	do := func() ([]Court, error) {
		form := url.Values{}
		form.Set("facilityId", facilityID)
		form.Set("facilitySlug", facilitySlug)
		form.Set("sport[id]", sportID)
		form.Set("sport[slug]", sportSlug)
		form.Set("sport[name]", sportName)
		form.Set("sport[uuid]", sportUUID)
		form.Set("date", date)
		form.Set("type", "user")

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+calendarUpdateEndpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, fmt.Errorf("eversports: create GetCourts request: %w", err)
		}
		setBrowserHeaders(req)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("eversports: GetCourts request: %w", err)
		}
		defer resp.Body.Close()

		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("eversports: GetCourts read response: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w", errUnauthorized)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("eversports: GetCourts HTTP %d: %s", resp.StatusCode, string(respBytes))
		}

		courts := parseCalendarHTML(string(respBytes))
		if len(courts) == 0 {
			c.logger.Warn("eversports: GetCourts: no courts found in calendar response — facility may be closed on this date, or check EVERSPORTS_FACILITY_ID/FACILITY_SLUG/SPORT_ID/SPORT_UUID config")
		}
		c.logger.Info("eversports courts fetched", "count", len(courts))
		return courts, nil
	}

	if err := c.EnsureLoggedIn(ctx); err != nil {
		return nil, err
	}
	courts, err := do()
	if err != nil && errors.Is(err, errUnauthorized) {
		c.invalidateSession()
		if loginErr := c.EnsureLoggedIn(ctx); loginErr != nil {
			return nil, loginErr
		}
		return do()
	}
	return courts, err
}

// parseCalendarHTML extracts unique courts from the booking calendar HTML
// returned by POST /api/booking/calendar/update.
// Each court row contains a numeric ID, a UUID, and a display name; courts
// are deduplicated by ID because the HTML repeats each court for every date.
func parseCalendarHTML(html string) []Court {
	rows := calendarCourtRowRe.FindAllStringSubmatch(html, -1)
	seen := make(map[string]struct{})
	courts := make([]Court, 0, len(rows))
	for _, row := range rows {
		content := row[1]

		idMatch := calendarCourtIDRe.FindStringSubmatch(content)
		if len(idMatch) < 2 {
			continue
		}
		id := idMatch[1]
		if _, ok := seen[id]; ok {
			continue
		}

		uuidMatch := calendarCourtUUIDRe.FindStringSubmatch(content)
		if len(uuidMatch) < 2 {
			continue
		}

		nameMatch := calendarCourtNameRe.FindStringSubmatch(content)
		if len(nameMatch) < 2 {
			continue
		}

		seen[id] = struct{}{}
		courts = append(courts, Court{
			ID:   id,
			UUID: uuidMatch[1],
			Name: strings.TrimSpace(nameMatch[1]),
		})
	}
	return courts
}

// ─── Court availability slots ────────────────────────────────────────────────

const slotsEndpoint = "/api/slot"

// GetSlots returns available court time slots for the given date.
// facilityID is the numeric Eversports facility ID (e.g. "76443").
// courtIDs are the numeric court IDs to include in the query.
// startDate must be in YYYY-MM-DD format.
// It logs in automatically if no session is held, and retries once on HTTP 401.
func (c *Client) GetSlots(ctx context.Context, facilityID string, courtIDs []string, startDate string) ([]Slot, error) {
	do := func() ([]Slot, error) {
		params := url.Values{}
		params.Set("facilityId", facilityID)
		params.Set("startDate", startDate)
		for _, id := range courtIDs {
			params.Add("courts[]", id)
		}
		rawURL := baseURL + slotsEndpoint + "?" + params.Encode()

		resp, err := c.doAuthed(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, fmt.Errorf("eversports: GetSlots request: %w", err)
		}
		defer resp.Body.Close()

		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("eversports: GetSlots read response: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w", errUnauthorized)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("eversports: GetSlots HTTP %d: %s", resp.StatusCode, string(respBytes))
		}

		var slotsResp rawSlotsResponse
		if err := json.Unmarshal(respBytes, &slotsResp); err != nil {
			return nil, fmt.Errorf("eversports: GetSlots decode response: %w", err)
		}
		c.logger.Info("eversports slots fetched", "count", len(slotsResp.Slots), "date", startDate)
		return slotsResp.Slots, nil
	}

	if err := c.EnsureLoggedIn(ctx); err != nil {
		return nil, err
	}
	slots, err := do()
	if err != nil && errors.Is(err, errUnauthorized) {
		c.invalidateSession()
		if loginErr := c.EnsureLoggedIn(ctx); loginErr != nil {
			return nil, loginErr
		}
		return do()
	}
	return slots, err
}

// ─── CreateBooking ────────────────────────────────────────────────────────────

const (
	courtBookingEndpoint      = "/checkout/api/payableitem/courtbooking"
	createFromBookingEndpoint = "/checkout/api/match/create-from-booking"
)

// CreateBooking creates a court booking via the Eversports checkout flow:
//  1. POST /checkout/api/payableitem/courtbooking  — reserve the slot and create payment
//  2. POST /checkout/api/payment/{id}/pay-offline  — settle with the account budget
//  3. POST /checkout/api/match/create-from-booking — attach a match record (best-effort)
//
// facilityUUID and sportUUID are constants for the venue/sport and should come
// from config. courtUUID identifies the specific court to book.
// start and end are converted to UTC before serialisation.
// The method logs in automatically and retries once on HTTP 401 from step 1 only;
// a 401 received in later steps returns an error without retrying to avoid
// creating duplicate bookings.
func (c *Client) CreateBooking(ctx context.Context, facilityUUID, courtUUID, sportUUID string, start, end time.Time) (*BookingResult, error) {
	// Serialise the three-step checkout flow so that create-from-booking (step 3)
	// always attaches to the booking created in steps 1–2 of the same call.
	c.bookingMu.Lock()
	defer c.bookingMu.Unlock()

	do := func() (*BookingResult, error) {
		// Step 1: reserve the court slot and obtain the payment intent.
		payload := courtBookingRequest{
			FacilityUUID:               facilityUUID,
			CourtUUID:                  courtUUID,
			SportUUID:                  sportUUID,
			Start:                      start.UTC().Format("2006-01-02T15:04:05.000Z"),
			End:                        end.UTC().Format("2006-01-02T15:04:05.000Z"),
			Origin:                     "eversport",
			UseBudgetIfAvailable:       true,
			UseSpecialPriceIfAvailable: true,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("eversports: marshal court booking request: %w", err)
		}
		resp, err := c.doAuthed(ctx, http.MethodPost, baseURL+courtBookingEndpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("eversports: court booking request: %w", err)
		}
		defer resp.Body.Close()
		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("eversports: read court booking response: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			// Only step 1 propagates errUnauthorized — retrying from here is safe
			// because no side-effects have occurred yet.
			return nil, fmt.Errorf("%w", errUnauthorized)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("eversports: court booking HTTP %d: %s", resp.StatusCode, string(respBytes))
		}
		var bookResp courtBookingResponse
		if err := json.Unmarshal(respBytes, &bookResp); err != nil {
			return nil, fmt.Errorf("eversports: decode court booking response: %w", err)
		}
		if !bookResp.Success {
			return nil, fmt.Errorf("eversports: court booking not successful: status=%q body=%s", bookResp.Status, string(respBytes))
		}

		// Step 2: pay with account budget. A 401 here means the session expired
		// mid-flow; we return a plain error rather than wrapping errUnauthorized
		// to prevent retrying from step 1 and creating a duplicate booking.
		payURL := fmt.Sprintf("%s/checkout/api/payment/%d/pay-offline", baseURL, bookResp.Payment.ID)
		payResp, err := c.doAuthed(ctx, http.MethodPost, payURL, bytes.NewReader([]byte("{}")))
		if err != nil {
			return nil, fmt.Errorf("eversports: pay-offline request: %w", err)
		}
		defer payResp.Body.Close()
		payRespBytes, err := io.ReadAll(payResp.Body)
		if err != nil {
			return nil, fmt.Errorf("eversports: read pay-offline response: %w", err)
		}
		if payResp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("eversports: pay-offline HTTP %d: %s", payResp.StatusCode, string(payRespBytes))
		}

		// Step 3: create match record (best-effort — failure does not abort the booking).
		c.createMatchFromBooking(ctx)

		c.logger.Info("eversports booking created", "bookingUuid", bookResp.BookingUUID, "bookingId", bookResp.BookingID)
		return &BookingResult{
			BookingUUID: bookResp.BookingUUID,
			BookingID:   bookResp.BookingID,
		}, nil
	}

	if err := c.EnsureLoggedIn(ctx); err != nil {
		return nil, err
	}
	result, err := do()
	if err != nil && errors.Is(err, errUnauthorized) {
		c.invalidateSession()
		if loginErr := c.EnsureLoggedIn(ctx); loginErr != nil {
			return nil, loginErr
		}
		return do()
	}
	return result, err
}

// createMatchFromBooking fires a best-effort POST to attach a match record to
// the most recently created booking. Errors are logged and ignored.
func (c *Client) createMatchFromBooking(ctx context.Context) {
	resp, err := c.doAuthed(ctx, http.MethodPost, baseURL+createFromBookingEndpoint, bytes.NewReader([]byte{}))
	if err != nil {
		c.logger.Warn("eversports: create-from-booking request failed", "err", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		c.logger.Warn("eversports: create-from-booking HTTP error", "status", resp.StatusCode)
	}
}

// ─── CancelMatch ──────────────────────────────────────────────────────────────

// cancelMatchMutation is the GraphQL mutation captured from a live browser
// DevTools request to cancel a court booking.
const cancelMatchMutation = `mutation CancelMatch($matchId: ID!, $origin: Origin!) {
  cancelMatch(matchId: $matchId, origin: $origin) {
    ... on BallsportMatch {
      id
      state
      relativeLink
      __typename
    }
    ... on ExpectedErrors {
      errors {
        id
        message
        path
        __typename
      }
      __typename
    }
    __typename
  }
}`

// CancelMatch cancels a booking by its UUID.
// It logs in automatically if no session is held, and retries once on HTTP 401.
func (c *Client) CancelMatch(ctx context.Context, matchID string) (*CancellationResult, error) {
	do := func() (*CancellationResult, error) {
		payload := gqlRequest{
			OperationName: "CancelMatch",
			Variables: map[string]any{
				"matchId": matchID,
				"origin":  "ORIGIN_MARKETPLACE",
			},
			Query: cancelMatchMutation,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("eversports: marshal CancelMatch request: %w", err)
		}
		resp, err := c.doAuthed(ctx, http.MethodPost, baseURL+graphqlEndpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("eversports: CancelMatch request: %w", err)
		}
		defer resp.Body.Close()

		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("eversports: read CancelMatch response: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w", errUnauthorized)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("eversports: CancelMatch HTTP %d: %s", resp.StatusCode, string(respBytes))
		}

		var gqlResp gqlCancelMatchResponse
		if err := json.Unmarshal(respBytes, &gqlResp); err != nil {
			return nil, fmt.Errorf("eversports: decode CancelMatch response: %w", err)
		}
		if len(gqlResp.Errors) > 0 {
			return nil, fmt.Errorf("eversports: CancelMatch graphql error: %s", gqlResp.Errors[0].Message)
		}
		cm := gqlResp.Data.CancelMatch
		if len(cm.Errors) > 0 {
			return nil, fmt.Errorf("eversports: CancelMatch error: %s", cm.Errors[0].Message)
		}
		c.logger.Info("eversports match cancelled", "matchId", matchID, "state", cm.State)
		return &CancellationResult{
			ID:           cm.ID,
			State:        cm.State,
			RelativeLink: cm.RelativeLink,
		}, nil
	}

	if err := c.EnsureLoggedIn(ctx); err != nil {
		return nil, err
	}
	result, err := do()
	if err != nil && errors.Is(err, errUnauthorized) {
		c.invalidateSession()
		if loginErr := c.EnsureLoggedIn(ctx); loginErr != nil {
			return nil, loginErr
		}
		return do()
	}
	return result, err
}

// ─── Facility (venue profile) ─────────────────────────────────────────────────

// venueProfileQuery fetches only the public-facing venue fields we expose.
// Excluded: meta, about, amenities, cheapestPrice, cheapestTrialProductPrice,
// faqs, images, location, logo, offerings, ratings, reviews, specialPriceTypes, trainers.
const venueProfileQuery = `query VenueProfileVenueContext($slug: String!) {
  venueContext(slug: $slug) {
    venue {
      id
      slug
      name
      rating
      reviewCount
      address
      hideAddress
      tags { name }
      contact { email facebook instagram website telephone }
      sports { id name slug }
      city { id slug }
      company {
        venues {
          id name slug
          location { city zip country }
        }
      }
    }
  }
}`

// GetFacility fetches the venue profile for the given facility slug.
// It logs in automatically if no session is held, and retries once on HTTP 401.
func (c *Client) GetFacility(ctx context.Context, slug string) (*Facility, error) {
	do := func() (*Facility, error) {
		payload := gqlRequest{
			OperationName: "VenueProfileVenueContext",
			Variables:     map[string]any{"slug": slug},
			Query:         venueProfileQuery,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("eversports: marshal facility request: %w", err)
		}
		resp, err := c.doAuthed(ctx, http.MethodPost, baseURL+graphqlEndpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("eversports: facility request: %w", err)
		}
		defer resp.Body.Close()

		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("eversports: read facility response: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w", errUnauthorized)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("eversports: facility HTTP %d: %s", resp.StatusCode, string(respBytes))
		}
		var gqlResp gqlFacilityResponse
		if err := json.Unmarshal(respBytes, &gqlResp); err != nil {
			return nil, fmt.Errorf("eversports: decode facility response: %w", err)
		}
		if len(gqlResp.Errors) > 0 {
			return nil, fmt.Errorf("eversports: facility GraphQL error: %s", gqlResp.Errors[0].Message)
		}

		v := gqlResp.Data.VenueContext.Venue
		if v.ID == "" {
			return nil, fmt.Errorf("eversports: facility slug %q: %w", slug, ErrNotFound)
		}
		f := &Facility{
			ID:          v.ID,
			Slug:        v.Slug,
			Name:        v.Name,
			Rating:      v.Rating,
			ReviewCount: v.ReviewCount,
			Address:     v.Address,
			HideAddress: v.HideAddress,
			Contact: FacilityContact{
				Email:     v.Contact.Email,
				Facebook:  v.Contact.Facebook,
				Instagram: v.Contact.Instagram,
				Website:   v.Contact.Website,
				Telephone: v.Contact.Telephone,
			},
			City: FacilityCity{ID: v.City.ID, Slug: v.City.Slug},
		}
		for _, t := range v.Tags {
			f.Tags = append(f.Tags, FacilityTag{Name: t.Name})
		}
		for _, s := range v.Sports {
			f.Sports = append(f.Sports, FacilitySport{ID: s.ID, Name: s.Name, Slug: s.Slug})
		}
		for _, cv := range v.Company.Venues {
			f.Company.Venues = append(f.Company.Venues, FacilityVenueRef{
				ID:   cv.ID,
				Name: cv.Name,
				Slug: cv.Slug,
				Location: FacilityVenueLocation{
					City:    cv.Location.City,
					Zip:     cv.Location.Zip,
					Country: cv.Location.Country,
				},
			})
		}

		c.logger.Info("eversports facility fetched", "slug", slug, "name", f.Name)
		return f, nil
	}

	if err := c.EnsureLoggedIn(ctx); err != nil {
		return nil, err
	}
	result, err := do()
	if err != nil && errors.Is(err, errUnauthorized) {
		c.invalidateSession()
		if loginErr := c.EnsureLoggedIn(ctx); loginErr != nil {
			return nil, loginErr
		}
		return do()
	}
	return result, err
}

// parseTime parses an RFC3339 timestamp, tolerating the millisecond variant
// ("2026-03-30T18:45:00.000Z") that Eversports sometimes returns.
func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02T15:04:05.999Z07:00", s)
}

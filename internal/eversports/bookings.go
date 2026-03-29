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
// It calls GET /api/user/activities?userId=<activitiesUserID>&past=false and
// parses the HTML fragment returned in the response body.
// It logs in automatically if no session is held, and retries once on HTTP 401.
func (c *Client) GetBookings(ctx context.Context) ([]Booking, error) {
	do := func() ([]Booking, error) {
		rawURL := fmt.Sprintf("%s%s?userId=%s&past=false", baseURL, activitiesEndpoint, c.activitiesUserID)
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

// ─── Debug-page helper ────────────────────────────────────────────────────────

// PageDebugInfo is returned by FetchPageDebugInfo.
type PageDebugInfo struct {
	URL         string          `json:"url"`
	FinalURL    string          `json:"final_url"`
	Status      int             `json:"status"`
	HasNextData bool            `json:"has_next_data"`
	NextData    json.RawMessage `json:"next_data,omitempty"`
	HTMLSnippet string          `json:"html_snippet,omitempty"`
}

// nextDataRe matches the JSON payload inside <script id="__NEXT_DATA__" ...>...</script>.
var nextDataRe = regexp.MustCompile(`<script[^>]+id="__NEXT_DATA__"[^>]*>([\s\S]*?)</script>`)

// FetchPageDebugInfo fetches the configured bookings page and returns the raw
// __NEXT_DATA__ JSON (or a 2000-char HTML snippet if not found). Use the
// GET /api/v1/eversports/debug-page endpoint to call this interactively.
// It logs in automatically if no session is held, and retries once if the page
// redirects to login (session expired).
func (c *Client) FetchPageDebugInfo(ctx context.Context) (*PageDebugInfo, error) {
	do := func() (*PageDebugInfo, error) {
		targetURL := baseURL + c.bookingsPath
		resp, err := c.doAuthedPage(ctx, targetURL)
		if err != nil {
			return nil, fmt.Errorf("eversports: fetch page: %w", err)
		}
		defer resp.Body.Close()

		info := &PageDebugInfo{
			URL:      targetURL,
			FinalURL: resp.Request.URL.String(),
			Status:   resp.StatusCode,
		}
		pageBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("eversports: read page body: %w", err)
		}
		matches := nextDataRe.FindSubmatch(pageBytes)
		if len(matches) < 2 {
			snippet := pageBytes
			if len(snippet) > 2000 {
				snippet = snippet[:2000]
			}
			info.HTMLSnippet = string(snippet)
			return info, nil
		}
		info.HasNextData = true
		info.NextData = json.RawMessage(matches[1])
		return info, nil
	}

	if err := c.EnsureLoggedIn(ctx); err != nil {
		return nil, err
	}
	info, err := do()
	// doAuthedPage wraps errUnauthorized when it detects a redirect to login.
	if err != nil && errors.Is(err, errUnauthorized) {
		c.invalidateSession()
		if loginErr := c.EnsureLoggedIn(ctx); loginErr != nil {
			return nil, loginErr
		}
		return do()
	}
	return info, err
}

// parseTime parses an RFC3339 timestamp, tolerating the millisecond variant
// ("2026-03-30T18:45:00.000Z") that Eversports sometimes returns.
func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02T15:04:05.999Z07:00", s)
}

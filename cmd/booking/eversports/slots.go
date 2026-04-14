package eversports

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// ─── Court list ───────────────────────────────────────────────────────────────

const calendarUpdateEndpoint = "/api/booking/calendar/update"

var (
	// calendarCourtRowRe matches each court row in the booking calendar HTML.
	// The outer <tr> tag is captured separately so court attributes on the <tr>
	// itself (e.g. data-court) are searched alongside the inner content.
	calendarCourtRowRe = regexp.MustCompile(`(?s)(<tr[^>]+class="court"[^>]*>)(.*?)</tr>`)

	// calendarCourtNameRe extracts the court display name from its <div>.
	calendarCourtNameRe = regexp.MustCompile(`<div class="court-name[^"]*">([^<]+)<`)

	// calendarCourtIDRe extracts the numeric court ID from a data-court attribute.
	calendarCourtIDRe = regexp.MustCompile(`data-court="(\d+)"`)

	// calendarCourtUUIDRe extracts the court UUID from a data-court-uuid attribute.
	calendarCourtUUIDRe = regexp.MustCompile(`data-court-uuid="([^"]+)"`)
)

// rawSlotsResponse is the JSON envelope returned by GET /api/slot.
type rawSlotsResponse struct {
	Slots []Slot `json:"slots"`
}

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
		for _, court := range courts {
			c.logger.Debug("eversports court detail",
				"id", court.ID,
				"uuid", court.UUID,
				"name", court.Name,
			)
		}
		return courts, nil
	}

	return withAuth(ctx, c, do)
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
		// row[1] = the opening <tr ...> tag, row[2] = inner content
		trTag := row[1]
		content := row[2]

		idMatch := calendarCourtIDRe.FindStringSubmatch(trTag + content)
		if len(idMatch) < 2 {
			continue
		}
		id := idMatch[1]
		if _, ok := seen[id]; ok {
			continue
		}

		uuidMatch := calendarCourtUUIDRe.FindStringSubmatch(trTag + content)
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

		// At DEBUG level, decode each slot as a raw map and log ALL fields so we
		// can see fields not captured by the typed Slot struct (e.g. price, type,
		// bookable flags, membership restrictions).
		if c.logger.Enabled(ctx, slog.LevelDebug) {
			var rawResp struct {
				Slots []json.RawMessage `json:"slots"`
			}
			if err := json.Unmarshal(respBytes, &rawResp); err == nil {
				logged := 0
				for _, raw := range rawResp.Slots {
					var m map[string]json.RawMessage
					if err := json.Unmarshal(raw, &m); err != nil {
						continue
					}
					// Log the first slot and any slot whose start is "2045" (target time).
					startVal, _ := m["start"]
					isTarget := string(startVal) == `"2045"`
					if logged == 0 || isTarget {
						c.logger.Debug("eversports: raw slot", "json", string(raw))
						logged++
						if logged >= 6 { // cap: 1 sample + up to 5 target-time slots
							break
						}
					}
				}
			}
		}

		return slotsResp.Slots, nil
	}

	return withAuth(ctx, c, do)
}

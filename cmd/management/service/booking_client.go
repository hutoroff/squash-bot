package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BookingSlot is a minimal representation of a court slot returned by the
// booking service GET /api/v1/eversports/matches endpoint.
type BookingSlot struct {
	Court              int          `json:"court"`               // numeric Eversports court ID
	CourtUUID          string       `json:"courtUuid,omitempty"` // court UUID required for booking
	VenueUUID          string       `json:"venueUuid,omitempty"` // per-court venue UUID (may differ from EVERSPORTS_FACILITY_UUID)
	IsUserBookingOwner bool         `json:"isUserBookingOwner"`
	Booking            *int         `json:"booking"` // nil = not booked by anyone
	Match              *SlotMatchID `json:"match,omitempty"`
}

// SlotMatchID holds the UUID needed to cancel a booking.
type SlotMatchID struct {
	UUID string `json:"uuid"`
}

// BookMatchResult is returned by BookMatch.
type BookMatchResult struct {
	BookingUUID string `json:"bookingUuid"`
	BookingID   int    `json:"bookingId"`
	MatchID     string `json:"matchId,omitempty"`
}

// BookingServiceClient is the interface for interacting with the booking service.
// It is an interface to allow test doubles.
type BookingServiceClient interface {
	// ListMatches returns slots for the given date filtered to the time window.
	// When my=true, only slots owned by the service user are returned.
	// When my=false, slots not owned by the service user are returned (includes unbooked).
	ListMatches(ctx context.Context, date, startTime, endTime string, my bool) ([]BookingSlot, error)
	// CancelMatch cancels the booking identified by the match UUID.
	CancelMatch(ctx context.Context, matchUUID string) error
	// BookMatch creates a new court booking. courtUUID is the Eversports court UUID,
	// venueUUID is the per-court venue UUID (empty = use booking service default),
	// start and end are RFC 3339 timestamps. Returns booking result on success.
	BookMatch(ctx context.Context, courtUUID, venueUUID, start, end string) (*BookMatchResult, error)
}

// httpBookingClient is the production implementation of BookingServiceClient.
type httpBookingClient struct {
	baseURL    string
	apiSecret  string
	httpClient *http.Client
}

// NewHTTPBookingClient creates a BookingServiceClient backed by the booking service HTTP API.
// baseURL is e.g. "http://booking:8081".
func NewHTTPBookingClient(baseURL, apiSecret string) BookingServiceClient {
	return &httpBookingClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiSecret: apiSecret,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *httpBookingClient) ListMatches(ctx context.Context, date, startTime, endTime string, my bool) ([]BookingSlot, error) {
	myStr := "false"
	if my {
		myStr = "true"
	}
	url := fmt.Sprintf("%s/api/v1/eversports/matches?date=%s&startTime=%s&endTime=%s&my=%s",
		c.baseURL, date, startTime, endTime, myStr)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiSecret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list matches: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list matches: unexpected status %d", resp.StatusCode)
	}

	var slots []BookingSlot
	if err := json.NewDecoder(resp.Body).Decode(&slots); err != nil {
		return nil, fmt.Errorf("decode slots: %w", err)
	}
	return slots, nil
}

func (c *httpBookingClient) CancelMatch(ctx context.Context, matchUUID string) error {
	url := fmt.Sprintf("%s/api/v1/eversports/matches/%s", c.baseURL, matchUUID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiSecret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cancel match %s: %w", matchUUID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cancel match %s: unexpected status %d", matchUUID, resp.StatusCode)
	}
	return nil
}

func (c *httpBookingClient) BookMatch(ctx context.Context, courtUUID, venueUUID, start, end string) (*BookMatchResult, error) {
	url := fmt.Sprintf("%s/api/v1/eversports/matches", c.baseURL)

	reqBody := map[string]string{
		"courtUuid": courtUUID,
		"start":     start,
		"end":       end,
	}
	if venueUUID != "" {
		reqBody["venueUuid"] = venueUUID
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiSecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("book match: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("book match: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result BookMatchResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode booking result: %w", err)
	}
	return &result, nil
}

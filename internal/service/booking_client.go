package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// BookingSlot is a minimal representation of a court slot returned by the
// sports-booking-service GET /api/v1/eversports/matches endpoint.
type BookingSlot struct {
	Court              int          `json:"court"` // numeric Eversports court ID
	IsUserBookingOwner bool         `json:"isUserBookingOwner"`
	Match              *SlotMatchID `json:"match,omitempty"`
}

// SlotMatchID holds the UUID needed to cancel a booking.
type SlotMatchID struct {
	UUID string `json:"uuid"`
}

// BookingServiceClient is the interface for cancelling courts via the sports-booking-service.
// It is an interface to allow test doubles.
type BookingServiceClient interface {
	// ListMatches returns slots for the given date filtered to the time window and owned by the service user.
	ListMatches(ctx context.Context, date, startTime, endTime string) ([]BookingSlot, error)
	// CancelMatch cancels the booking identified by the match UUID.
	CancelMatch(ctx context.Context, matchUUID string) error
}

// httpBookingClient is the production implementation of BookingServiceClient.
type httpBookingClient struct {
	baseURL    string
	apiSecret  string
	httpClient *http.Client
}

// NewHTTPBookingClient creates a BookingServiceClient backed by the sports-booking-service HTTP API.
// baseURL is e.g. "http://sports-booking-service:8081".
func NewHTTPBookingClient(baseURL, apiSecret string) BookingServiceClient {
	return &httpBookingClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiSecret: apiSecret,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *httpBookingClient) ListMatches(ctx context.Context, date, startTime, endTime string) ([]BookingSlot, error) {
	url := fmt.Sprintf("%s/api/v1/eversports/matches?date=%s&startTime=%s&endTime=%s&my=true",
		c.baseURL, date, startTime, endTime)

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

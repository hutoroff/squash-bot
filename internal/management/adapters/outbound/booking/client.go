package booking

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"
)

type httpBookingClient struct {
	baseURL    string
	apiSecret  string
	httpClient *http.Client
}

// NewHTTPBookingClient creates a BookingServiceClient backed by the booking service HTTP API.
func NewHTTPBookingClient(baseURL, apiSecret string) outbound.BookingServiceClient {
	return &httpBookingClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiSecret: apiSecret,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *httpBookingClient) ListCourts(ctx context.Context, date, login, password string) ([]outbound.BookingCourt, error) {
	url := fmt.Sprintf("%s/api/v1/eversports/courts?date=%s", c.baseURL, date)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiSecret)
	if login != "" {
		req.Header.Set("X-Eversports-Email", login)
		req.Header.Set("X-Eversports-Password", password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list courts: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list courts: status %d: %s", resp.StatusCode, string(respBody))
	}

	var courts []outbound.BookingCourt
	if err := json.Unmarshal(respBody, &courts); err != nil {
		return nil, fmt.Errorf("decode courts: %w", err)
	}
	return courts, nil
}

func (c *httpBookingClient) ListMatches(ctx context.Context, date, startTime, endTime string, my bool, login, password string) ([]outbound.BookingSlot, error) {
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
	if login != "" {
		req.Header.Set("X-Eversports-Email", login)
		req.Header.Set("X-Eversports-Password", password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list matches: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list matches: unexpected status %d", resp.StatusCode)
	}

	var slots []outbound.BookingSlot
	if err := json.NewDecoder(resp.Body).Decode(&slots); err != nil {
		return nil, fmt.Errorf("decode slots: %w", err)
	}
	return slots, nil
}

func (c *httpBookingClient) CancelMatch(ctx context.Context, matchUUID, login, password string) error {
	url := fmt.Sprintf("%s/api/v1/eversports/matches/%s", c.baseURL, matchUUID)

	var body io.Reader
	if login != "" {
		payload := map[string]string{"email": login, "password": password}
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal cancel request: %w", err)
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiSecret)
	if login != "" {
		req.Header.Set("Content-Type", "application/json")
	}

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

func (c *httpBookingClient) BookMatch(ctx context.Context, courtUUID, start, end, login, password string) (*outbound.BookMatchResult, error) {
	url := fmt.Sprintf("%s/api/v1/eversports/matches", c.baseURL)

	payload := map[string]string{
		"courtUuid": courtUUID,
		"start":     start,
		"end":       end,
	}
	if login != "" {
		payload["email"] = login
		payload["password"] = password
	}
	body, err := json.Marshal(payload)
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

	var result outbound.BookMatchResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode booking result: %w", err)
	}
	return &result, nil
}

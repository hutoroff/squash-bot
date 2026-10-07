package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/hutoroff/squash-bot/internal/observability"
)

// BookingSlot is a minimal representation of a court slot returned by the
// booking service GET /api/v1/eversports/matches endpoint.
type BookingSlot struct {
	Court              int          `json:"court"`               // numeric Eversports court ID
	CourtUUID          string       `json:"courtUuid,omitempty"` // court UUID required for booking
	IsUserBookingOwner bool         `json:"isUserBookingOwner"`
	Present            bool         `json:"present"`
	Title              *string      `json:"title"`
	Booking            *int         `json:"booking"` // nil = not booked by anyone
	Match              *SlotMatchID `json:"match,omitempty"`
}

// BookingCourt is a court returned by the booking service GET /api/v1/eversports/courts endpoint.
type BookingCourt struct {
	ID   string `json:"id"`   // numeric court ID as string, e.g. "77385"
	UUID string `json:"uuid"` // court UUID used in booking requests
	Name string `json:"name"` // display name, e.g. "Court 1"
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
	// ListCourts returns all courts at the facility for the given date.
	// login and password select a per-credential Eversports session; empty strings
	// fall back to the booking service's default (env-var) account.
	ListCourts(ctx context.Context, date, login, password string) ([]BookingCourt, error)
	// ListMatches returns slots for the given date filtered to the time window.
	// Pass empty startTime/endTime for no time filter.
	// When my=true, only slots owned by the service user are returned.
	// When my=false, all slots (including unbooked) are returned.
	// login and password select a per-credential Eversports session; empty strings
	// fall back to the booking service's default (env-var) account.
	ListMatches(ctx context.Context, date, startTime, endTime string, my bool, login, password string) ([]BookingSlot, error)
	// CancelMatch cancels the booking identified by the match UUID.
	// login and password select a per-credential Eversports session; empty strings
	// fall back to the booking service's default (env-var) account.
	CancelMatch(ctx context.Context, matchUUID, login, password string) error
	// BookMatch creates a new court booking. courtUUID is the Eversports court UUID,
	// start and end are RFC 3339 timestamps. login and password are the Eversports
	// account credentials to use for this booking. Returns booking result on success.
	BookMatch(ctx context.Context, courtUUID, start, end, login, password string) (*BookMatchResult, error)
}

// ReliableBookingServiceClient exposes the keyed v2 mutation and ledger-read
// contract. Callers must durably allocate operationID before invoking it.
type ReliableBookingServiceClient interface {
	VerifyBinding(context.Context, bookingapi.Scope) error
	SubmitCreateOperation(context.Context, bookingapi.OperationID, bookingapi.CreateRequest) (*bookingapi.OperationStatus, error)
	SubmitCancelOperation(context.Context, bookingapi.OperationID, string, bookingapi.CancelRequest) (*bookingapi.OperationStatus, error)
	GetOperationStatus(context.Context, bookingapi.OperationID) (*bookingapi.OperationStatus, error)
	ReconcileOperation(context.Context, bookingapi.OperationID, bookingapi.ReconcileRequest) (*bookingapi.OperationStatus, error)
	ResolveOperation(context.Context, bookingapi.OperationID, bookingapi.OperationID, bookingapi.ResolutionRequest) (*bookingapi.OperationStatus, error)
}

// BookingContractError is a typed v2 rejection. It contains only the bounded,
// sanitized booking contract response, never provider response bodies.
type BookingContractError struct {
	HTTPStatus  int
	Code        bookingapi.ErrorCode
	OperationID bookingapi.OperationID
	State       string
	Phase       string
	StatusURL   string
}

func (e *BookingContractError) Error() string {
	return fmt.Sprintf("booking v2 request rejected: status=%d code=%s operation_id=%s", e.HTTPStatus, e.Code, e.OperationID)
}

// httpBookingClient is the production implementation of BookingServiceClient.
type httpBookingClient struct {
	baseURL      string
	apiSecret    string
	httpClient   *http.Client
	tracker      *discovery.EndpointTracker
	reliableOnly bool
	resolver     discovery.Resolver
	requirement  discovery.Requirement
	metrics      *observability.DependencyMetrics
}

// NewHTTPBookingClient creates a BookingServiceClient backed by the booking service HTTP API.
// baseURL is e.g. "http://booking:8081".
func NewHTTPBookingClient(baseURL, apiSecret string) BookingServiceClient {
	return newHTTPBookingClient(baseURL, apiSecret)
}

// NewReliableHTTPBookingClient creates a keyed v2 booking client. Unlike the
// legacy interface, it has no keyless mutation methods.
func NewReliableHTTPBookingClient(baseURL, apiSecret string) ReliableBookingServiceClient {
	client := newHTTPBookingClient(baseURL, apiSecret)
	client.reliableOnly = true
	return client
}

// NewReliableBookingReadClient exposes facility reads through the existing
// interface while failing closed if a keyless mutation path is reached.
func NewReliableBookingReadClient(baseURL, apiSecret string) BookingServiceClient {
	client := newHTTPBookingClient(baseURL, apiSecret)
	client.reliableOnly = true
	return client
}

func NewDiscoveredReliableHTTPBookingClient(resolver discovery.Resolver, requirement discovery.Requirement, apiSecret string) ReliableBookingServiceClient {
	client := newHTTPBookingClient("", apiSecret)
	client.reliableOnly = true
	client.resolver = resolver
	client.requirement = requirement
	return client
}

func NewDiscoveredReliableBookingReadClient(resolver discovery.Resolver, requirement discovery.Requirement, apiSecret string) BookingServiceClient {
	client := newHTTPBookingClient("", apiSecret)
	client.reliableOnly = true
	client.resolver = resolver
	client.requirement = requirement
	return client
}

func newHTTPBookingClient(baseURL, apiSecret string) *httpBookingClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &httpBookingClient{
		baseURL: strings.TrimRight(baseURL, "/"), apiSecret: apiSecret,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		tracker:    discovery.NewEndpointTracker(transport.CloseIdleConnections),
	}
}

func (c *httpBookingClient) ListCourts(ctx context.Context, date, login, password string) ([]BookingCourt, error) {
	baseURL, err := c.resolveBaseURL(ctx)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/api/v2/eversports/courts?date=%s", baseURL, date)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiSecret)
	if login != "" {
		req.Header.Set("X-Eversports-Email", login)
		req.Header.Set("X-Eversports-Password", password)
	}

	resp, err := c.do(req)
	if err != nil {
		c.invalidateResolvedEndpoint()
		return nil, fmt.Errorf("list courts: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list courts: status %d: %s", resp.StatusCode, string(respBody))
	}

	var courts []BookingCourt
	if err := json.Unmarshal(respBody, &courts); err != nil {
		return nil, fmt.Errorf("decode courts: %w", err)
	}
	return courts, nil
}

func (c *httpBookingClient) ListMatches(ctx context.Context, date, startTime, endTime string, my bool, login, password string) ([]BookingSlot, error) {
	baseURL, err := c.resolveBaseURL(ctx)
	if err != nil {
		return nil, err
	}
	myStr := "false"
	if my {
		myStr = "true"
	}
	url := fmt.Sprintf("%s/api/v2/eversports/matches?date=%s&startTime=%s&endTime=%s&my=%s",
		baseURL, date, startTime, endTime, myStr)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiSecret)
	if login != "" {
		req.Header.Set("X-Eversports-Email", login)
		req.Header.Set("X-Eversports-Password", password)
	}

	resp, err := c.do(req)
	if err != nil {
		c.invalidateResolvedEndpoint()
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

func (c *httpBookingClient) CancelMatch(ctx context.Context, matchUUID, login, password string) error {
	if c.reliableOnly {
		return &BookingContractError{HTTPStatus: http.StatusConflict, Code: bookingapi.ErrorContractUpgradeRequired}
	}
	baseURL, err := c.resolveBaseURL(ctx)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/v1/eversports/matches/%s", baseURL, matchUUID)

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

	resp, err := c.do(req)
	if err != nil {
		c.invalidateResolvedEndpoint()
		return fmt.Errorf("cancel match %s: %w", matchUUID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cancel match %s: unexpected status %d", matchUUID, resp.StatusCode)
	}
	return nil
}

func (c *httpBookingClient) VerifyBinding(ctx context.Context, expected bookingapi.Scope) error {
	baseURL, err := c.resolveBaseURL(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v2/eversports/descriptor", nil)
	if err != nil {
		return fmt.Errorf("build booking v2 descriptor request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiSecret)
	req.Header.Set("Accept", "application/json")
	response, err := c.do(req)
	if err != nil {
		c.invalidateResolvedEndpoint()
		return fmt.Errorf("read booking v2 descriptor: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxReliableBookingResponseBytes+1))
	if err != nil || len(body) > maxReliableBookingResponseBytes {
		return fmt.Errorf("read booking v2 descriptor: invalid bounded response")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("booking v2 descriptor unavailable: status %d", response.StatusCode)
	}
	var descriptor bookingapi.Descriptor
	if err := json.Unmarshal(body, &descriptor); err != nil {
		return fmt.Errorf("decode booking v2 descriptor: %w", err)
	}
	if descriptor.Contract != bookingapi.ContractName || descriptor.ContractMajor != bookingapi.ContractMajor || descriptor.Scope != expected {
		return fmt.Errorf("booking v2 descriptor does not match the durable binding")
	}
	required := map[string]bool{"create": false, "cancel": false, "status": false, "reconcile": false, "resolve": false}
	for _, capability := range descriptor.Capabilities {
		if _, ok := required[capability]; ok {
			required[capability] = true
		}
	}
	for _, present := range required {
		if !present {
			return fmt.Errorf("booking v2 descriptor is missing required capabilities")
		}
	}
	return nil
}

func (c *httpBookingClient) SubmitCreateOperation(ctx context.Context, operationID bookingapi.OperationID, request bookingapi.CreateRequest) (*bookingapi.OperationStatus, error) {
	return c.doReliableOperation(ctx, http.MethodPost, "/api/v2/eversports/matches", operationID, operationID, request)
}

func (c *httpBookingClient) SubmitCancelOperation(ctx context.Context, operationID bookingapi.OperationID, matchID string, request bookingapi.CancelRequest) (*bookingapi.OperationStatus, error) {
	if strings.TrimSpace(matchID) == "" {
		return nil, fmt.Errorf("booking v2 cancellation match ID is required")
	}
	return c.doReliableOperation(ctx, http.MethodDelete, "/api/v2/eversports/matches/"+url.PathEscape(matchID), operationID, operationID, request)
}

func (c *httpBookingClient) GetOperationStatus(ctx context.Context, operationID bookingapi.OperationID) (*bookingapi.OperationStatus, error) {
	status, err := c.doReliableOperation(ctx, http.MethodGet, "/api/v2/eversports/operations/"+url.PathEscape(string(operationID)), "", operationID, nil)
	return requireBookingOperationID(status, operationID, err)
}

func (c *httpBookingClient) ReconcileOperation(ctx context.Context, operationID bookingapi.OperationID, request bookingapi.ReconcileRequest) (*bookingapi.OperationStatus, error) {
	status, err := c.doReliableOperation(ctx, http.MethodPost, "/api/v2/eversports/operations/"+url.PathEscape(string(operationID))+"/reconcile", "", operationID, request)
	return requireBookingOperationID(status, operationID, err)
}

func (c *httpBookingClient) ResolveOperation(ctx context.Context, operationID, eventKey bookingapi.OperationID, request bookingapi.ResolutionRequest) (*bookingapi.OperationStatus, error) {
	status, err := c.doReliableOperation(ctx, http.MethodPost, "/api/v2/eversports/operations/"+url.PathEscape(string(operationID))+"/resolution", eventKey, operationID, request)
	return requireBookingOperationID(status, operationID, err)
}

func requireBookingOperationID(status *bookingapi.OperationStatus, expected bookingapi.OperationID, err error) (*bookingapi.OperationStatus, error) {
	if err != nil || status == nil {
		return status, err
	}
	if status.OperationID != string(expected) {
		return nil, fmt.Errorf("booking v2 status operation ID mismatch")
	}
	return status, nil
}

const maxReliableBookingResponseBytes = 1 << 20

func (c *httpBookingClient) doReliableOperation(ctx context.Context, method, path string, idempotencyKey, expectedOperationID bookingapi.OperationID, payload any) (*bookingapi.OperationStatus, error) {
	baseURL, err := c.resolveBaseURL(ctx)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode booking v2 request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("build booking v2 request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiSecret)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", string(idempotencyKey))
	}

	response, err := c.do(req)
	if err != nil {
		c.invalidateResolvedEndpoint()
		return nil, fmt.Errorf("booking v2 request: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxReliableBookingResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read booking v2 response: %w", err)
	}
	if len(responseBody) > maxReliableBookingResponseBytes {
		return nil, fmt.Errorf("booking v2 response exceeds %d bytes", maxReliableBookingResponseBytes)
	}

	var status bookingapi.OperationStatus
	if err := json.Unmarshal(responseBody, &status); err == nil && status.OperationID != "" && status.State != "" && status.Kind != "" && status.Outcome != "" {
		if !reliableStatusHTTPCode(response.StatusCode) || !validReliableOperationStatus(&status) {
			return nil, fmt.Errorf("decode booking v2 status: invalid contract state")
		}
		parsedID, parseErr := bookingapi.ParseOperationID(status.OperationID)
		if parseErr != nil {
			return nil, fmt.Errorf("decode booking v2 status: invalid operation ID")
		}
		if expectedOperationID != "" && parsedID != expectedOperationID {
			return nil, fmt.Errorf("booking v2 status operation ID mismatch")
		}
		return &status, nil
	}

	var contractError bookingapi.ErrorResponse
	if err := json.Unmarshal(responseBody, &contractError); err == nil && contractError.Code != "" {
		operationID := bookingapi.OperationID(contractError.OperationID)
		if contractError.OperationID != "" {
			parsedID, parseErr := bookingapi.ParseOperationID(contractError.OperationID)
			if parseErr != nil {
				return nil, fmt.Errorf("decode booking v2 error: invalid operation ID")
			}
			operationID = parsedID
		}
		return nil, &BookingContractError{
			HTTPStatus: response.StatusCode, Code: contractError.Code, OperationID: operationID,
			State: contractError.State, Phase: contractError.Phase, StatusURL: contractError.StatusURL,
		}
	}
	return nil, fmt.Errorf("decode booking v2 response: status %d contained no recognized contract document", response.StatusCode)
}

func (c *httpBookingClient) do(request *http.Request) (*http.Response, error) {
	started := time.Now()
	response, err := c.httpClient.Do(request)
	if c.metrics != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		c.metrics.Observe("booking", status, err, started)
	}
	return response, err
}

func (c *httpBookingClient) invalidateResolvedEndpoint() {
	if c.resolver != nil {
		discovery.Invalidate(c.resolver, c.requirement)
	}
}

// SetDependencyMetrics records bounded logical booking dependency outcomes.
func (c *httpBookingClient) SetDependencyMetrics(metrics *observability.DependencyMetrics) {
	c.metrics = metrics
}

func (c *httpBookingClient) resolveBaseURL(ctx context.Context) (string, error) {
	if c.resolver == nil {
		return c.baseURL, nil
	}
	started := time.Now()
	endpoint, err := discovery.ResolveOne(ctx, c.resolver, c.requirement)
	if err != nil {
		err = fmt.Errorf("resolve booking service: %w", err)
		if c.metrics != nil {
			c.metrics.Observe("booking", 0, err, started)
		}
		return "", err
	}
	if endpoint.API == "" {
		return "", fmt.Errorf("resolve booking service: %w", discovery.ErrNoEndpoint)
	}
	baseURL := strings.TrimRight(endpoint.API, "/")
	c.tracker.Observe(baseURL)
	return baseURL, nil
}

func reliableStatusHTTPCode(status int) bool {
	switch status {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusGone:
		return true
	default:
		return false
	}
}

func validReliableOperationStatus(status *bookingapi.OperationStatus) bool {
	if status == nil || (status.Kind != bookingapi.OperationCreate && status.Kind != bookingapi.OperationCancel) {
		return false
	}
	switch status.Phase {
	case bookingapi.PhaseAdmission, bookingapi.PhaseLogin, bookingapi.PhaseReserve, bookingapi.PhasePayment,
		bookingapi.PhaseMatchAttachment, bookingapi.PhaseCancellation, bookingapi.PhaseFinalization:
	default:
		return false
	}
	switch status.State {
	case bookingapi.StateAccepted, bookingapi.StateRunning:
		return status.Outcome == bookingapi.OutcomePending
	case bookingapi.StateNotDispatched, bookingapi.StateRejectedNoEffect:
		return status.Outcome == bookingapi.OutcomeNoEffect
	case bookingapi.StateSucceeded:
		return status.Outcome == bookingapi.OutcomeSuccess || status.Outcome == bookingapi.OutcomeSuccessWithWarning
	case bookingapi.StateOutcomeUnknown:
		return status.Outcome == bookingapi.OutcomeUnknown
	default:
		return false
	}
}

func (c *httpBookingClient) BookMatch(ctx context.Context, courtUUID, start, end, login, password string) (*BookMatchResult, error) {
	if c.reliableOnly {
		return nil, &BookingContractError{HTTPStatus: http.StatusConflict, Code: bookingapi.ErrorContractUpgradeRequired}
	}
	baseURL, err := c.resolveBaseURL(ctx)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/api/v1/eversports/matches", baseURL)

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

	resp, err := c.do(req)
	if err != nil {
		c.invalidateResolvedEndpoint()
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

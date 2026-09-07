package eversports

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	courtBookingEndpoint      = "/checkout/api/payableitem/courtbooking"
	mpFeeEndpoint             = "/checkout/api/tracking/getMPFeeForCourtBooking"
	trackCheckoutEndpoint     = "/checkout/api/tracking/trackCheckoutCompleted"
	createFromBookingEndpoint = "/checkout/api/match/create-from-booking"
)

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

type courtBookingResponse struct {
	BookingUUID string `json:"bookingUuid"`
	BookingID   int    `json:"bookingId"`
	Payment     struct {
		ID int `json:"id"`
	} `json:"payment"`
	Success bool   `json:"success"`
	Status  string `json:"status"`
}

type createMatchRequest struct {
	BookingID string `json:"bookingId"`
}

type createMatchResponse struct {
	MatchID string `json:"matchId"`
}

type mpFeeRequest struct {
	BookingID int    `json:"bookingId"`
	VenueUUID string `json:"venueUuid"`
}

// ReserveCourt performs exactly one reservation request. It assumes a session
// was established by EnsureLoggedIn and does not retry or dispatch payment.
func (c *Client) ReserveCourt(ctx context.Context, facilityUUID, courtUUID, sportUUID string, start, end time.Time) (*ReservationResult, error) {
	return c.ReserveCourtProviderTime(ctx, facilityUUID, courtUUID, sportUUID,
		start.Format("2006-01-02T15:04:05.000Z07:00"), end.Format("2006-01-02T15:04:05.000Z07:00"))
}

// ReserveCourtProviderTime is ReserveCourt with already validated provider
// timestamps. It preserves the facility-local offsets stored at admission.
func (c *Client) ReserveCourtProviderTime(ctx context.Context, facilityUUID, courtUUID, sportUUID, startProvider, endProvider string) (*ReservationResult, error) {
	payload := courtBookingRequest{
		FacilityUUID:               facilityUUID,
		CourtUUID:                  courtUUID,
		SportUUID:                  sportUUID,
		Start:                      startProvider,
		End:                        endProvider,
		Origin:                     "eversport",
		UseBudgetIfAvailable:       true,
		UseSpecialPriceIfAvailable: true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, mutationFailure(MutationPhaseReserve, MutationNoEffect, MutationCodeRequestBuildFailed, false,
			fmt.Errorf("marshal court booking request: %w", err))
	}
	c.logger.Debug("eversports: booking reserve request",
		"facilityUuid", payload.FacilityUUID,
		"courtUuid", payload.CourtUUID,
		"sportUuid", payload.SportUUID,
		"start", payload.Start,
		"end", payload.End,
	)
	resp, err := c.doAuthedMutation(ctx, http.MethodPost, c.baseURL+courtBookingEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, mutationFailure(MutationPhaseReserve, MutationUnknown, MutationCodeTransportError, false,
			fmt.Errorf("court booking request: %w", err))
	}
	defer resp.Body.Close()
	respBytes, err := readProviderResponse(resp.Body)
	if err != nil {
		return nil, mutationFailure(MutationPhaseReserve, MutationUnknown, MutationCodeResponseReadFailed, false,
			fmt.Errorf("read court booking response: %w", err))
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, mutationFailure(MutationPhaseReserve, MutationNoEffect, MutationCodeAuthenticationExpired, true, errUnauthorized)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, mutationFailure(MutationPhaseReserve, MutationUnknown, MutationCodeHTTPError, false,
			fmt.Errorf("court booking HTTP %d: %s", resp.StatusCode, bodySnippet(respBytes)))
	}
	// Captured reserve behavior establishes the login/challenge HTML response
	// as a no-effect authentication boundary. No other HTML/redirect rule is
	// used to authorize a mutation retry.
	if err := htmlAuthError("court booking", respBytes); err != nil {
		return nil, mutationFailure(MutationPhaseReserve, MutationNoEffect, MutationCodeAuthenticationExpired, true, err)
	}
	var bookResp courtBookingResponse
	if err := json.Unmarshal(respBytes, &bookResp); err != nil {
		return nil, mutationFailure(MutationPhaseReserve, MutationUnknown, MutationCodeInvalidResponse, false,
			fmt.Errorf("decode court booking response: %w (body: %s)", err, bodySnippet(respBytes)))
	}
	if !bookResp.Success {
		return nil, mutationFailure(MutationPhaseReserve, MutationUnknown, MutationCodeProviderRejected, false,
			fmt.Errorf("court booking not successful: status=%q", bookResp.Status))
	}
	if err := validateCourtBookingResponse(bookResp); err != nil {
		return nil, mutationFailure(MutationPhaseReserve, MutationUnknown, MutationCodeInvalidResponse, false, err)
	}
	return &ReservationResult{
		BookingUUID: bookResp.BookingUUID,
		BookingID:   bookResp.BookingID,
		PaymentID:   bookResp.Payment.ID,
		Status:      bookResp.Status,
	}, nil
}

// PayOffline performs exactly one payment request and never restarts reserve.
func (c *Client) PayOffline(ctx context.Context, paymentID int) error {
	payURL := fmt.Sprintf("%s/checkout/api/payment/%d/pay-offline", c.baseURL, paymentID)
	resp, err := c.doAuthedMutation(ctx, http.MethodPost, payURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return mutationFailure(MutationPhasePayment, MutationUnknown, MutationCodeTransportError, false,
			fmt.Errorf("pay-offline request: %w", err))
	}
	defer resp.Body.Close()
	respBytes, err := readProviderResponse(resp.Body)
	if err != nil {
		return mutationFailure(MutationPhasePayment, MutationUnknown, MutationCodeResponseReadFailed, false,
			fmt.Errorf("read pay-offline response: %w", err))
	}
	if resp.StatusCode != http.StatusOK {
		return mutationFailure(MutationPhasePayment, MutationUnknown, MutationCodeHTTPError, false,
			fmt.Errorf("pay-offline HTTP %d: %s", resp.StatusCode, bodySnippet(respBytes)))
	}
	if err := validateOfflinePaymentResponse(respBytes); err != nil {
		return mutationFailure(MutationPhasePayment, MutationUnknown, MutationCodeInvalidResponse, false, err)
	}
	return nil
}

// AttachMatch performs exactly one session-dependent match attachment request.
// It does not retry; callers decide whether its failure is an optional warning.
func (c *Client) AttachMatch(ctx context.Context, bookingUUID string) (string, error) {
	body, err := json.Marshal(createMatchRequest{BookingID: bookingUUID})
	if err != nil {
		return "", mutationFailure(MutationPhaseMatchAttachment, MutationNoEffect, MutationCodeRequestBuildFailed, false, err)
	}
	resp, err := c.doAuthedMutation(ctx, http.MethodPost, c.baseURL+createFromBookingEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", mutationFailure(MutationPhaseMatchAttachment, MutationUnknown, MutationCodeTransportError, false, err)
	}
	defer resp.Body.Close()
	respBytes, err := readProviderResponse(resp.Body)
	if err != nil {
		return "", mutationFailure(MutationPhaseMatchAttachment, MutationUnknown, MutationCodeResponseReadFailed, false, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", mutationFailure(MutationPhaseMatchAttachment, MutationUnknown, MutationCodeHTTPError, false,
			fmt.Errorf("create-from-booking HTTP %d: %s", resp.StatusCode, bodySnippet(respBytes)))
	}
	if isHTMLResponse(respBytes) {
		return "", mutationFailure(MutationPhaseMatchAttachment, MutationUnknown, MutationCodeInvalidResponse, false,
			fmt.Errorf("create-from-booking returned HTML: %s", bodySnippet(respBytes)))
	}
	var matchResp createMatchResponse
	if err := json.Unmarshal(respBytes, &matchResp); err != nil || !validProviderIdentifier(matchResp.MatchID) {
		return "", mutationFailure(MutationPhaseMatchAttachment, MutationUnknown, MutationCodeInvalidResponse, false,
			fmt.Errorf("create-from-booking returned invalid response: %s", bodySnippet(respBytes)))
	}
	return matchResp.MatchID, nil
}

// CompleteBookingTracking performs the two bounded best-effort tracking calls.
// It never retries and its result does not alter confirmed booking evidence.
func (c *Client) CompleteBookingTracking(ctx context.Context, bookingID int, facilityUUID string) error {
	return errors.Join(c.reportMPFee(ctx, bookingID, facilityUUID), c.trackCheckoutCompleted(ctx))
}

// CreateBooking preserves the legacy checkout behavior using the phase-aware
// single-dispatch primitives. Durable v2 orchestration journals each primitive
// separately and does not call this convenience method.
func (c *Client) CreateBooking(ctx context.Context, facilityUUID, courtUUID, sportUUID string, start, end time.Time) (*BookingResult, error) {
	c.bookingMu.Lock()
	defer c.bookingMu.Unlock()

	if err := c.EnsureLoggedIn(ctx); err != nil {
		return nil, loginMutationFailure(err)
	}
	reservation, err := c.ReserveCourt(ctx, facilityUUID, courtUUID, sportUUID, start, end)
	var mutationErr *MutationError
	if errors.As(err, &mutationErr) && mutationErr.RetrySafe && errors.Is(err, errUnauthorized) {
		if loginErr := c.Reauthenticate(ctx); loginErr != nil {
			return nil, loginMutationFailure(loginErr)
		}
		reservation, err = c.ReserveCourt(ctx, facilityUUID, courtUUID, sportUUID, start, end)
	}
	if err != nil {
		return nil, err
	}
	if err := c.PayOffline(ctx, reservation.PaymentID); err != nil {
		return nil, err
	}
	matchID, attachErr := c.AttachMatch(ctx, reservation.BookingUUID)
	if attachErr != nil {
		c.logger.Warn("eversports: createMatchFromBooking failed", "err", attachErr)
		matchID = ""
	}
	if trackingErr := c.CompleteBookingTracking(ctx, reservation.BookingID, facilityUUID); trackingErr != nil {
		c.logger.Warn("eversports: booking tracking failed", "err", trackingErr)
	}
	c.logger.Info("eversports booking created", "bookingUuid", reservation.BookingUUID, "bookingId", reservation.BookingID, "matchId", matchID)
	return &BookingResult{BookingUUID: reservation.BookingUUID, BookingID: reservation.BookingID, MatchID: matchID}, nil
}

func validateCourtBookingResponse(response courtBookingResponse) error {
	if !validProviderIdentifier(response.BookingUUID) || response.BookingID <= 0 || response.Payment.ID <= 0 {
		return fmt.Errorf("court booking response missing or invalid booking/payment identifiers")
	}
	return nil
}

func validProviderIdentifier(value string) bool {
	if value == "" || len(value) > 200 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validateOfflinePaymentResponse(body []byte) error {
	if isHTMLResponse(body) {
		return fmt.Errorf("pay-offline returned HTML instead of JSON: %s", bodySnippet(body))
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(body, &response); err != nil || response == nil {
		return fmt.Errorf("pay-offline returned invalid JSON object: %s", bodySnippet(body))
	}
	if raw, ok := response["success"]; ok {
		var success bool
		if err := json.Unmarshal(raw, &success); err != nil || !success {
			return fmt.Errorf("pay-offline did not report success")
		}
	}
	if raw, ok := response["errors"]; ok && string(raw) != "null" && string(raw) != "[]" {
		return fmt.Errorf("pay-offline returned provider errors")
	}
	return nil
}

func (c *Client) reportMPFee(ctx context.Context, bookingID int, venueUUID string) error {
	payload := mpFeeRequest{BookingID: bookingID, VenueUUID: venueUUID}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal marketplace-fee tracking request: %w", err)
	}
	resp, err := c.doAuthedMutation(ctx, http.MethodPost, c.baseURL+mpFeeEndpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("marketplace-fee tracking request: %w", err)
	}
	defer resp.Body.Close()
	if _, err := readProviderResponse(resp.Body); err != nil {
		return fmt.Errorf("read marketplace-fee tracking response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("marketplace-fee tracking HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) trackCheckoutCompleted(ctx context.Context) error {
	resp, err := c.doAuthedMutation(ctx, http.MethodPost, c.baseURL+trackCheckoutEndpoint, bytes.NewReader(nil))
	if err != nil {
		return fmt.Errorf("checkout tracking request: %w", err)
	}
	defer resp.Body.Close()
	if _, err := readProviderResponse(resp.Body); err != nil {
		return fmt.Errorf("read checkout tracking response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("checkout tracking HTTP %d", resp.StatusCode)
	}
	return nil
}

func isCFChallenge(body []byte) bool {
	return bytes.Contains(body, []byte("challenge-platform/scripts/jsd/main.js"))
}

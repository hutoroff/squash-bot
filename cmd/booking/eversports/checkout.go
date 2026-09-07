package eversports

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ─── CreateBooking ────────────────────────────────────────────────────────────

const (
	courtBookingEndpoint      = "/checkout/api/payableitem/courtbooking"
	mpFeeEndpoint             = "/checkout/api/tracking/getMPFeeForCourtBooking"
	trackCheckoutEndpoint     = "/checkout/api/tracking/trackCheckoutCompleted"
	createFromBookingEndpoint = "/checkout/api/match/create-from-booking"
)

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

// CreateBooking creates a court booking via the Eversports checkout flow:
//  1. POST /checkout/api/payableitem/courtbooking               — reserve the slot and create payment
//  2. POST /checkout/api/payment/{id}/pay-offline               — settle with the account budget
//  3. POST /checkout/api/match/create-from-booking              — attach a match record (best-effort, before tracking)
//  4. POST /checkout/api/tracking/getMPFeeForCourtBooking       — report MP fee (best-effort)
//  5. POST /checkout/api/tracking/trackCheckoutCompleted        — track checkout completion (best-effort)
//
// facilityUUID and sportUUID are constants for the venue/sport and should come
// from config. courtUUID identifies the specific court to book.
// start and end retain their supplied local offset during serialisation.
// The method logs in automatically and retries once on a positively identified
// no-effect authentication response from step 1 only;
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
			Start:                      start.Format("2006-01-02T15:04:05.000Z07:00"),
			End:                        end.Format("2006-01-02T15:04:05.000Z07:00"),
			Origin:                     "eversport",
			UseBudgetIfAvailable:       true,
			UseSpecialPriceIfAvailable: true,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, mutationFailure(MutationPhaseReserve, MutationNoEffect, "request_build_failed", false,
				fmt.Errorf("marshal court booking request: %w", err))
		}
		c.logger.Debug("eversports: booking step 1 request",
			"facilityUuid", payload.FacilityUUID,
			"courtUuid", payload.CourtUUID,
			"sportUuid", payload.SportUUID,
			"start", payload.Start,
			"end", payload.End,
		)
		resp, err := c.doAuthed(ctx, http.MethodPost, c.baseURL+courtBookingEndpoint, bytes.NewReader(body))
		if err != nil {
			return nil, mutationFailure(MutationPhaseReserve, MutationUnknown, "transport_error", false,
				fmt.Errorf("court booking request: %w", err))
		}
		defer resp.Body.Close()
		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, mutationFailure(MutationPhaseReserve, MutationUnknown, "response_read_failed", false,
				fmt.Errorf("read court booking response: %w", err))
		}
		if resp.StatusCode == http.StatusUnauthorized {
			// Existing captured behavior establishes this as a no-effect auth
			// boundary. Only this typed result may restart the checkout.
			return nil, mutationFailure(MutationPhaseReserve, MutationNoEffect, "authentication_expired", true, errUnauthorized)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, mutationFailure(MutationPhaseReserve, MutationUnknown, "http_error", false,
				fmt.Errorf("court booking HTTP %d: %s", resp.StatusCode, bodySnippet(respBytes)))
		}
		// Existing captured behavior treats the login/challenge HTML returned at
		// reserve as a no-effect authentication boundary.
		if err := htmlAuthError("court booking", respBytes); err != nil {
			return nil, mutationFailure(MutationPhaseReserve, MutationNoEffect, "authentication_expired", true, err)
		}
		var bookResp courtBookingResponse
		if err := json.Unmarshal(respBytes, &bookResp); err != nil {
			return nil, mutationFailure(MutationPhaseReserve, MutationUnknown, "invalid_response", false,
				fmt.Errorf("decode court booking response: %w (body: %s)", err, bodySnippet(respBytes)))
		}
		if !bookResp.Success {
			// success=false is a rejection-shaped response, but the undocumented
			// provider contract does not prove that no reservation was created.
			return nil, mutationFailure(MutationPhaseReserve, MutationUnknown, "provider_rejected", false,
				fmt.Errorf("court booking not successful: status=%q", bookResp.Status))
		}
		if err := validateCourtBookingResponse(bookResp); err != nil {
			return nil, mutationFailure(MutationPhaseReserve, MutationUnknown, "invalid_response", false, err)
		}

		// Step 2: pay with account budget. A 401 here means the session expired
		// mid-flow; we return a plain error rather than wrapping errUnauthorized
		// to prevent retrying from step 1 and creating a duplicate booking.
		payURL := fmt.Sprintf("%s/checkout/api/payment/%d/pay-offline", c.baseURL, bookResp.Payment.ID)
		payResp, err := c.doAuthed(ctx, http.MethodPost, payURL, bytes.NewReader([]byte("{}")))
		if err != nil {
			return nil, mutationFailure(MutationPhasePayment, MutationUnknown, "transport_error", false,
				fmt.Errorf("pay-offline request: %w", err))
		}
		defer payResp.Body.Close()
		payRespBytes, err := io.ReadAll(payResp.Body)
		if err != nil {
			return nil, mutationFailure(MutationPhasePayment, MutationUnknown, "response_read_failed", false,
				fmt.Errorf("read pay-offline response: %w", err))
		}
		if payResp.StatusCode != http.StatusOK {
			return nil, mutationFailure(MutationPhasePayment, MutationUnknown, "http_error", false,
				fmt.Errorf("pay-offline HTTP %d: %s", payResp.StatusCode, bodySnippet(payRespBytes)))
		}
		if err := validateOfflinePaymentResponse(payRespBytes); err != nil {
			return nil, mutationFailure(MutationPhasePayment, MutationUnknown, "invalid_response", false, err)
		}

		// Step 3: create match record (best-effort — failure does not abort the booking).
		// Must run before tracking calls: it relies on server-side checkout session
		// state ("most recently created booking") which tracking may finalize.
		matchID := c.createMatchFromBooking(ctx, bookResp.BookingUUID)

		// Step 4: report MP fee (best-effort — failure does not abort the booking).
		c.reportMPFee(ctx, bookResp.BookingID, facilityUUID)

		// Step 5: track checkout completion (best-effort — failure does not abort the booking).
		c.trackCheckoutCompleted(ctx)

		c.logger.Info("eversports booking created", "bookingUuid", bookResp.BookingUUID, "bookingId", bookResp.BookingID, "matchId", matchID)
		return &BookingResult{
			BookingUUID: bookResp.BookingUUID,
			BookingID:   bookResp.BookingID,
			MatchID:     matchID,
		}, nil
	}

	if err := c.EnsureLoggedIn(ctx); err != nil {
		return nil, loginMutationFailure(err)
	}
	result, err := do()
	var mutationErr *MutationError
	if errors.As(err, &mutationErr) && mutationErr.RetrySafe && errors.Is(err, errUnauthorized) {
		c.invalidateSession()
		if loginErr := c.EnsureLoggedIn(ctx); loginErr != nil {
			return nil, loginMutationFailure(loginErr)
		}
		return do()
	}
	return result, err
}

func validateCourtBookingResponse(response courtBookingResponse) error {
	if response.BookingUUID == "" || response.BookingID <= 0 || response.Payment.ID <= 0 {
		return fmt.Errorf("court booking response missing required booking/payment identifiers")
	}
	return nil
}

// validateOfflinePaymentResponse validates the response shape currently
// observed by local fixtures. A valid JSON object is an acknowledgment from the
// endpoint; it is not a documented provider idempotency or settlement contract.
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

// reportMPFee fires a best-effort POST to the tracking endpoint after a
// successful booking. Errors are logged and ignored.
func (c *Client) reportMPFee(ctx context.Context, bookingID int, venueUUID string) {
	payload := mpFeeRequest{BookingID: bookingID, VenueUUID: venueUUID}
	body, err := json.Marshal(payload)
	if err != nil {
		c.logger.Warn("eversports: booking step 4 marshal failed", "err", err)
		return
	}
	resp, err := c.doAuthed(ctx, http.MethodPost, c.baseURL+mpFeeEndpoint, bytes.NewReader(body))
	if err != nil {
		c.logger.Warn("eversports: reportMPFee failed", "err", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
}

// trackCheckoutCompleted fires a best-effort POST to the checkout tracking
// endpoint after payment is settled. Errors are logged and ignored.
func (c *Client) trackCheckoutCompleted(ctx context.Context) {
	resp, err := c.doAuthed(ctx, http.MethodPost, c.baseURL+trackCheckoutEndpoint, bytes.NewReader([]byte{}))
	if err != nil {
		c.logger.Warn("eversports: trackCheckoutCompleted failed", "err", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
}

// isCFChallenge reports whether the response body looks like a Cloudflare JS
// challenge page (HTTP 200 with HTML) rather than a real API response.
func isCFChallenge(body []byte) bool {
	return bytes.Contains(body, []byte("challenge-platform/scripts/jsd/main.js"))
}

// createMatchFromBooking fires a best-effort POST to attach a match record to
// the booking identified by bookingUUID. Returns the created matchId, or empty
// string on failure.
func (c *Client) createMatchFromBooking(ctx context.Context, bookingUUID string) string {
	body, _ := json.Marshal(createMatchRequest{BookingID: bookingUUID})
	resp, err := c.doAuthed(ctx, http.MethodPost, c.baseURL+createFromBookingEndpoint, bytes.NewReader(body))
	if err != nil {
		c.logger.Warn("eversports: createMatchFromBooking failed", "err", err)
		return ""
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)
	if isCFChallenge(respBytes) {
		c.logger.Warn("eversports: createMatchFromBooking Cloudflare challenge detected")
		return ""
	}
	var matchResp createMatchResponse
	if err := json.Unmarshal(respBytes, &matchResp); err != nil {
		c.logger.Warn("eversports: booking step 3 parse response failed", "err", err)
		return ""
	}
	return matchResp.MatchID
}

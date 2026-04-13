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

// reportMPFee fires a best-effort POST to the tracking endpoint after a
// successful booking. Errors are logged and ignored.
func (c *Client) reportMPFee(ctx context.Context, bookingID int, venueUUID string) {
	payload := mpFeeRequest{BookingID: bookingID, VenueUUID: venueUUID}
	body, err := json.Marshal(payload)
	if err != nil {
		c.logger.Warn("eversports: booking step 4 marshal failed", "err", err)
		return
	}
	resp, err := c.doAuthed(ctx, http.MethodPost, baseURL+mpFeeEndpoint, bytes.NewReader(body))
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
	resp, err := c.doAuthed(ctx, http.MethodPost, baseURL+trackCheckoutEndpoint, bytes.NewReader([]byte{}))
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
	resp, err := c.doAuthed(ctx, http.MethodPost, baseURL+createFromBookingEndpoint, bytes.NewReader(body))
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

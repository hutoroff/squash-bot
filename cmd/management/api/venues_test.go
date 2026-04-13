package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestHandler returns a Handler with only the logger set.
// The venueService is intentionally nil — all tests in this file exercise
// validation paths that return before the service is ever called.
func newTestHandler() *Handler {
	return &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// ── createVenue validation ────────────────────────────────────────────────────

// TestCreateVenue_NegativeGracePeriodHours is a regression test for the bug
// where the API accepted negative integers for grace_period_hours because the
// check only defaulted the zero value, allowing any negative number through.
func TestCreateVenue_NegativeGracePeriodHours(t *testing.T) {
	body := `{"group_id":1,"name":"Court A","courts":"1,2","grace_period_hours":-1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/venues", strings.NewReader(body))
	w := httptest.NewRecorder()

	newTestHandler().createVenue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("grace_period_hours=-1: want 400, got %d", w.Code)
	}
}

// TestCreateVenue_NegativeBookingOpensDays is a regression test for the same
// bug applied to booking_opens_days.
func TestCreateVenue_NegativeBookingOpensDays(t *testing.T) {
	body := `{"group_id":1,"name":"Court A","courts":"1,2","booking_opens_days":-5}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/venues", strings.NewReader(body))
	w := httptest.NewRecorder()

	newTestHandler().createVenue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("booking_opens_days=-5: want 400, got %d", w.Code)
	}
}

// ── updateVenue validation ────────────────────────────────────────────────────

// TestUpdateVenue_NegativeGracePeriodHours verifies the same rejection for the
// update endpoint.
func TestUpdateVenue_NegativeGracePeriodHours(t *testing.T) {
	body := `{"group_id":1,"name":"Court A","courts":"1,2","grace_period_hours":-3}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/venues/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	newTestHandler().updateVenue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("grace_period_hours=-3: want 400, got %d", w.Code)
	}
}

// TestUpdateVenue_NegativeBookingOpensDays verifies the same rejection for the
// update endpoint.
func TestUpdateVenue_NegativeBookingOpensDays(t *testing.T) {
	body := `{"group_id":1,"name":"Court A","courts":"1,2","booking_opens_days":-14}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/venues/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	newTestHandler().updateVenue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("booking_opens_days=-14: want 400, got %d", w.Code)
	}
}

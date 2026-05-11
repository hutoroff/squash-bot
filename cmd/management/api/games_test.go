package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── bookCourts validation ─────────────────────────────────────────────────────

// TestBookCourts_MissingCount verifies that omitting count (or count=0) returns 400.
func TestBookCourts_MissingCount(t *testing.T) {
	body := `{"group_id":1,"actor_telegram_id":123}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/games/1/book-courts", strings.NewReader(body))
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.bookCourts(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("count=0: want 400, got %d", w.Code)
	}
}

// TestBookCourts_NegativeCount verifies that count<0 returns 400.
func TestBookCourts_NegativeCount(t *testing.T) {
	body := `{"count":-1,"group_id":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/games/1/book-courts", strings.NewReader(body))
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.bookCourts(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("count=-1: want 400, got %d", w.Code)
	}
}

// TestBookCourts_InvalidGameID verifies that a non-integer path value returns 400.
func TestBookCourts_InvalidGameID(t *testing.T) {
	body := `{"count":1,"group_id":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/games/abc/book-courts", strings.NewReader(body))
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()

	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.bookCourts(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid game id: want 400, got %d", w.Code)
	}
}

// ── bookingReadiness validation ───────────────────────────────────────────────

// TestBookingReadiness_NoCredService verifies that a handler with nil venueCredService
// always returns ready=false with reason "credentials_not_configured".
func TestBookingReadiness_NoCredService(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/venues/7/booking-readiness?group_id=1", nil)
	req.SetPathValue("id", "7")
	w := httptest.NewRecorder()

	h := &Handler{
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		venueCredService: nil, // no credentials service configured
	}
	h.bookingReadiness(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "credentials_not_configured") {
		t.Errorf("expected credentials_not_configured reason, got: %s", body)
	}
	if !strings.Contains(body, `"ready":false`) {
		t.Errorf("expected ready=false, got: %s", body)
	}
}

// TestBookingReadiness_InvalidVenueID verifies a non-integer venue id returns 400.
func TestBookingReadiness_InvalidVenueID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/venues/xyz/booking-readiness", nil)
	req.SetPathValue("id", "xyz")
	w := httptest.NewRecorder()

	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.bookingReadiness(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid venue id: want 400, got %d", w.Code)
	}
}

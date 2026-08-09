package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/service"
)

// ── bookCourts validation ─────────────────────────────────────────────────────

// TestBookCourts_MissingCount verifies that omitting count (or count=0) returns 400.
func TestBookCourts_MissingCount(t *testing.T) {
	body := `{"group_id":1,"actor_user_id":123}`
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
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
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

// ── checkGameAccess (F1/IDOR guard) ──────────────────────────────────────────

// newTestGameService builds a *service.GameService backed by gameRepo, with
// every other dependency nil. Safe because PlayerCanAccessGame only touches gameRepo.
func newTestGameService(gameRepo service.GameRepository) *service.GameService {
	return service.NewGameService(
		gameRepo, nil, nil, nil, nil, nil, nil, time.UTC,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil, nil, nil, nil, 14,
	)
}

func TestCheckGameAccess_InvalidGameID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/games/abc/access?user_id=1", nil)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()

	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.checkGameAccess(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid game id: want 400, got %d", w.Code)
	}
}

func TestCheckGameAccess_MissingUserID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/games/1/access", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.checkGameAccess(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("missing user_id: want 400, got %d", w.Code)
	}
}

func TestCheckGameAccess_Allowed(t *testing.T) {
	svc := newTestGameService(&apiStubGameRepo{canAccess: true})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/games/1/access?user_id=42", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	h := &Handler{gameService: svc, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.checkGameAccess(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"allowed":true`) {
		t.Errorf("want allowed:true, got: %s", w.Body.String())
	}
}

func TestCheckGameAccess_Denied(t *testing.T) {
	svc := newTestGameService(&apiStubGameRepo{canAccess: false})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/games/1/access?user_id=42", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	h := &Handler{gameService: svc, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.checkGameAccess(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"allowed":false`) {
		t.Errorf("want allowed:false, got: %s", w.Body.String())
	}
}

func TestCheckGameAccess_RepoError(t *testing.T) {
	svc := newTestGameService(&apiStubGameRepo{canAccessErr: context.DeadlineExceeded})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/games/1/access?user_id=42", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	h := &Handler{gameService: svc, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.checkGameAccess(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("repo error: want 500, got %d", w.Code)
	}
}

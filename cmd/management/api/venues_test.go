package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/internal/models"
)

type venueRepoForAPI struct{ existing *models.Venue }

func (venueRepoForAPI) Create(_ context.Context, v *models.Venue) (*models.Venue, error) {
	return v, nil
}
func (venueRepoForAPI) GetByID(context.Context, int64) (*models.Venue, error) { return nil, nil }
func (r venueRepoForAPI) GetByIDAndGroupID(context.Context, int64, int64) (*models.Venue, error) {
	return r.existing, nil
}
func (venueRepoForAPI) GetByGroupID(context.Context, int64) ([]*models.Venue, error) { return nil, nil }
func (venueRepoForAPI) Update(_ context.Context, v *models.Venue) (*models.Venue, error) {
	return v, nil
}
func (venueRepoForAPI) Delete(context.Context, int64, int64) error            { return nil }
func (venueRepoForAPI) SetLastBookingReminderAt(context.Context, int64) error { return nil }
func (venueRepoForAPI) SetLastAutoBookingAt(context.Context, int64) error     { return nil }

// newTestHandler returns a Handler with only the logger set.
// The venueService is intentionally nil — all tests in this file exercise
// validation paths that return before the service is ever called.
func newTestHandler() *Handler {
	return &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func newVenueTestHandler() *Handler {
	h := newTestHandler()
	h.venueService = service.NewVenueService(venueRepoForAPI{}, nil)
	return h
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

func TestCreateVenue_PreventiveCancellationFraction(t *testing.T) {
	for _, fraction := range []string{"1/3", "1/2", "2/3"} {
		t.Run(fraction, func(t *testing.T) {
			body := `{"group_id":1,"name":"Court A","courts":"1","preventive_cancellation_fraction":"` + fraction + `"}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/venues", strings.NewReader(body))
			w := httptest.NewRecorder()

			newVenueTestHandler().createVenue(w, req)

			if w.Code != http.StatusCreated {
				t.Errorf("fraction %q: want 201, got %d", fraction, w.Code)
			}
		})
	}
}

func TestCreateVenue_InvalidPreventiveCancellationFraction(t *testing.T) {
	body := `{"group_id":1,"name":"Court A","courts":"1","preventive_cancellation_fraction":"3/4"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/venues", strings.NewReader(body))
	w := httptest.NewRecorder()

	newTestHandler().createVenue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid fraction: want 400, got %d", w.Code)
	}
}

func TestUpdateVenue_PreventiveCancellationFraction(t *testing.T) {
	for _, fraction := range []string{"1/3", "1/2", "2/3"} {
		t.Run(fraction, func(t *testing.T) {
			body := `{"group_id":1,"name":"Court A","courts":"1","preventive_cancellation_fraction":"` + fraction + `"}`
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/venues/1", strings.NewReader(body))
			req.SetPathValue("id", "1")
			w := httptest.NewRecorder()

			newVenueTestHandler().updateVenue(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("fraction %q: want 200, got %d", fraction, w.Code)
			}
		})
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

func TestUpdateVenue_InvalidPreventiveCancellationFraction(t *testing.T) {
	body := `{"group_id":1,"name":"Court A","courts":"1","preventive_cancellation_fraction":"3/4"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/venues/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	newTestHandler().updateVenue(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid fraction: want 400, got %d", w.Code)
	}
}

func TestUpdateVenue_LegacyCourtsPreservesOtherSports(t *testing.T) {
	repo := venueRepoForAPI{existing: &models.Venue{Sports: []models.VenueSport{
		{Sport: "squash", Courts: "1,2"},
		{Sport: "bowling", Courts: "A,B"},
	}}}
	h := newTestHandler()
	h.venueService = service.NewVenueService(repo, nil)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/venues/1", strings.NewReader(
		`{"group_id":1,"name":"Sports Hall","courts":"3,4"}`))
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	h.updateVenue(w, req)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"sport":"bowling","courts":"A,B"`) || !strings.Contains(w.Body.String(), `"sport":"squash","courts":"3,4"`) {
		t.Fatalf("legacy update did not preserve sports: status=%d body=%s", w.Code, w.Body.String())
	}
}

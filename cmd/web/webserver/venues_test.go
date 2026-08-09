package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// venueMgmt is a fake management service for venue routes. It answers
// /api/v1/users/{id}/admin-groups with adminGroupsJSON, GET /api/v1/venues/{id}
// with a venue owned by venueGroupID, and everything else with status + respJSON.
type venueMgmt struct {
	adminGroupsJSON string
	venueGroupID    int64
	venueStatus     int
	status          int
	respJSON        string

	paths   []string
	methods []string
	bodies  []string
}

func newVenuesHandler(t *testing.T, m *venueMgmt) (*VenuesHandler, *AuthHandler) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.paths = append(m.paths, r.URL.RequestURI())
		m.methods = append(m.methods, r.Method)
		m.bodies = append(m.bodies, string(body))

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/admin-groups"):
			fmt.Fprint(w, m.adminGroupsJSON)
		case r.Method == http.MethodGet && isVenueByIDPath(r.URL.Path):
			if m.venueStatus != 0 {
				w.WriteHeader(m.venueStatus)
				fmt.Fprint(w, `{"error":"venue not found"}`)
				return
			}
			fmt.Fprintf(w, `{"id":7,"group_id":%d,"name":"SquashPoint"}`, m.venueGroupID)
		default:
			if m.status != 0 {
				w.WriteHeader(m.status)
			}
			fmt.Fprint(w, m.respJSON)
		}
	}))
	t.Cleanup(srv.Close)

	auth := testAuthHandler(t, nil)
	return NewVenuesHandler(auth, srv.URL, "mgmt-secret"), auth
}

// isVenueByIDPath reports whether path is exactly /api/v1/venues/{id}.
func isVenueByIDPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) == 4 && parts[2] == "venues"
}

func venueReq(t *testing.T, auth *AuthHandler, method, target string, body string) *http.Request {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	r.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	return r
}

// ── group scoping ────────────────────────────────────────────────────────────

func TestListVenues_ForcesGroupID(t *testing.T) {
	m := &venueMgmt{adminGroupsJSON: `[{"chat_id":-100123}]`, respJSON: `[]`}
	v, auth := newVenuesHandler(t, m)

	req := venueReq(t, auth, http.MethodGet, "/api/groups/-100123/venues", "")
	w := routeAndServe("GET /api/groups/{chatID}/venues", v.handleListVenues, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := m.paths[len(m.paths)-1]; got != "/api/v1/venues?group_id=-100123" {
		t.Errorf("upstream path: got %q", got)
	}
}

func TestListVenues_NotAdmin_403(t *testing.T) {
	m := &venueMgmt{adminGroupsJSON: `[]`}
	v, auth := newVenuesHandler(t, m)

	req := venueReq(t, auth, http.MethodGet, "/api/groups/-100123/venues", "")
	w := routeAndServe("GET /api/groups/{chatID}/venues", v.handleListVenues, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestCreateVenue_ForcesGroupIDAndActor(t *testing.T) {
	m := &venueMgmt{adminGroupsJSON: `[{"chat_id":-100123}]`, status: http.StatusCreated, respJSON: `{"id":7}`}
	v, auth := newVenuesHandler(t, m)

	req := venueReq(t, auth, http.MethodPost, "/api/groups/-100123/venues",
		`{"name":"SquashPoint","courts":"1,2","group_id":-999,"actor_user_id":1}`)
	w := routeAndServe("POST /api/groups/{chatID}/venues", v.handleCreateVenue, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", w.Code)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(m.bodies[len(m.bodies)-1]), &sent); err != nil {
		t.Fatalf("decode forwarded body: %v", err)
	}
	if sent["group_id"] != float64(-100123) {
		t.Errorf("group_id must be forced to the path chat id, got %v", sent["group_id"])
	}
	if sent["actor_user_id"] != float64(42) {
		t.Errorf("actor must come from the JWT, got %v", sent["actor_user_id"])
	}
}

// ── cross-group venue access ─────────────────────────────────────────────────

func TestGetVenue_OtherGroupsVenue_404(t *testing.T) {
	m := &venueMgmt{adminGroupsJSON: `[{"chat_id":-100123}]`, venueGroupID: -999}
	v, auth := newVenuesHandler(t, m)

	req := venueReq(t, auth, http.MethodGet, "/api/groups/-100123/venues/7", "")
	w := routeAndServe("GET /api/groups/{chatID}/venues/{venueID}", v.handleGetVenue, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("venue of another group must 404, got %d", w.Code)
	}
}

func TestGetVenue_OwnVenue_200(t *testing.T) {
	m := &venueMgmt{adminGroupsJSON: `[{"chat_id":-100123}]`, venueGroupID: -100123}
	v, auth := newVenuesHandler(t, m)

	req := venueReq(t, auth, http.MethodGet, "/api/groups/-100123/venues/7", "")
	w := routeAndServe("GET /api/groups/{chatID}/venues/{venueID}", v.handleGetVenue, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestUpdateVenue_ConflictPassthrough(t *testing.T) {
	m := &venueMgmt{
		adminGroupsJSON: `[{"chat_id":-100123}]`,
		venueGroupID:    -100123,
		status:          http.StatusBadRequest,
		respJSON:        `{"error":"auto_booking_disallowed_by_owner"}`,
	}
	v, auth := newVenuesHandler(t, m)

	req := venueReq(t, auth, http.MethodPatch, "/api/groups/-100123/venues/7",
		`{"name":"X","courts":"1","auto_booking_enabled":true}`)
	w := routeAndServe("PATCH /api/groups/{chatID}/venues/{venueID}", v.handleUpdateVenue, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want the upstream 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "auto_booking_disallowed_by_owner") {
		t.Errorf("upstream error body not proxied: %s", w.Body.String())
	}
}

func TestDeleteVenue_PassesActorAndGroup(t *testing.T) {
	m := &venueMgmt{adminGroupsJSON: `[{"chat_id":-100123}]`, venueGroupID: -100123, status: http.StatusConflict,
		respJSON: `{"error":"venue has active court bookings and cannot be deleted"}`}
	v, auth := newVenuesHandler(t, m)

	req := venueReq(t, auth, http.MethodDelete, "/api/groups/-100123/venues/7", "")
	w := routeAndServe("DELETE /api/groups/{chatID}/venues/{venueID}", v.handleDeleteVenue, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("409 must be proxied, got %d", w.Code)
	}
	last := m.paths[len(m.paths)-1]
	for _, want := range []string{"group_id=-100123", "actor_user_id=42", "actor_display=Test"} {
		if !strings.Contains(last, want) {
			t.Errorf("delete query missing %q: %s", want, last)
		}
	}
}

// ── credentials ──────────────────────────────────────────────────────────────

func TestAddCredential_503Passthrough(t *testing.T) {
	m := &venueMgmt{adminGroupsJSON: `[{"chat_id":-100123}]`, venueGroupID: -100123,
		status: http.StatusServiceUnavailable, respJSON: `{"error":"credential management is disabled"}`}
	v, auth := newVenuesHandler(t, m)

	req := venueReq(t, auth, http.MethodPost, "/api/groups/-100123/venues/7/credentials",
		`{"login":"a@b.c","password":"secret","priority":1,"max_courts":3}`)
	w := routeAndServe("POST /api/groups/{chatID}/venues/{venueID}/credentials", v.handleAddCredential, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", w.Code)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(m.bodies[len(m.bodies)-1]), &sent); err != nil {
		t.Fatalf("decode forwarded body: %v", err)
	}
	if sent["group_id"] != float64(-100123) {
		t.Errorf("group_id must be forced, got %v", sent["group_id"])
	}
}

func TestDeleteCredential_ScopedToVenueAndGroup(t *testing.T) {
	m := &venueMgmt{adminGroupsJSON: `[{"chat_id":-100123}]`, venueGroupID: -100123, status: http.StatusNoContent}
	v, auth := newVenuesHandler(t, m)

	req := venueReq(t, auth, http.MethodDelete, "/api/groups/-100123/venues/7/credentials/3", "")
	w := routeAndServe("DELETE /api/groups/{chatID}/venues/{venueID}/credentials/{cid}", v.handleDeleteCredential, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	if got := m.paths[len(m.paths)-1]; !strings.HasPrefix(got, "/api/v1/venues/7/credentials/3?") {
		t.Errorf("upstream path: got %q", got)
	}
}

func TestBookingReadiness_ProxiesReason(t *testing.T) {
	m := &venueMgmt{adminGroupsJSON: `[{"chat_id":-100123}]`, venueGroupID: -100123,
		respJSON: `{"ready":false,"max_courts":0,"reason":"no_usable_credentials"}`}
	v, auth := newVenuesHandler(t, m)

	req := venueReq(t, auth, http.MethodGet, "/api/groups/-100123/venues/7/booking-readiness", "")
	w := routeAndServe("GET /api/groups/{chatID}/venues/{venueID}/booking-readiness", v.handleBookingReadiness, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "no_usable_credentials") {
		t.Errorf("readiness body not proxied: %s", w.Body.String())
	}
}

// ── preferences ──────────────────────────────────────────────────────────────

func TestPrefs_UsesUserIDFromJWT(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	auth := testAuthHandler(t, nil)
	p := NewPrefsHandler(auth, srv.URL, "mgmt-secret")

	req := httptest.NewRequest(http.MethodPatch, "/api/me/dm-language", strings.NewReader(`{"language":"de"}`))
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("PATCH /api/me/dm-language", p.patchPreference("dm-language"), req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	if want := "/api/v1/users/42/dm-language"; seen[0] != want {
		t.Errorf("want %q, got %q", want, seen[0])
	}
}

func TestPrefs_NoSession_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("management should not be called")
	}))
	t.Cleanup(srv.Close)

	p := NewPrefsHandler(testAuthHandler(t, nil), srv.URL, "mgmt-secret")
	req := httptest.NewRequest(http.MethodGet, "/api/me/preferences", nil)
	w := routeAndServe("GET /api/me/preferences", p.handleGetPreferences, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

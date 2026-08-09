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

// usersRecorder is a fake management service for the users routes. It records
// every request's path, body, and headers so tests can assert on the identity
// the handler forwarded, and answers with status+respJSON (default 200).
type usersRecorder struct {
	status   int
	respJSON string

	paths   []string
	bodies  []string
	headers []http.Header
}

func newUsersMgmtServer(t *testing.T, rec *usersRecorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.paths = append(rec.paths, r.URL.RequestURI())
		rec.bodies = append(rec.bodies, string(body))
		rec.headers = append(rec.headers, r.Header.Clone())

		w.Header().Set("Content-Type", "application/json")
		if rec.status != 0 {
			w.WriteHeader(rec.status)
		}
		fmt.Fprint(w, rec.respJSON)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testUsersHandler(t *testing.T, mgmt *httptest.Server) (*UsersHandler, *AuthHandler) {
	t.Helper()
	auth := testAuthHandler(t, nil)
	return NewUsersHandler(auth, mgmt.URL, "mgmt-secret"), auth
}

// ── GET /api/users ────────────────────────────────────────────────────────────

func TestHandleListUsers_NoSession_401(t *testing.T) {
	rec := &usersRecorder{}
	mgmt := newUsersMgmtServer(t, rec)
	u, _ := testUsersHandler(t, mgmt)

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	w := routeAndServe("GET /api/users", u.handleListUsers, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
	if len(rec.paths) != 0 {
		t.Errorf("management should not be called, saw %v", rec.paths)
	}
}

func TestHandleListUsers_InjectsCallerUserIdHeader(t *testing.T) {
	rec := &usersRecorder{respJSON: `[{"user_id":1,"display_name":"@alice","is_server_owner":true,"providers":["telegram"],"created_at":"2026-01-01T00:00:00Z"}]`}
	mgmt := newUsersMgmtServer(t, rec)
	u, auth := testUsersHandler(t, mgmt)

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/users", u.handleListUsers, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	if got := rec.headers[0].Get("X-Caller-User-Id"); got != "42" {
		t.Errorf("X-Caller-User-Id: want 42 (from the JWT), got %q", got)
	}
	if got := rec.headers[0].Get("Authorization"); got != "Bearer mgmt-secret" {
		t.Errorf("Authorization: want 'Bearer mgmt-secret', got %q", got)
	}
	if got := w.Body.String(); got != rec.respJSON {
		t.Errorf("body not proxied verbatim:\n  want: %s\n  got:  %s", rec.respJSON, got)
	}
}

func TestHandleListUsers_UpstreamForbidden_Passthrough(t *testing.T) {
	// A non-owner caller: management itself enforces the check and 403s.
	rec := &usersRecorder{status: http.StatusForbidden, respJSON: `{"error":"forbidden"}`}
	mgmt := newUsersMgmtServer(t, rec)
	u, auth := testUsersHandler(t, mgmt)

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/users", u.handleListUsers, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403 proxied verbatim, got %d", w.Code)
	}
}

func TestHandleListUsers_UpstreamUnavailable_502(t *testing.T) {
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	mgmt.Close() // closed immediately so the HTTP client gets a connection error

	u, auth := testUsersHandler(t, mgmt)
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/users", u.handleListUsers, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("want 502, got %d", w.Code)
	}
}

// ── PATCH /api/users/{userID}/server-owner ───────────────────────────────────

func TestHandleSetServerOwner_NoSession_401(t *testing.T) {
	rec := &usersRecorder{}
	mgmt := newUsersMgmtServer(t, rec)
	u, _ := testUsersHandler(t, mgmt)

	req := httptest.NewRequest(http.MethodPatch, "/api/users/7/server-owner", strings.NewReader(`{"enabled":true}`))
	w := routeAndServe("PATCH /api/users/{userID}/server-owner", u.handleSetServerOwner, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
	if len(rec.paths) != 0 {
		t.Errorf("management should not be called, saw %v", rec.paths)
	}
}

func TestHandleSetServerOwner_InvalidUserID_400(t *testing.T) {
	rec := &usersRecorder{}
	mgmt := newUsersMgmtServer(t, rec)
	u, auth := testUsersHandler(t, mgmt)

	req := httptest.NewRequest(http.MethodPatch, "/api/users/abc/server-owner", strings.NewReader(`{"enabled":true}`))
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("PATCH /api/users/{userID}/server-owner", u.handleSetServerOwner, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// The actor must always come from the JWT — a client-supplied actor_user_id
// must never reach management, or a caller could attribute a role change to
// (or spoof authority as) an arbitrary other user.
func TestHandleSetServerOwner_OverwritesSpoofedActorFields(t *testing.T) {
	rec := &usersRecorder{status: http.StatusNoContent}
	mgmt := newUsersMgmtServer(t, rec)
	u, auth := testUsersHandler(t, mgmt)

	req := httptest.NewRequest(http.MethodPatch, "/api/users/7/server-owner",
		strings.NewReader(`{"enabled":true,"actor_user_id":999,"actor_display":"spoofed"}`))
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("PATCH /api/users/{userID}/server-owner", u.handleSetServerOwner, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d (body: %s)", w.Code, w.Body.String())
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(rec.bodies[0]), &sent); err != nil {
		t.Fatalf("decode forwarded body %q: %v", rec.bodies[0], err)
	}
	if sent["enabled"] != true {
		t.Errorf("enabled not forwarded: %v", sent)
	}
	if sent["actor_user_id"] != float64(42) {
		t.Errorf("actor_user_id must come from the JWT (42), got %v — spoofed value must be overwritten", sent["actor_user_id"])
	}
	if sent["actor_display"] != "Test" {
		t.Errorf("actor_display must come from the JWT, got %v", sent["actor_display"])
	}
	if got := rec.paths[0]; got != "/api/v1/users/7/server-owner" {
		t.Errorf("upstream path: got %q", got)
	}
}

// A literal `null` decodes into a nil map; injecting the actor fields into it
// used to panic instead of rejecting the body (see groups.go's equivalent guard).
func TestHandleSetServerOwner_NullBody_400(t *testing.T) {
	rec := &usersRecorder{status: http.StatusNoContent}
	mgmt := newUsersMgmtServer(t, rec)
	u, auth := testUsersHandler(t, mgmt)

	req := httptest.NewRequest(http.MethodPatch, "/api/users/7/server-owner", strings.NewReader(`null`))
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("PATCH /api/users/{userID}/server-owner", u.handleSetServerOwner, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestHandleSetServerOwner_UpstreamForbidden_Passthrough(t *testing.T) {
	// Actor is not a server owner: management enforces this itself and 403s.
	rec := &usersRecorder{status: http.StatusForbidden, respJSON: `{"error":"forbidden"}`}
	mgmt := newUsersMgmtServer(t, rec)
	u, auth := testUsersHandler(t, mgmt)

	req := httptest.NewRequest(http.MethodPatch, "/api/users/7/server-owner", strings.NewReader(`{"enabled":false}`))
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("PATCH /api/users/{userID}/server-owner", u.handleSetServerOwner, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403 proxied verbatim, got %d", w.Code)
	}
}

func TestHandleSetServerOwner_UpstreamConflict_LastOwner_Passthrough(t *testing.T) {
	rec := &usersRecorder{status: http.StatusConflict, respJSON: `{"error":"cannot revoke the last server owner"}`}
	mgmt := newUsersMgmtServer(t, rec)
	u, auth := testUsersHandler(t, mgmt)

	req := httptest.NewRequest(http.MethodPatch, "/api/users/7/server-owner", strings.NewReader(`{"enabled":false}`))
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("PATCH /api/users/{userID}/server-owner", u.handleSetServerOwner, req)

	if w.Code != http.StatusConflict {
		t.Errorf("want 409 proxied verbatim, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "cannot revoke the last server owner") {
		t.Errorf("upstream error body not proxied: %s", w.Body.String())
	}
}

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

// testGroupsHandler returns a GroupsHandler wired to the given fake management server.
// ownerIDs maps Telegram IDs that should be treated as server owners.
func testGroupsHandler(t *testing.T, mgmt *httptest.Server, ownerIDs map[int64]bool) (*GroupsHandler, *AuthHandler) {
	t.Helper()
	auth := testAuthHandler(t, nil)
	auth.serverOwnerIDs = ownerIDs
	return NewGroupsHandler(auth, mgmt.URL, "mgmt-secret"), auth
}

// mgmtRecorder is a fake management service that records the requests it saw
// and answers /api/v1/admins/{id}/groups with adminGroupsJSON; every other path
// returns 200 with respJSON.
type mgmtRecorder struct {
	adminGroupsJSON string
	respJSON        string
	status          int

	paths   []string
	methods []string
	bodies  []string
}

func newMgmtServer(t *testing.T, rec *mgmtRecorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.paths = append(rec.paths, r.URL.RequestURI())
		rec.methods = append(rec.methods, r.Method)
		rec.bodies = append(rec.bodies, string(body))

		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/v1/admins/") {
			fmt.Fprint(w, rec.adminGroupsJSON)
			return
		}
		if rec.status != 0 {
			w.WriteHeader(rec.status)
		}
		fmt.Fprint(w, rec.respJSON)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ── GET /api/groups ──────────────────────────────────────────────────────────

func TestGroupsHandler_NoSession_401(t *testing.T) {
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("management should not be called")
	}))
	t.Cleanup(mgmt.Close)

	g, _ := testGroupsHandler(t, mgmt, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
	w := routeAndServe("GET /api/groups", g.handleListGroups, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

// A non-owner is no longer rejected: management decides which groups they see.
func TestGroupsHandler_NonOwner_ListsOwnGroups(t *testing.T) {
	rec := &mgmtRecorder{adminGroupsJSON: `[{"chat_id":-100123}]`}
	mgmt := newMgmtServer(t, rec)
	g, auth := testGroupsHandler(t, mgmt, map[int64]bool{999: true})

	req := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/groups", g.handleListGroups, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if want := "/api/v1/admins/42/groups"; rec.paths[0] != want {
		t.Errorf("upstream path: want %q, got %q", want, rec.paths[0])
	}
}

func TestGroupsHandler_Owner_ProxiesResponse(t *testing.T) {
	const groupsJSON = `[{"chat_id":-100123,"title":"Test Group","bot_is_admin":true,"language":"en","timezone":"UTC","added_at":"2026-01-15T10:00:00Z"}]`

	var capturedAuth string
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, groupsJSON)
	}))
	t.Cleanup(mgmt.Close)

	g, auth := testGroupsHandler(t, mgmt, map[int64]bool{42: true})

	req := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/groups", g.handleListGroups, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	if capturedAuth != "Bearer mgmt-secret" {
		t.Errorf("Authorization: want 'Bearer mgmt-secret', got %q", capturedAuth)
	}
	if got := w.Body.String(); got != groupsJSON {
		t.Errorf("body mismatch:\n  want: %s\n  got:  %s", groupsJSON, got)
	}
}

func TestGroupsHandler_UpstreamError_502(t *testing.T) {
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	// Close immediately so the HTTP client gets a connection error.
	mgmt.Close()

	g, auth := testGroupsHandler(t, mgmt, map[int64]bool{42: true})

	req := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/groups", g.handleListGroups, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("want 502, got %d", w.Code)
	}
}

// ── authorizeGroup matrix (via GET /api/groups/{chatID}) ─────────────────────

func TestAuthorizeGroup_NoSession_401(t *testing.T) {
	rec := &mgmtRecorder{adminGroupsJSON: `[]`}
	mgmt := newMgmtServer(t, rec)
	g, _ := testGroupsHandler(t, mgmt, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/groups/-100123", nil)
	w := routeAndServe("GET /api/groups/{chatID}", g.handleGetGroup, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
	if len(rec.paths) != 0 {
		t.Errorf("management should not be called, saw %v", rec.paths)
	}
}

func TestAuthorizeGroup_NotAdmin_403(t *testing.T) {
	rec := &mgmtRecorder{adminGroupsJSON: `[{"chat_id":-999}]`}
	mgmt := newMgmtServer(t, rec)
	g, auth := testGroupsHandler(t, mgmt, map[int64]bool{999: true})

	req := httptest.NewRequest(http.MethodGet, "/api/groups/-100123", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/groups/{chatID}", g.handleGetGroup, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestAuthorizeGroup_Admin_200(t *testing.T) {
	rec := &mgmtRecorder{adminGroupsJSON: `[{"chat_id":-100123}]`, respJSON: `{"chat_id":-100123}`}
	mgmt := newMgmtServer(t, rec)
	g, auth := testGroupsHandler(t, mgmt, map[int64]bool{999: true})

	req := httptest.NewRequest(http.MethodGet, "/api/groups/-100123", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/groups/{chatID}", g.handleGetGroup, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := rec.paths[len(rec.paths)-1]; got != "/api/v1/groups/-100123" {
		t.Errorf("upstream path: got %q", got)
	}
}

func TestAuthorizeGroup_Owner_SkipsLookup(t *testing.T) {
	rec := &mgmtRecorder{adminGroupsJSON: `[]`, respJSON: `{"chat_id":-100123}`}
	mgmt := newMgmtServer(t, rec)
	g, auth := testGroupsHandler(t, mgmt, map[int64]bool{42: true})

	req := httptest.NewRequest(http.MethodGet, "/api/groups/-100123", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/groups/{chatID}", g.handleGetGroup, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	for _, p := range rec.paths {
		if strings.HasPrefix(p, "/api/v1/admins/") {
			t.Errorf("owner must not trigger an admin-groups lookup, saw %v", rec.paths)
		}
	}
}

func TestAuthorizeGroup_InvalidChatID_400(t *testing.T) {
	rec := &mgmtRecorder{adminGroupsJSON: `[]`}
	mgmt := newMgmtServer(t, rec)
	g, auth := testGroupsHandler(t, mgmt, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/groups/abc", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/groups/{chatID}", g.handleGetGroup, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// ── settings PATCH ───────────────────────────────────────────────────────────

func TestPatchGroupSetting_InjectsActorFields(t *testing.T) {
	rec := &mgmtRecorder{adminGroupsJSON: `[{"chat_id":-100123}]`, status: http.StatusNoContent}
	mgmt := newMgmtServer(t, rec)
	g, auth := testGroupsHandler(t, mgmt, map[int64]bool{999: true})

	req := httptest.NewRequest(http.MethodPatch, "/api/groups/-100123/language",
		strings.NewReader(`{"language":"de","actor_telegram_id":1,"actor_display":"spoofed"}`))
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("PATCH /api/groups/{chatID}/language", g.patchGroupSetting("language"), req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	last := rec.bodies[len(rec.bodies)-1]
	var sent map[string]any
	if err := json.Unmarshal([]byte(last), &sent); err != nil {
		t.Fatalf("decode forwarded body %q: %v", last, err)
	}
	if sent["language"] != "de" {
		t.Errorf("language not forwarded: %v", sent)
	}
	if sent["actor_telegram_id"] != float64(42) {
		t.Errorf("actor_telegram_id must come from the JWT, got %v", sent["actor_telegram_id"])
	}
	if sent["actor_display"] != "Test" {
		t.Errorf("actor_display must come from the JWT, got %v", sent["actor_display"])
	}
	if got := rec.paths[len(rec.paths)-1]; got != "/api/v1/groups/-100123/language" {
		t.Errorf("upstream path: got %q", got)
	}
}

// A literal `null` decodes into a nil map; injecting the actor fields into it
// used to panic instead of rejecting the body.
func TestPatchGroupSetting_NullBody_400(t *testing.T) {
	rec := &mgmtRecorder{adminGroupsJSON: `[{"chat_id":-100123}]`, status: http.StatusNoContent}
	mgmt := newMgmtServer(t, rec)
	g, auth := testGroupsHandler(t, mgmt, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/groups/-100123/language", strings.NewReader(`null`))
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("PATCH /api/groups/{chatID}/language", g.patchGroupSetting("language"), req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestPatchGroupSetting_NotAdmin_403(t *testing.T) {
	rec := &mgmtRecorder{adminGroupsJSON: `[]`}
	mgmt := newMgmtServer(t, rec)
	g, auth := testGroupsHandler(t, mgmt, map[int64]bool{999: true})

	req := httptest.NewRequest(http.MethodPatch, "/api/groups/-100123/timezone", strings.NewReader(`{"timezone":"UTC"}`))
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("PATCH /api/groups/{chatID}/timezone", g.patchGroupSetting("timezone"), req)

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestSetGroupAutoBookingAllowed_NonOwner_403(t *testing.T) {
	rec := &mgmtRecorder{adminGroupsJSON: `[{"chat_id":-100123}]`}
	mgmt := newMgmtServer(t, rec)
	g, auth := testGroupsHandler(t, mgmt, map[int64]bool{999: true})

	req := httptest.NewRequest(http.MethodPatch, "/api/groups/-100123/auto-booking-allowed", strings.NewReader(`{"enabled":false}`))
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("PATCH /api/groups/{chatID}/auto-booking-allowed", g.handleSetGroupAutoBookingAllowed, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("group admin must not flip the master switch: want 403, got %d", w.Code)
	}
}

func TestSetGroupAutoBookingAllowed_Owner_Forwards(t *testing.T) {
	rec := &mgmtRecorder{adminGroupsJSON: `[]`, status: http.StatusNoContent}
	mgmt := newMgmtServer(t, rec)
	g, auth := testGroupsHandler(t, mgmt, map[int64]bool{42: true})

	req := httptest.NewRequest(http.MethodPatch, "/api/groups/-100123/auto-booking-allowed", strings.NewReader(`{"enabled":false}`))
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("PATCH /api/groups/{chatID}/auto-booking-allowed", g.handleSetGroupAutoBookingAllowed, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	if !strings.Contains(rec.bodies[0], `"actor_telegram_id":42`) {
		t.Errorf("actor not injected: %s", rec.bodies[0])
	}
}

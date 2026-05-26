package webserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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

// ── auth enforcement ─────────────────────────────────────────────────────────

func TestGroupsHandler_NoSession_401(t *testing.T) {
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("management should not be called")
	}))
	t.Cleanup(mgmt.Close)

	g, _ := testGroupsHandler(t, mgmt, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
	w := routeAndServe("GET /api/groups", g.handleListGroups, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestGroupsHandler_NotOwner_403(t *testing.T) {
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("management should not be called")
	}))
	t.Cleanup(mgmt.Close)

	ownerIDs := map[int64]bool{999: true}
	g, auth := testGroupsHandler(t, mgmt, ownerIDs)

	req := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/groups", g.handleListGroups, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

// ── happy path ───────────────────────────────────────────────────────────────

func TestGroupsHandler_Owner_ProxiesResponse(t *testing.T) {
	const groupsJSON = `[{"chat_id":-100123,"title":"Test Group","bot_is_admin":true,"language":"en","timezone":"UTC","added_at":"2026-01-15T10:00:00Z"}]`

	var capturedAuth string
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, groupsJSON)
	}))
	t.Cleanup(mgmt.Close)

	ownerIDs := map[int64]bool{42: true}
	g, auth := testGroupsHandler(t, mgmt, ownerIDs)

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

	ownerIDs := map[int64]bool{42: true}
	g, auth := testGroupsHandler(t, mgmt, ownerIDs)

	req := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/groups", g.handleListGroups, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("want 502, got %d", w.Code)
	}
}

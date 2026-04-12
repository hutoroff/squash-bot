package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// testGamesHandler returns a GamesHandler wired to the given fake management server.
func testGamesHandler(t *testing.T, mgmt *httptest.Server) (*GamesHandler, *AuthHandler) {
	t.Helper()
	auth := testAuthHandler(t, nil)
	return NewGamesHandler(auth, mgmt.URL, "mgmt-secret"), auth
}

// validSessionCookie returns a signed JWT cookie for the given telegram user.
func validSessionCookie(t *testing.T, auth *AuthHandler, telegramID int64, username string) *http.Cookie {
	t.Helper()
	token, err := issueJWT(auth.jwtSecret, JWTClaims{
		TelegramID: telegramID,
		Username:   username,
		FirstName:  "Test",
		Exp:        time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	return &http.Cookie{Name: sessionCookieName, Value: token}
}

// routeAndServe wires handler fn into a mux with pattern, sends req, returns recorder.
// Required so that r.PathValue("id") is populated (Go 1.22 feature).
func routeAndServe(pattern string, fn http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc(pattern, fn)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// participationsServer returns a test server that responds to the three endpoints
// that game action handlers call (action + /participations + /guests).
// The action endpoint responds with actionStatus; the list endpoints return their JSON.
func participationsServer(t *testing.T, actionStatus int, partsJSON, guestsJSON string) (*httptest.Server, *[]byte) {
	t.Helper()
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/participations"):
			fmt.Fprint(w, partsJSON)
		case strings.HasSuffix(r.URL.Path, "/guests"):
			fmt.Fprint(w, guestsJSON)
		default:
			// Any other call is the action endpoint (join / skip / guest).
			body, _ := io.ReadAll(r.Body)
			capturedBody = body
			w.WriteHeader(actionStatus)
			fmt.Fprint(w, "[]")
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &capturedBody
}

// ── auth enforcement ──────────────────────────────────────────────────────────

func TestGamesRoutes_RequireAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "[]")
	}))
	t.Cleanup(srv.Close)

	auth := testAuthHandler(t, nil)
	g := NewGamesHandler(auth, srv.URL, "mgmt-secret")

	routes := []struct {
		method  string
		pattern string
		path    string
		handler http.HandlerFunc
	}{
		{"GET", "GET /api/games/{id}/participants", "/api/games/1/participants", g.handleGetParticipants},
		{"POST", "POST /api/games/{id}/join", "/api/games/1/join", g.handleJoinGame},
		{"POST", "POST /api/games/{id}/skip", "/api/games/1/skip", g.handleSkipGame},
		{"POST", "POST /api/games/{id}/guest", "/api/games/1/guest", g.handleAddGuest},
		{"DELETE", "DELETE /api/games/{id}/guest", "/api/games/1/guest", g.handleRemoveGuest},
	}

	for _, tc := range routes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			// No session cookie attached.
			w := routeAndServe(tc.pattern, tc.handler, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("want 401 without cookie, got %d", w.Code)
			}
		})
	}
}

// ── handleGetParticipants ─────────────────────────────────────────────────────

func TestHandleGetParticipants_HappyPath(t *testing.T) {
	const partsJSON = `[{"id":1,"player":{"telegram_id":42,"username":"alice"},"status":"registered"}]`
	const guestsJSON = `[{"id":1,"invited_by":{"telegram_id":42}}]`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/participations") {
			fmt.Fprint(w, partsJSON)
			return
		}
		fmt.Fprint(w, guestsJSON)
	}))
	t.Cleanup(srv.Close)

	g, auth := testGamesHandler(t, srv)

	req := httptest.NewRequest(http.MethodGet, "/api/games/42/participants", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/games/{id}/participants", g.handleGetParticipants, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	parts, ok := resp["participations"].([]any)
	if !ok || len(parts) != 1 {
		t.Errorf("want 1 participation, got %v", resp["participations"])
	}
	guests, ok := resp["guests"].([]any)
	if !ok || len(guests) != 1 {
		t.Errorf("want 1 guest, got %v", resp["guests"])
	}
}

func TestHandleGetParticipants_ManagementError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	g, auth := testGamesHandler(t, srv)

	req := httptest.NewRequest(http.MethodGet, "/api/games/1/participants", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("GET /api/games/{id}/participants", g.handleGetParticipants, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("want 502 on upstream error, got %d", w.Code)
	}
}

// ── handleJoinGame ────────────────────────────────────────────────────────────

func TestHandleJoinGame_HappyPath(t *testing.T) {
	srv, capturedBody := participationsServer(t, http.StatusOK, "[]", "[]")

	var capturedAuth string
	// Wrap the server to also capture the Authorization header on the action call.
	wrappedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/join") {
			capturedAuth = r.Header.Get("Authorization")
		}
		srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(wrappedSrv.Close)

	auth := testAuthHandler(t, nil)
	g := NewGamesHandler(auth, wrappedSrv.URL, "mgmt-secret")

	req := httptest.NewRequest(http.MethodPost, "/api/games/5/join", nil)
	req.AddCookie(validSessionCookie(t, auth, 99, "bob"))
	w := routeAndServe("POST /api/games/{id}/join", g.handleJoinGame, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Bearer token forwarded to management.
	if capturedAuth != "Bearer mgmt-secret" {
		t.Errorf("Authorization: want 'Bearer mgmt-secret', got %q", capturedAuth)
	}

	// JWT claims forwarded as player identity in the request body.
	var body map[string]any
	if err := json.Unmarshal(*capturedBody, &body); err != nil {
		t.Fatalf("decode forwarded body: %v (raw: %s)", err, string(*capturedBody))
	}
	if tid, _ := body["telegram_id"].(float64); int64(tid) != 99 {
		t.Errorf("forwarded telegram_id: want 99, got %v", body["telegram_id"])
	}
	if uname, _ := body["username"].(string); uname != "bob" {
		t.Errorf("forwarded username: want 'bob', got %q", uname)
	}
}

func TestHandleJoinGame_ManagementError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	g, auth := testGamesHandler(t, srv)

	req := httptest.NewRequest(http.MethodPost, "/api/games/5/join", nil)
	req.AddCookie(validSessionCookie(t, auth, 99, "bob"))
	w := routeAndServe("POST /api/games/{id}/join", g.handleJoinGame, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("want 502 on upstream error, got %d", w.Code)
	}
}

// ── handleSkipGame ────────────────────────────────────────────────────────────

func TestHandleSkipGame_ForwardsUserIdentity(t *testing.T) {
	srv, capturedBody := participationsServer(t, http.StatusOK, "[]", "[]")

	g, auth := testGamesHandler(t, srv)

	req := httptest.NewRequest(http.MethodPost, "/api/games/3/skip", nil)
	req.AddCookie(validSessionCookie(t, auth, 55, "carol"))
	w := routeAndServe("POST /api/games/{id}/skip", g.handleSkipGame, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(*capturedBody, &body); err != nil {
		t.Fatalf("decode forwarded body: %v", err)
	}
	if tid, _ := body["telegram_id"].(float64); int64(tid) != 55 {
		t.Errorf("forwarded telegram_id: want 55, got %v", body["telegram_id"])
	}
}

// ── handleAddGuest / handleRemoveGuest ────────────────────────────────────────

func TestHandleAddGuest_HappyPath(t *testing.T) {
	srv, _ := participationsServer(t, http.StatusOK, "[]", "[]")
	g, auth := testGamesHandler(t, srv)

	req := httptest.NewRequest(http.MethodPost, "/api/games/7/guest", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("POST /api/games/{id}/guest", g.handleAddGuest, req)

	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
}

func TestHandleRemoveGuest_HappyPath(t *testing.T) {
	srv, _ := participationsServer(t, http.StatusOK, "[]", "[]")
	g, auth := testGamesHandler(t, srv)

	req := httptest.NewRequest(http.MethodDelete, "/api/games/7/guest", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := routeAndServe("DELETE /api/games/{id}/guest", g.handleRemoveGuest, req)

	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
}

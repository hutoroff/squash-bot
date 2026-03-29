package booking

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/vkhutorov/squash_bot/internal/eversports"
)

// testLogger silences all log output during tests.
var testLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))

// mockClient implements eversportsClient for tests. Fields control
// what each method returns.
type mockClient struct {
	loginErr    error
	loggedIn    bool
	bookings    []eversports.Booking
	bookingsErr error
	match       *eversports.Booking
	matchErr    error
	lastMatchID string // records the matchID passed to GetMatchByID
	debugInfo   *eversports.PageDebugInfo
	debugErr    error
}

func (m *mockClient) Login(_ context.Context) error {
	if m.loginErr == nil {
		m.loggedIn = true
	}
	return m.loginErr
}

func (m *mockClient) IsLoggedIn() bool { return m.loggedIn }

func (m *mockClient) GetBookings(_ context.Context) ([]eversports.Booking, error) {
	return m.bookings, m.bookingsErr
}

func (m *mockClient) GetMatchByID(_ context.Context, matchID string) (*eversports.Booking, error) {
	m.lastMatchID = matchID
	return m.match, m.matchErr
}

func (m *mockClient) FetchPageDebugInfo(_ context.Context) (*eversports.PageDebugInfo, error) {
	return m.debugInfo, m.debugErr
}

// newTestHandler creates a Handler backed by the given mock.
func newTestHandler(mock *mockClient) *Handler {
	return &Handler{eversports: mock, logger: testLogger, version: "test-version"}
}

// serve registers routes on a fresh mux and returns a test server.
func serve(h *Handler) *httptest.Server {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return httptest.NewServer(mux)
}

// ─── /health ──────────────────────────────────────────────────────────────────

func TestHealth(t *testing.T) {
	srv := serve(newTestHandler(&mockClient{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
}

// ─── /version ─────────────────────────────────────────────────────────────────

func TestGetVersion(t *testing.T) {
	srv := serve(newTestHandler(&mockClient{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["version"] != "test-version" {
		t.Errorf("version: want %q, got %q", "test-version", body["version"])
	}
}

// ─── POST /api/v1/eversports/login ────────────────────────────────────────────

func TestLogin_Success(t *testing.T) {
	srv := serve(newTestHandler(&mockClient{}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/eversports/login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "logged_in" {
		t.Errorf("status: want %q, got %q", "logged_in", body["status"])
	}
}

func TestLogin_Failure(t *testing.T) {
	mock := &mockClient{loginErr: errors.New("wrong password")}
	srv := serve(newTestHandler(mock))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/eversports/login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

// ─── GET /api/v1/eversports/bookings ─────────────────────────────────────────

func TestGetBookings_NotLoggedIn(t *testing.T) {
	srv := serve(newTestHandler(&mockClient{loggedIn: false}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/bookings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("want 412, got %d", resp.StatusCode)
	}
}

func TestGetBookings_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	bookings := []eversports.Booking{
		{ID: "booking-1", Start: now, End: now.Add(time.Hour), State: "ACCEPTED"},
	}
	mock := &mockClient{loggedIn: true, bookings: bookings}
	srv := serve(newTestHandler(mock))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/bookings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	var got []eversports.Booking
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("bookings count: want 1, got %d", len(got))
	}
	if got[0].ID != "booking-1" {
		t.Errorf("ID: want %q, got %q", "booking-1", got[0].ID)
	}
}

func TestGetBookings_Error(t *testing.T) {
	mock := &mockClient{loggedIn: true, bookingsErr: errors.New("network error")}
	srv := serve(newTestHandler(mock))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/bookings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

// ─── GET /api/v1/eversports/matches/{id} ─────────────────────────────────────

func TestGetMatch_NotLoggedIn(t *testing.T) {
	srv := serve(newTestHandler(&mockClient{loggedIn: false}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches/some-id")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("want 412, got %d", resp.StatusCode)
	}
}

func TestGetMatch_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	match := &eversports.Booking{ID: "match-uuid", Start: now, End: now.Add(time.Hour), State: "ACCEPTED"}
	mock := &mockClient{loggedIn: true, match: match}
	srv := serve(newTestHandler(mock))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches/match-uuid")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	var got eversports.Booking
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "match-uuid" {
		t.Errorf("ID: want %q, got %q", "match-uuid", got.ID)
	}
	if mock.lastMatchID != "match-uuid" {
		t.Errorf("forwarded matchID: want %q, got %q", "match-uuid", mock.lastMatchID)
	}
}

func TestGetMatch_Error(t *testing.T) {
	mock := &mockClient{loggedIn: true, matchErr: errors.New("not found")}
	srv := serve(newTestHandler(mock))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches/bad-id")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

// ─── GET /api/v1/eversports/debug-page ───────────────────────────────────────

func TestDebugPage_NotLoggedIn(t *testing.T) {
	srv := serve(newTestHandler(&mockClient{loggedIn: false}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/debug-page")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("want 412, got %d", resp.StatusCode)
	}
}

func TestDebugPage_Success(t *testing.T) {
	info := &eversports.PageDebugInfo{
		URL:         "https://www.eversports.de/user/bookings",
		FinalURL:    "https://www.eversports.de/user/bookings",
		Status:      200,
		HasNextData: false,
		HTMLSnippet: "<html>...",
	}
	mock := &mockClient{loggedIn: true, debugInfo: info}
	srv := serve(newTestHandler(mock))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/debug-page")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	var got eversports.PageDebugInfo
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.URL != info.URL {
		t.Errorf("URL: want %q, got %q", info.URL, got.URL)
	}
}

func TestDebugPage_Error(t *testing.T) {
	mock := &mockClient{loggedIn: true, debugErr: errors.New("redirect detected")}
	srv := serve(newTestHandler(mock))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/debug-page")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

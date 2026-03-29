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
// what each method returns. Authentication is handled by the real client;
// the mock always behaves as if already logged in.
type mockClient struct {
	bookings      []eversports.Booking
	bookingsErr   error
	match         *eversports.Booking
	matchErr      error
	lastMatchID   string // records the matchID passed to GetMatchByID
	debugInfo     *eversports.PageDebugInfo
	debugErr      error
	slots         []eversports.Slot
	slotsErr      error
	lastSlotsDate string // records the startDate passed to GetSlots
}

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

func (m *mockClient) GetSlots(_ context.Context, _ string, _ []string, date string) ([]eversports.Slot, error) {
	m.lastSlotsDate = date
	return m.slots, m.slotsErr
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

// ─── GET /api/v1/eversports/bookings ─────────────────────────────────────────

func TestGetBookings_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	bookings := []eversports.Booking{
		{ID: "booking-1", Start: now, End: now.Add(time.Hour), State: "ACCEPTED"},
	}
	mock := &mockClient{bookings: bookings}
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
	mock := &mockClient{bookingsErr: errors.New("network error")}
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

func TestGetMatch_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	match := &eversports.Booking{ID: "match-uuid", Start: now, End: now.Add(time.Hour), State: "ACCEPTED"}
	mock := &mockClient{match: match}
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
	mock := &mockClient{matchErr: errors.New("not found")}
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

func TestDebugPage_Success(t *testing.T) {
	info := &eversports.PageDebugInfo{
		URL:         "https://www.eversports.de/user/bookings",
		FinalURL:    "https://www.eversports.de/user/bookings",
		Status:      200,
		HasNextData: false,
		HTMLSnippet: "<html>...",
	}
	mock := &mockClient{debugInfo: info}
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
	mock := &mockClient{debugErr: errors.New("redirect detected")}
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

// ─── GET /api/v1/eversports/games ────────────────────────────────────────────

func newCourtBookingsHandler(mock *mockClient, facilityID string, courtIDs []string) *Handler {
	return &Handler{eversports: mock, logger: testLogger, version: "test-version", facilityID: facilityID, courtIDs: courtIDs}
}

func TestGetCourtBookings_NotConfigured(t *testing.T) {
	// No facilityID or courtIDs set — server misconfiguration, not a client error.
	srv := serve(newTestHandler(&mockClient{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/games?date=2026-04-07")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", resp.StatusCode)
	}
}

func TestGetCourtBookings_MissingDate(t *testing.T) {
	srv := serve(newCourtBookingsHandler(&mockClient{}, "76443", []string{"77385"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/games")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestGetCourtBookings_InvalidDate(t *testing.T) {
	srv := serve(newCourtBookingsHandler(&mockClient{}, "76443", []string{"77385"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/games?date=not-a-date")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestGetCourtBookings_Success(t *testing.T) {
	bookingID := 135271816
	slots := []eversports.Slot{
		{Date: "2026-04-07", Start: "2045", Court: 77392, IsUserBookingOwner: true, Booking: &bookingID},
	}
	mock := &mockClient{slots: slots}
	srv := serve(newCourtBookingsHandler(mock, "76443", []string{"77385", "77392"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/games?date=2026-04-07")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	var got []eversports.Slot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("slots count: want 1, got %d", len(got))
	}
	if got[0].Start != "2045" {
		t.Errorf("Start: want %q, got %q", "2045", got[0].Start)
	}
	if mock.lastSlotsDate != "2026-04-07" {
		t.Errorf("forwarded date: want %q, got %q", "2026-04-07", mock.lastSlotsDate)
	}
}

func TestGetCourtBookings_Error(t *testing.T) {
	mock := &mockClient{slotsErr: errors.New("API error")}
	srv := serve(newCourtBookingsHandler(mock, "76443", []string{"77385"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/games?date=2026-04-07")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

func TestGetCourtBookings_FilterByDate(t *testing.T) {
	// Eversports can return slots for dates beyond the requested one; only the
	// requested date should be included in the response.
	slots := []eversports.Slot{
		{Date: "2026-04-07", Start: "1830", Court: 77385},
		{Date: "2026-04-08", Start: "1830", Court: 77385},
		{Date: "2026-04-09", Start: "1830", Court: 77385},
	}
	mock := &mockClient{slots: slots}
	srv := serve(newCourtBookingsHandler(mock, "76443", []string{"77385"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/games?date=2026-04-07")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var got []eversports.Slot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 slot for 2026-04-07, got %d", len(got))
	}
	if got[0].Date != "2026-04-07" {
		t.Errorf("Date: want %q, got %q", "2026-04-07", got[0].Date)
	}
}

func TestGetCourtBookings_InvertedTimeRange(t *testing.T) {
	srv := serve(newCourtBookingsHandler(&mockClient{}, "76443", []string{"77385"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/games?date=2026-04-07&startTime=2000&endTime=1830")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for inverted range, got %d", resp.StatusCode)
	}
}

func TestGetCourtBookings_InvalidStartTime(t *testing.T) {
	srv := serve(newCourtBookingsHandler(&mockClient{}, "76443", []string{"77385"}))
	defer srv.Close()

	for _, bad := range []string{"abc", "25:00", "99", "2500"} {
		resp, err := http.Get(srv.URL + "/api/v1/eversports/games?date=2026-04-07&startTime=" + bad)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("startTime=%q: want 400, got %d", bad, resp.StatusCode)
		}
	}
}

func TestGetCourtBookings_FilterByStartTime(t *testing.T) {
	slots := []eversports.Slot{
		{Date: "2026-04-07", Start: "1700", Court: 77385},
		{Date: "2026-04-07", Start: "1830", Court: 77385},
		{Date: "2026-04-07", Start: "2000", Court: 77385},
	}
	mock := &mockClient{slots: slots}
	srv := serve(newCourtBookingsHandler(mock, "76443", []string{"77385"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/games?date=2026-04-07&startTime=1830")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	var got []eversports.Slot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 slots (1830, 2000), got %d", len(got))
	}
	if got[0].Start != "1830" || got[1].Start != "2000" {
		t.Errorf("unexpected starts: %v", []string{got[0].Start, got[1].Start})
	}
}

func TestGetCourtBookings_FilterByEndTime(t *testing.T) {
	slots := []eversports.Slot{
		{Date: "2026-04-07", Start: "1700", Court: 77385},
		{Date: "2026-04-07", Start: "1830", Court: 77385},
		{Date: "2026-04-07", Start: "2000", Court: 77385},
	}
	mock := &mockClient{slots: slots}
	srv := serve(newCourtBookingsHandler(mock, "76443", []string{"77385"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/games?date=2026-04-07&endTime=1830")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var got []eversports.Slot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 slots (1700, 1830), got %d", len(got))
	}
}

func TestGetCourtBookings_InvalidMyParam(t *testing.T) {
	srv := serve(newCourtBookingsHandler(&mockClient{}, "76443", []string{"77385"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/games?date=2026-04-07&my=yes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for invalid my param, got %d", resp.StatusCode)
	}
}

func TestGetCourtBookings_FilterByMyTrue(t *testing.T) {
	bookingID := 135271816
	slots := []eversports.Slot{
		{Date: "2026-04-07", Start: "2045", Court: 77389, IsUserBookingOwner: true, Booking: &bookingID},
		{Date: "2026-04-07", Start: "2045", Court: 77391, IsUserBookingOwner: false},
		{Date: "2026-04-07", Start: "2045", Court: 77392, IsUserBookingOwner: true, Booking: &bookingID},
	}
	mock := &mockClient{slots: slots}
	srv := serve(newCourtBookingsHandler(mock, "76443", []string{"77389", "77391", "77392"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/games?date=2026-04-07&my=true")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	var got []eversports.Slot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 owned slots, got %d", len(got))
	}
	for _, s := range got {
		if !s.IsUserBookingOwner {
			t.Errorf("slot %d should be owned by user", s.Court)
		}
	}
}

func TestGetCourtBookings_FilterByMyFalse(t *testing.T) {
	bookingID := 135271816
	slots := []eversports.Slot{
		{Date: "2026-04-07", Start: "2045", Court: 77389, IsUserBookingOwner: true, Booking: &bookingID},
		{Date: "2026-04-07", Start: "2045", Court: 77391, IsUserBookingOwner: false},
		{Date: "2026-04-07", Start: "2045", Court: 77392, IsUserBookingOwner: false},
	}
	mock := &mockClient{slots: slots}
	srv := serve(newCourtBookingsHandler(mock, "76443", []string{"77389", "77391", "77392"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/games?date=2026-04-07&my=false")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var got []eversports.Slot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 non-owned slots, got %d", len(got))
	}
}

func TestGetCourtBookings_FilterByTimeRange(t *testing.T) {
	slots := []eversports.Slot{
		{Date: "2026-04-07", Start: "1700", Court: 77385},
		{Date: "2026-04-07", Start: "1830", Court: 77385},
		{Date: "2026-04-07", Start: "2000", Court: 77385},
		{Date: "2026-04-07", Start: "2130", Court: 77385},
	}
	mock := &mockClient{slots: slots}
	srv := serve(newCourtBookingsHandler(mock, "76443", []string{"77385"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/games?date=2026-04-07&startTime=1830&endTime=2000")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var got []eversports.Slot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 slots (1830, 2000), got %d", len(got))
	}
	if got[0].Start != "1830" || got[1].Start != "2000" {
		t.Errorf("unexpected starts: %v", []string{got[0].Start, got[1].Start})
	}
}

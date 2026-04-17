package booking

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
)

// testLogger silences all log output during tests.
var testLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))

// mockClient implements eversportsClient for tests. Fields control
// what each method returns. Authentication is handled by the real client;
// the mock always behaves as if already logged in.
type mockClient struct {
	match             *eversports.Booking
	matchErr          error
	lastMatchID       string // records the matchID passed to GetMatchByID
	cancellation      *eversports.CancellationResult
	cancellationErr   error
	lastCancelMatchID string // records the matchID passed to CancelMatch
	booking           *eversports.BookingResult
	bookingErr        error
	lastBookingArgs   struct {
		courtUUID string
		sportUUID string
		start     time.Time
		end       time.Time
	}
	slots            []eversports.Slot
	slotsErr         error
	lastSlotsDate    string // records the startDate passed to GetSlots
	courts           []eversports.Court
	courtsErr        error
	lastCourtsDate   string // records the date passed to GetCourts
	facility         *eversports.Facility
	facilityErr      error
	lastFacilitySlug string
}

func (m *mockClient) GetMatchByID(_ context.Context, matchID string) (*eversports.Booking, error) {
	m.lastMatchID = matchID
	return m.match, m.matchErr
}

func (m *mockClient) CancelMatch(_ context.Context, matchID string) (*eversports.CancellationResult, error) {
	m.lastCancelMatchID = matchID
	return m.cancellation, m.cancellationErr
}

func (m *mockClient) CreateBooking(_ context.Context, _, courtUUID, sportUUID string, start, end time.Time) (*eversports.BookingResult, error) {
	m.lastBookingArgs.courtUUID = courtUUID
	m.lastBookingArgs.sportUUID = sportUUID
	m.lastBookingArgs.start = start
	m.lastBookingArgs.end = end
	return m.booking, m.bookingErr
}

func (m *mockClient) GetSlots(_ context.Context, _ string, _ []string, date string) ([]eversports.Slot, error) {
	m.lastSlotsDate = date
	return m.slots, m.slotsErr
}

func (m *mockClient) GetCourts(_ context.Context, _, _, _, _, _, _, date string) ([]eversports.Court, error) {
	m.lastCourtsDate = date
	return m.courts, m.courtsErr
}

func (m *mockClient) GetFacility(_ context.Context, slug string) (*eversports.Facility, error) {
	m.lastFacilitySlug = slug
	return m.facility, m.facilityErr
}

// newTestHandler creates a Handler backed by the given mock.
func newTestHandler(mock *mockClient) *Handler {
	return &Handler{eversports: mock, logger: testLogger, version: "test-version"}
}

// newBookingHandler creates a Handler with facilityUUID configured.
func newBookingHandler(mock *mockClient) *Handler {
	return &Handler{
		eversports:   mock,
		logger:       testLogger,
		version:      "test-version",
		facilityUUID: "6266968c-b0fd-4115-ad3b-ae225cc880f1",
	}
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

// ─── DELETE /api/v1/eversports/matches/{id} ──────────────────────────────────

func TestCancelMatch_Success(t *testing.T) {
	result := &eversports.CancellationResult{
		ID:           "728e8066-4100-4dc4-81bb-9b9660b30fe6",
		State:        "CANCELLED",
		RelativeLink: "/match/728e8066-4100-4dc4-81bb-9b9660b30fe6",
	}
	mock := &mockClient{cancellation: result}
	srv := serve(newTestHandler(mock))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/eversports/matches/728e8066-4100-4dc4-81bb-9b9660b30fe6", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	var got eversports.CancellationResult
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != result.ID {
		t.Errorf("ID: want %q, got %q", result.ID, got.ID)
	}
	if got.State != "CANCELLED" {
		t.Errorf("State: want %q, got %q", "CANCELLED", got.State)
	}
	if mock.lastCancelMatchID != "728e8066-4100-4dc4-81bb-9b9660b30fe6" {
		t.Errorf("forwarded matchID: want %q, got %q", "728e8066-4100-4dc4-81bb-9b9660b30fe6", mock.lastCancelMatchID)
	}
}

func TestCancelMatch_Error(t *testing.T) {
	mock := &mockClient{cancellationErr: errors.New("cannot cancel past booking")}
	srv := serve(newTestHandler(mock))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/eversports/matches/bad-id", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

func TestCancelMatch_WithCredentials_UsesCredClient(t *testing.T) {
	// The default client must NOT be called; only the per-credential client.
	defaultMock := &mockClient{cancellationErr: errors.New("default client must not be used")}
	credMock := &mockClient{cancellation: &eversports.CancellationResult{
		ID:    "match-abc",
		State: "CANCELLED",
	}}

	h := newTestHandler(defaultMock)
	h.credClients.Store("user@example.com", eversportsClient(credMock))
	srv := serve(h)
	defer srv.Close()

	body := strings.NewReader(`{"email":"user@example.com","password":"secret"}`)
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/eversports/matches/match-abc", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	if credMock.lastCancelMatchID != "match-abc" {
		t.Errorf("credential client not called: lastCancelMatchID = %q", credMock.lastCancelMatchID)
	}
	if defaultMock.lastCancelMatchID != "" {
		t.Error("default client must not have been called")
	}
}

func TestGetMatch_WithCredentialHeaders_UsesCredClient(t *testing.T) {
	// The default client must NOT be called; only the per-credential client.
	defaultMock := &mockClient{matchErr: errors.New("default client must not be used")}
	credMock := &mockClient{match: &eversports.Booking{ID: "match-xyz"}}

	h := newTestHandler(defaultMock)
	h.credClients.Store("user@example.com", eversportsClient(credMock))
	srv := serve(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/eversports/matches/match-xyz", nil)
	req.Header.Set("X-Eversports-Email", "user@example.com")
	req.Header.Set("X-Eversports-Password", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	if credMock.lastMatchID != "match-xyz" {
		t.Errorf("credential client not called: lastMatchID = %q", credMock.lastMatchID)
	}
	if defaultMock.lastMatchID != "" {
		t.Error("default client must not have been called")
	}
}

// ─── POST /api/v1/eversports/matches ─────────────────────────────────────────

func TestCreateMatch_Success(t *testing.T) {
	result := &eversports.BookingResult{
		BookingUUID: "68a0a8ed-7b71-4083-ae0b-1e99349582a6",
		BookingID:   135414366,
	}
	mock := &mockClient{booking: result}
	srv := serve(newBookingHandler(mock))
	defer srv.Close()

	body := `{"courtUuid":"32ef2369-cf50-427f-8bdf-d380189584e8","start":"2026-04-12T06:45:00Z","end":"2026-04-12T07:30:00Z"}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/eversports/matches", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("want 201, got %d", resp.StatusCode)
	}
	var got eversports.BookingResult
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.BookingUUID != result.BookingUUID {
		t.Errorf("bookingUuid: want %q, got %q", result.BookingUUID, got.BookingUUID)
	}
	if got.BookingID != result.BookingID {
		t.Errorf("bookingId: want %d, got %d", result.BookingID, got.BookingID)
	}
	if mock.lastBookingArgs.courtUUID != "32ef2369-cf50-427f-8bdf-d380189584e8" {
		t.Errorf("courtUuid forwarded: want %q, got %q", "32ef2369-cf50-427f-8bdf-d380189584e8", mock.lastBookingArgs.courtUUID)
	}
	if mock.lastBookingArgs.sportUUID != squashSportUUID {
		t.Errorf("sportUUID forwarded: want %q, got %q", squashSportUUID, mock.lastBookingArgs.sportUUID)
	}
}

func TestCreateMatch_NotConfigured(t *testing.T) {
	srv := serve(newTestHandler(&mockClient{}))
	defer srv.Close()

	body := `{"courtUuid":"32ef2369-cf50-427f-8bdf-d380189584e8","start":"2026-04-12T06:45:00Z","end":"2026-04-12T07:30:00Z"}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/eversports/matches", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", resp.StatusCode)
	}
}

func TestCreateMatch_MissingCourtUUID(t *testing.T) {
	srv := serve(newBookingHandler(&mockClient{}))
	defer srv.Close()

	body := `{"start":"2026-04-12T06:45:00Z","end":"2026-04-12T07:30:00Z"}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/eversports/matches", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestCreateMatch_InvalidTime(t *testing.T) {
	srv := serve(newBookingHandler(&mockClient{}))
	defer srv.Close()

	for _, tc := range []struct{ body string }{
		{`{"courtUuid":"abc","start":"not-a-time","end":"2026-04-12T07:30:00Z"}`},
		{`{"courtUuid":"abc","start":"2026-04-12T06:45:00Z","end":"not-a-time"}`},
		{`{"courtUuid":"abc","start":"2026-04-12T07:30:00Z","end":"2026-04-12T06:45:00Z"}`}, // end before start
	} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/eversports/matches", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q: want 400, got %d", tc.body, resp.StatusCode)
		}
	}
}

func TestCreateMatch_Error(t *testing.T) {
	mock := &mockClient{bookingErr: errors.New("slot already taken")}
	srv := serve(newBookingHandler(mock))
	defer srv.Close()

	body := `{"courtUuid":"32ef2369-cf50-427f-8bdf-d380189584e8","start":"2026-04-12T06:45:00Z","end":"2026-04-12T07:30:00Z"}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/eversports/matches", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

// ─── GET /api/v1/eversports/matches ──────────────────────────────────────────

func newCourtBookingsHandler(mock *mockClient, facilityID string) *Handler {
	return &Handler{
		eversports:   mock,
		logger:       testLogger,
		version:      "test-version",
		facilityID:   facilityID,
		facilitySlug: "squash-house-berlin-03",
	}
}

func TestGetCourtBookings_NotConfigured(t *testing.T) {
	// Missing any required field → 500 (server misconfiguration, not a client error).
	for _, h := range []*Handler{
		{eversports: &mockClient{}, logger: testLogger},                      // all missing
		{eversports: &mockClient{}, logger: testLogger, facilitySlug: "s"},   // facilityID missing
		{eversports: &mockClient{}, logger: testLogger, facilityID: "76443"}, // facilitySlug missing
	} {
		srv := serve(h)
		resp, err := http.Get(srv.URL + "/api/v1/eversports/matches?date=2026-04-07")
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("want 500 when config incomplete, got %d", resp.StatusCode)
		}
	}
}

func TestGetCourtBookings_MissingDate(t *testing.T) {
	srv := serve(newCourtBookingsHandler(&mockClient{}, "76443"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestGetCourtBookings_InvalidDate(t *testing.T) {
	srv := serve(newCourtBookingsHandler(&mockClient{}, "76443"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches?date=not-a-date")
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
	srv := serve(newCourtBookingsHandler(mock, "76443"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches?date=2026-04-07")
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
		t.Errorf("forwarded date to GetSlots: want %q, got %q", "2026-04-07", mock.lastSlotsDate)
	}
	if mock.lastCourtsDate != "2026-04-07" {
		t.Errorf("forwarded date to GetCourts: want %q, got %q", "2026-04-07", mock.lastCourtsDate)
	}
}

func TestGetCourtBookings_Error(t *testing.T) {
	mock := &mockClient{slotsErr: errors.New("API error")}
	srv := serve(newCourtBookingsHandler(mock, "76443"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches?date=2026-04-07")
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
	srv := serve(newCourtBookingsHandler(mock, "76443"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches?date=2026-04-07")
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
	srv := serve(newCourtBookingsHandler(&mockClient{}, "76443"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches?date=2026-04-07&startTime=2000&endTime=1830")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for inverted range, got %d", resp.StatusCode)
	}
}

func TestGetCourtBookings_InvalidStartTime(t *testing.T) {
	srv := serve(newCourtBookingsHandler(&mockClient{}, "76443"))
	defer srv.Close()

	for _, bad := range []string{"abc", "25:00", "99", "2500"} {
		resp, err := http.Get(srv.URL + "/api/v1/eversports/matches?date=2026-04-07&startTime=" + bad)
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
	srv := serve(newCourtBookingsHandler(mock, "76443"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches?date=2026-04-07&startTime=1830")
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
	srv := serve(newCourtBookingsHandler(mock, "76443"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches?date=2026-04-07&endTime=1830")
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
	srv := serve(newCourtBookingsHandler(&mockClient{}, "76443"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches?date=2026-04-07&my=yes")
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
	srv := serve(newCourtBookingsHandler(mock, "76443"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches?date=2026-04-07&my=true")
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
	srv := serve(newCourtBookingsHandler(mock, "76443"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches?date=2026-04-07&my=false")
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
	srv := serve(newCourtBookingsHandler(mock, "76443"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/matches?date=2026-04-07&startTime=1830&endTime=2000")
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

// ─── GET /api/v1/eversports/courts ───────────────────────────────────────────

func newCourtsHandler(mock *mockClient) *Handler {
	return &Handler{
		eversports:   mock,
		logger:       testLogger,
		version:      "test-version",
		facilityID:   "76443",
		facilitySlug: "squash-house-berlin-03",
	}
}

func TestGetCourts_NotConfigured(t *testing.T) {
	// Missing required fields — should return 500.
	for _, h := range []*Handler{
		{eversports: &mockClient{}, logger: testLogger},                      // all missing
		{eversports: &mockClient{}, logger: testLogger, facilitySlug: "s"},   // facilityID missing
		{eversports: &mockClient{}, logger: testLogger, facilityID: "76443"}, // facilitySlug missing
	} {
		srv := serve(h)
		resp, err := http.Get(srv.URL + "/api/v1/eversports/courts")
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("want 500 when config incomplete, got %d", resp.StatusCode)
		}
	}
}

func TestGetCourts_Success(t *testing.T) {
	courts := []eversports.Court{
		{ID: "77385", UUID: "32ef2369-cf50-427f-8bdf-d380189584e8", Name: "Court 1"},
		{ID: "77386", UUID: "aa2a1234-0000-0000-0000-000000000001", Name: "Court 2"},
	}
	mock := &mockClient{courts: courts}
	srv := serve(newCourtsHandler(mock))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/courts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	var got []eversports.Court
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("courts count: want 2, got %d", len(got))
	}
	if got[0].ID != "77385" || got[0].Name != "Court 1" || got[0].UUID != "32ef2369-cf50-427f-8bdf-d380189584e8" {
		t.Errorf("first court: unexpected value %+v", got[0])
	}
}

func TestGetCourts_Error(t *testing.T) {
	mock := &mockClient{courtsErr: errors.New("parse failed")}
	srv := serve(newCourtsHandler(mock))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/courts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

func TestGetCourts_WithDate(t *testing.T) {
	courts := []eversports.Court{{ID: "77385", UUID: "32ef2369-cf50-427f-8bdf-d380189584e8", Name: "Court 1"}}
	mock := &mockClient{courts: courts}
	srv := serve(newCourtsHandler(mock))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/courts?date=2026-05-01")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	var got []eversports.Court
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("courts count: want 1, got %d", len(got))
	}
	if mock.lastCourtsDate != "2026-05-01" {
		t.Errorf("forwarded date to GetCourts: want %q, got %q", "2026-05-01", mock.lastCourtsDate)
	}
}

func TestGetCourts_InvalidDate(t *testing.T) {
	srv := serve(newCourtsHandler(&mockClient{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/courts?date=not-a-date")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

// ─── GET /api/v1/eversports/facility ─────────────────────────────────────────

func TestGetFacility_MissingSlug(t *testing.T) {
	srv := serve(newTestHandler(&mockClient{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/facility")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestGetFacility_SlugForwarded(t *testing.T) {
	facility := &eversports.Facility{ID: "1234", Slug: "squash-house-berlin-03", Name: "Squash House Berlin"}
	mock := &mockClient{facility: facility}
	srv := serve(newTestHandler(mock))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/facility?slug=squash-house-berlin-03")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	var got eversports.Facility
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Slug != "squash-house-berlin-03" {
		t.Errorf("Slug: want %q, got %q", "squash-house-berlin-03", got.Slug)
	}
	if mock.lastFacilitySlug != "squash-house-berlin-03" {
		t.Errorf("forwarded slug: want %q, got %q", "squash-house-berlin-03", mock.lastFacilitySlug)
	}
}

func TestGetFacility_NotFound(t *testing.T) {
	mock := &mockClient{facilityErr: eversports.ErrNotFound}
	srv := serve(newTestHandler(mock))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/eversports/facility?slug=unknown-slug")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

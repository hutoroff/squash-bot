package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
)

// eversportsClient is the subset of *eversports.Client methods used by Handler.
// Defined as an interface to allow test doubles.
// Authentication is handled automatically by the client implementation.
type eversportsClient interface {
	GetMatchByID(ctx context.Context, matchID string) (*eversports.Booking, error)
	CancelMatch(ctx context.Context, matchID string) (*eversports.CancellationResult, error)
	CreateBooking(ctx context.Context, facilityUUID, courtUUID, sportUUID string, start, end time.Time) (*eversports.BookingResult, error)
	GetSlots(ctx context.Context, facilityID string, courtIDs []string, startDate string) ([]eversports.Slot, error)
	GetCourts(ctx context.Context, facilityID, facilitySlug, sportID, sportSlug, sportName, sportUUID, date string) ([]eversports.Court, error)
	GetFacility(ctx context.Context, slug string) (*eversports.Facility, error)
}

// Sport constants for squash (this service is squash-specific).
const (
	squashSportUUID = "b388b6e6-69de-11e8-bdc6-02bd505aa7b2"
	squashSportSlug = "squash"
	squashSportName = "Squash"
	squashSportID   = "496"
)

// Handler wires all HTTP routes for the booking service.
type Handler struct {
	eversports   eversportsClient
	logger       *slog.Logger
	version      string
	facilityID   string
	facilityUUID string
	facilitySlug string
}

func NewHandler(es eversportsClient, logger *slog.Logger, version, facilityID, facilityUUID, facilitySlug string) *Handler {
	return &Handler{
		eversports:   es,
		logger:       logger,
		version:      version,
		facilityID:   facilityID,
		facilityUUID: facilityUUID,
		facilitySlug: facilitySlug,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /version", h.getVersion)

	mux.HandleFunc("GET /api/v1/eversports/matches", h.listMatches)
	mux.HandleFunc("POST /api/v1/eversports/matches", h.createMatch)
	mux.HandleFunc("GET /api/v1/eversports/matches/{id}", h.getMatch)
	mux.HandleFunc("DELETE /api/v1/eversports/matches/{id}", h.cancelMatch)
	mux.HandleFunc("GET /api/v1/eversports/courts", h.getCourts)
	mux.HandleFunc("GET /api/v1/eversports/facility", h.getFacility)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok")) //nolint:errcheck
}

func (h *Handler) getVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": h.version})
}

func (h *Handler) getMatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "match id is required")
		return
	}
	match, err := h.eversports.GetMatchByID(r.Context(), id)
	if err != nil {
		h.logger.Error("eversports get match failed", "id", id, "err", err)
		writeError(w, http.StatusBadGateway, "get match failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, match)
}

// createMatchRequest is the JSON body expected by POST /api/v1/eversports/matches.
type createMatchRequest struct {
	CourtUUID string `json:"courtUuid"`
	Start     string `json:"start"` // RFC 3339, e.g. "2026-04-12T06:45:00Z"
	End       string `json:"end"`   // RFC 3339
}

func (h *Handler) createMatch(w http.ResponseWriter, r *http.Request) {
	if h.facilityUUID == "" {
		writeError(w, http.StatusInternalServerError, "booking creation requires EVERSPORTS_FACILITY_UUID to be configured")
		return
	}

	var req createMatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.CourtUUID == "" {
		writeError(w, http.StatusBadRequest, "courtUuid is required")
		return
	}
	start, err := time.Parse(time.RFC3339, req.Start)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid start time (expected RFC 3339): "+err.Error())
		return
	}
	end, err := time.Parse(time.RFC3339, req.End)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid end time (expected RFC 3339): "+err.Error())
		return
	}
	if !end.After(start) {
		writeError(w, http.StatusBadRequest, "end must be after start")
		return
	}

	h.logger.Debug("booking: create match request",
		"facilityUuid", h.facilityUUID,
		"courtUuid", req.CourtUUID,
		"start_raw", req.Start,
		"end_raw", req.End,
		"start_parsed", start.Format(time.RFC3339),
		"end_parsed", end.Format(time.RFC3339),
		"start_offset", start.Format("-07:00"),
	)
	result, err := h.eversports.CreateBooking(r.Context(), h.facilityUUID, req.CourtUUID, squashSportUUID, start, end)
	if err != nil {
		h.logger.Error("eversports create booking failed",
			"facilityUuid", h.facilityUUID,
			"courtUuid", req.CourtUUID,
			"start", req.Start,
			"err", err,
		)
		writeError(w, http.StatusBadGateway, "create booking failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (h *Handler) cancelMatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "match id is required")
		return
	}
	result, err := h.eversports.CancelMatch(r.Context(), id)
	if err != nil {
		h.logger.Error("eversports cancel match failed", "id", id, "err", err)
		writeError(w, http.StatusBadGateway, "cancel match failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// listMatches returns court reservations for the given date from the
// Eversports /api/slot endpoint. Each item in the response is a time slot on a
// specific court; slots where booking != null are already reserved.
// Requires ?date=YYYY-MM-DD.
// Optional ?startTime=HHMM and ?endTime=HHMM restrict to a time window (inclusive).
// Optional ?my=true|false filters by whether the authenticated user owns the reservation.
// EVERSPORTS_FACILITY_ID, EVERSPORTS_FACILITY_SLUG, EVERSPORTS_SPORT_ID, and
// EVERSPORTS_SPORT_UUID must be configured (same requirements as /courts).
func (h *Handler) listMatches(w http.ResponseWriter, r *http.Request) {
	if h.facilityID == "" || h.facilitySlug == "" {
		writeError(w, http.StatusInternalServerError, "games endpoint requires EVERSPORTS_FACILITY_ID and EVERSPORTS_FACILITY_SLUG to be configured")
		return
	}

	q := r.URL.Query()

	date := q.Get("date")
	if date == "" {
		writeError(w, http.StatusBadRequest, "date query parameter is required (format: YYYY-MM-DD)")
		return
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		writeError(w, http.StatusBadRequest, "invalid date format — expected YYYY-MM-DD")
		return
	}

	var startTime, endTime string
	if s := q.Get("startTime"); s != "" {
		if _, err := parseHHMM(s); err != nil {
			writeError(w, http.StatusBadRequest, "invalid startTime: "+err.Error())
			return
		}
		startTime = s
	}
	if s := q.Get("endTime"); s != "" {
		if _, err := parseHHMM(s); err != nil {
			writeError(w, http.StatusBadRequest, "invalid endTime: "+err.Error())
			return
		}
		endTime = s
	}

	if startTime != "" && endTime != "" && startTime > endTime {
		writeError(w, http.StatusBadRequest, "startTime must not be later than endTime")
		return
	}

	var myFilter *bool
	if s := q.Get("my"); s != "" {
		switch s {
		case "true":
			v := true
			myFilter = &v
		case "false":
			v := false
			myFilter = &v
		default:
			writeError(w, http.StatusBadRequest, `invalid "my" parameter — expected "true" or "false"`)
			return
		}
	}

	courts, err := h.eversports.GetCourts(r.Context(), h.facilityID, h.facilitySlug, squashSportID, squashSportSlug, squashSportName, squashSportUUID, date)
	if err != nil {
		h.logger.Error("eversports get courts for games failed", "err", err)
		writeError(w, http.StatusBadGateway, "get courts failed: "+err.Error())
		return
	}
	courtIDs := make([]string, len(courts))
	courtUUIDByID := make(map[string]string, len(courts))
	for i, c := range courts {
		courtIDs[i] = c.ID
		courtUUIDByID[c.ID] = c.UUID
	}

	slots, err := h.eversports.GetSlots(r.Context(), h.facilityID, courtIDs, date)
	if err != nil {
		h.logger.Error("eversports get court bookings failed", "date", date, "err", err)
		writeError(w, http.StatusBadGateway, "get court bookings failed: "+err.Error())
		return
	}

	slots = filterSlots(slots, date, startTime, endTime, myFilter)

	// Enrich each slot with the court UUID so callers can book without a separate /courts call.
	type slotWithCourtUUID struct {
		eversports.Slot
		CourtUUID string `json:"courtUuid,omitempty"`
	}
	enriched := make([]slotWithCourtUUID, len(slots))
	for i, s := range slots {
		courtKey := strconv.Itoa(s.Court)
		enriched[i] = slotWithCourtUUID{
			Slot:      s,
			CourtUUID: courtUUIDByID[courtKey],
		}
	}

	writeJSON(w, http.StatusOK, enriched)
}

// parseHHMM validates and returns a 4-digit HHMM time string (e.g. "1830").
func parseHHMM(s string) (string, error) {
	if len(s) != 4 {
		return "", fmt.Errorf("expected 4 digits (HHMM), got %q", s)
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("expected 4 digits (HHMM), got %q", s)
		}
	}
	h, _ := strconv.Atoi(s[:2])
	m, _ := strconv.Atoi(s[2:])
	if h > 23 || m > 59 {
		return "", fmt.Errorf("time out of range: %q", s)
	}
	return s, nil
}

// filterSlots applies all active filters to the slot list.
// date is always matched. Empty startTime/endTime means no time bound.
// nil myFilter means no ownership filter.
// Time comparison is lexicographic — valid because Start is zero-padded HHMM.
func filterSlots(slots []eversports.Slot, date, startTime, endTime string, myFilter *bool) []eversports.Slot {
	out := slots[:0]
	for _, s := range slots {
		if s.Date != date {
			continue
		}
		if startTime != "" && s.Start < startTime {
			continue
		}
		if endTime != "" && s.Start > endTime {
			continue
		}
		if myFilter != nil && s.IsUserBookingOwner != *myFilter {
			continue
		}
		out = append(out, s)
	}
	return out
}

// getCourts returns the list of courts at the configured facility by
// parsing the Eversports booking calendar HTML.
// Requires EVERSPORTS_FACILITY_ID and EVERSPORTS_FACILITY_SLUG to be configured.
// Optional ?date=YYYY-MM-DD; defaults to today.
func (h *Handler) getCourts(w http.ResponseWriter, r *http.Request) {
	if h.facilityID == "" || h.facilitySlug == "" {
		writeError(w, http.StatusInternalServerError, "courts endpoint requires EVERSPORTS_FACILITY_ID and EVERSPORTS_FACILITY_SLUG to be configured")
		return
	}
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", date); err != nil {
		writeError(w, http.StatusBadRequest, "invalid date format — expected YYYY-MM-DD")
		return
	}
	courts, err := h.eversports.GetCourts(r.Context(), h.facilityID, h.facilitySlug, squashSportID, squashSportSlug, squashSportName, squashSportUUID, date)
	if err != nil {
		h.logger.Error("eversports get courts failed", "err", err)
		writeError(w, http.StatusBadGateway, "get courts failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, courts)
}

// getFacility returns the venue profile for a given facility slug.
// Requires ?slug= query parameter.
func (h *Handler) getFacility(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug query parameter is required")
		return
	}
	facility, err := h.eversports.GetFacility(r.Context(), slug)
	if err != nil {
		if errors.Is(err, eversports.ErrNotFound) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("facility %q not found", slug))
			return
		}
		h.logger.Error("eversports get facility failed", "slug", slug, "err", err)
		writeError(w, http.StatusBadGateway, "get facility failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, facility)
}

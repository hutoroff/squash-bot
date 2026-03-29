package booking

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/vkhutorov/squash_bot/internal/eversports"
)

// eversportsClient is the subset of *eversports.Client methods used by Handler.
// Defined as an interface to allow test doubles.
type eversportsClient interface {
	Login(ctx context.Context) error
	IsLoggedIn() bool
	GetBookings(ctx context.Context) ([]eversports.Booking, error)
	GetMatchByID(ctx context.Context, matchID string) (*eversports.Booking, error)
	FetchPageDebugInfo(ctx context.Context) (*eversports.PageDebugInfo, error)
	GetSlots(ctx context.Context, facilityID string, courtIDs []string, startDate string) ([]eversports.Slot, error)
}

// Handler wires all HTTP routes for the sports-booking-service.
type Handler struct {
	eversports eversportsClient
	logger     *slog.Logger
	version    string
	facilityID string
	courtIDs   []string
}

func NewHandler(es *eversports.Client, logger *slog.Logger, version, facilityID string, courtIDs []string) *Handler {
	return &Handler{
		eversports: es,
		logger:     logger,
		version:    version,
		facilityID: facilityID,
		courtIDs:   courtIDs,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /version", h.getVersion)

	mux.HandleFunc("POST /api/v1/eversports/login", h.login)
	mux.HandleFunc("GET /api/v1/eversports/bookings", h.getBookings)
	mux.HandleFunc("GET /api/v1/eversports/matches/{id}", h.getMatch)
	mux.HandleFunc("GET /api/v1/eversports/games", h.getGames)
	mux.HandleFunc("GET /api/v1/eversports/debug-page", h.debugPage)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok")) //nolint:errcheck
}

func (h *Handler) getVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": h.version})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if err := h.eversports.Login(r.Context()); err != nil {
		h.logger.Error("eversports login failed", "err", err)
		writeError(w, http.StatusBadGateway, "eversports login failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_in"})
}

func (h *Handler) getBookings(w http.ResponseWriter, r *http.Request) {
	if !h.eversports.IsLoggedIn() {
		writeError(w, http.StatusPreconditionFailed, "not logged in — call POST /api/v1/eversports/login first")
		return
	}

	bookings, err := h.eversports.GetBookings(r.Context())
	if err != nil {
		h.logger.Error("eversports get bookings failed", "err", err)
		writeError(w, http.StatusBadGateway, "eversports bookings failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, bookings)
}

func (h *Handler) getMatch(w http.ResponseWriter, r *http.Request) {
	if !h.eversports.IsLoggedIn() {
		writeError(w, http.StatusPreconditionFailed, "not logged in — call POST /api/v1/eversports/login first")
		return
	}
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

// getGames returns court reservations for the given date from the
// Eversports /api/slot endpoint. Each item in the response is a time slot on a
// specific court; slots where booking != null are already reserved.
// Requires ?date=YYYY-MM-DD.
// Optional ?startTime=HHMM and ?endTime=HHMM restrict to a time window (inclusive).
// Optional ?my=true|false filters by whether the authenticated user owns the reservation.
// EVERSPORTS_FACILITY_ID and EVERSPORTS_COURT_IDS must be configured.
func (h *Handler) getGames(w http.ResponseWriter, r *http.Request) {
	if !h.eversports.IsLoggedIn() {
		writeError(w, http.StatusPreconditionFailed, "not logged in — call POST /api/v1/eversports/login first")
		return
	}
	if h.facilityID == "" || len(h.courtIDs) == 0 {
		writeError(w, http.StatusInternalServerError, "games endpoint requires EVERSPORTS_FACILITY_ID and EVERSPORTS_COURT_IDS to be configured")
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

	slots, err := h.eversports.GetSlots(r.Context(), h.facilityID, h.courtIDs, date)
	if err != nil {
		h.logger.Error("eversports get court bookings failed", "date", date, "err", err)
		writeError(w, http.StatusBadGateway, "get court bookings failed: "+err.Error())
		return
	}

	slots = filterSlots(slots, date, startTime, endTime, myFilter)

	writeJSON(w, http.StatusOK, slots)
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

// debugPage fetches the configured bookings page and returns diagnostic
// information: the full __NEXT_DATA__ JSON if present, or the first 2000
// characters of raw HTML if not. Use this to inspect what the page actually
// returns and find the correct field paths for bookings data.
func (h *Handler) debugPage(w http.ResponseWriter, r *http.Request) {
	if !h.eversports.IsLoggedIn() {
		writeError(w, http.StatusPreconditionFailed, "not logged in — call POST /api/v1/eversports/login first")
		return
	}

	info, err := h.eversports.FetchPageDebugInfo(r.Context())
	if err != nil {
		h.logger.Error("eversports debug-page failed", "err", err)
		writeError(w, http.StatusBadGateway, "debug-page failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

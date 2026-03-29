package booking

import (
	"log/slog"
	"net/http"

	"github.com/vkhutorov/squash_bot/internal/eversports"
)

// Handler wires all HTTP routes for the sports-booking-service.
type Handler struct {
	eversports *eversports.Client
	logger     *slog.Logger
	version    string
}

func NewHandler(es *eversports.Client, logger *slog.Logger, version string) *Handler {
	return &Handler{
		eversports: es,
		logger:     logger,
		version:    version,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /version", h.getVersion)

	mux.HandleFunc("POST /api/v1/eversports/login", h.login)
	mux.HandleFunc("GET /api/v1/eversports/bookings", h.getBookings)
	mux.HandleFunc("GET /api/v1/eversports/matches/{id}", h.getMatch)
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

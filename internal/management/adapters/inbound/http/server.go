package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/hutoroff/squash-bot/internal/management/application/ports/inbound"
)

// Handler wires all HTTP routes for the management service.
type Handler struct {
	gameSvc        inbound.GameUseCases
	partSvc        inbound.ParticipationUseCases
	venueSvc       inbound.VenueUseCases
	venueCredSvc   inbound.VenueCredentialUseCases
	groupSvc       inbound.GroupUseCases
	playerSvc      inbound.PlayerUseCases
	scheduler      inbound.SchedulerUseCases
	auditSvc       inbound.AuditUseCases
	adminResolver  inbound.AdminGroupsResolver
	serverOwnerIDs map[int64]bool
	logger         *slog.Logger
	version        string
}

func NewHandler(
	gameSvc inbound.GameUseCases,
	partSvc inbound.ParticipationUseCases,
	venueSvc inbound.VenueUseCases,
	venueCredSvc inbound.VenueCredentialUseCases,
	groupSvc inbound.GroupUseCases,
	playerSvc inbound.PlayerUseCases,
	scheduler inbound.SchedulerUseCases,
	auditSvc inbound.AuditUseCases,
	adminResolver inbound.AdminGroupsResolver,
	serverOwnerIDs map[int64]bool,
	logger *slog.Logger,
	version string,
) *Handler {
	return &Handler{
		gameSvc:        gameSvc,
		partSvc:        partSvc,
		venueSvc:       venueSvc,
		venueCredSvc:   venueCredSvc,
		groupSvc:       groupSvc,
		playerSvc:      playerSvc,
		scheduler:      scheduler,
		auditSvc:       auditSvc,
		adminResolver:  adminResolver,
		serverOwnerIDs: serverOwnerIDs,
		logger:         logger,
		version:        version,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /version", h.getVersion)

	// Games
	mux.HandleFunc("POST /api/v1/games", h.createGame)
	mux.HandleFunc("GET /api/v1/games", h.listGames)
	mux.HandleFunc("GET /api/v1/games/{id}", h.getGame)
	mux.HandleFunc("PATCH /api/v1/games/{id}/message-id", h.updateMessageID)
	mux.HandleFunc("PATCH /api/v1/games/{id}/courts", h.updateCourts)
	// Participations
	mux.HandleFunc("POST /api/v1/games/{id}/join", h.joinGame)
	mux.HandleFunc("POST /api/v1/games/{id}/skip", h.skipGame)
	mux.HandleFunc("POST /api/v1/games/{id}/guests", h.addGuest)
	mux.HandleFunc("DELETE /api/v1/games/{id}/guests", h.removeGuest)
	mux.HandleFunc("GET /api/v1/games/{id}/participations", h.getParticipations)
	mux.HandleFunc("GET /api/v1/games/{id}/guests", h.getGuests)
	mux.HandleFunc("DELETE /api/v1/games/{id}/players/{telegramID}", h.kickPlayer)
	mux.HandleFunc("DELETE /api/v1/games/{id}/guests/{guestID}", h.kickGuest)

	// Players
	mux.HandleFunc("GET /api/v1/players/{telegramID}", h.getPlayerByTelegramID)
	mux.HandleFunc("GET /api/v1/players/{telegramID}/next-game", h.getNextGame)
	mux.HandleFunc("GET /api/v1/players/{playerID}/games", h.listPlayerGames)

	// Groups
	mux.HandleFunc("PUT /api/v1/groups/{chatID}", h.upsertGroup)
	mux.HandleFunc("PATCH /api/v1/groups/{chatID}/language", h.setGroupLanguage)
	mux.HandleFunc("PATCH /api/v1/groups/{chatID}/timezone", h.setGroupTimezone)
	mux.HandleFunc("PATCH /api/v1/groups/{chatID}/changelog", h.setGroupChangelog)
	mux.HandleFunc("DELETE /api/v1/groups/{chatID}", h.removeGroup)
	mux.HandleFunc("GET /api/v1/groups", h.listGroups)
	mux.HandleFunc("GET /api/v1/groups/{chatID}", h.getGroup)

	// Venues
	mux.HandleFunc("POST /api/v1/venues", h.createVenue)
	mux.HandleFunc("GET /api/v1/venues", h.listVenues)
	mux.HandleFunc("GET /api/v1/venues/{id}", h.getVenue)
	mux.HandleFunc("PATCH /api/v1/venues/{id}", h.updateVenue)
	mux.HandleFunc("DELETE /api/v1/venues/{id}", h.deleteVenue)

	// Venue credentials
	mux.HandleFunc("POST /api/v1/venues/{id}/credentials", h.addCredential)
	mux.HandleFunc("GET /api/v1/venues/{id}/credentials", h.listCredentials)
	mux.HandleFunc("DELETE /api/v1/venues/{id}/credentials/{cid}", h.removeCredential)
	mux.HandleFunc("GET /api/v1/venues/{id}/credentials/priorities", h.listCredentialPriorities)

	// Scheduler triggers
	mux.HandleFunc("POST /api/v1/scheduler/trigger/{event}", h.triggerScheduler)

	// Audit
	mux.HandleFunc("GET /api/v1/audit", h.listAuditEvents)
}

// NewServer builds an http.Server with the handler's routes registered.
func NewServer(addr string, h *Handler, secret string) *http.Server {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return &http.Server{
		Addr:         addr,
		Handler:      requireBearer(secret, mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

func requireBearer(secret string, next http.Handler) http.Handler {
	expected := []byte("Bearer " + secret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/version" {
			next.ServeHTTP(w, r)
			return
		}
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok")) //nolint:errcheck
}

func (h *Handler) getVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": h.version})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON: encode", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// Run starts the server and blocks until ctx is cancelled, then gracefully shuts down.
func Run(ctx context.Context, srv *http.Server, logger *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		logger.Info("HTTP server shutting down")
		return srv.Shutdown(shutCtx)
	}
}

func (h *Handler) isServerOwner(tgID int64) bool {
	return h.serverOwnerIDs[tgID]
}

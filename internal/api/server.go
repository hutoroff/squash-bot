package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/vkhutorov/squash_bot/internal/service"
	"github.com/vkhutorov/squash_bot/internal/storage"
)

// Handler wires all HTTP routes for the squash-games-management service.
type Handler struct {
	gameService  *service.GameService
	partService  *service.ParticipationService
	groupRepo    *storage.GroupRepo
	scheduler    *service.SchedulerService
	logger       *slog.Logger
}

func NewHandler(
	gameService *service.GameService,
	partService *service.ParticipationService,
	groupRepo *storage.GroupRepo,
	scheduler *service.SchedulerService,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		gameService: gameService,
		partService: partService,
		groupRepo:   groupRepo,
		scheduler:   scheduler,
		logger:      logger,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)

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
	mux.HandleFunc("GET /api/v1/players/{telegramID}/next-game", h.getNextGame)

	// Groups
	mux.HandleFunc("PUT /api/v1/groups/{chatID}", h.upsertGroup)
	mux.HandleFunc("DELETE /api/v1/groups/{chatID}", h.removeGroup)
	mux.HandleFunc("GET /api/v1/groups", h.listGroups)
	mux.HandleFunc("GET /api/v1/groups/{chatID}", h.getGroup)

	// Scheduler triggers
	mux.HandleFunc("POST /api/v1/scheduler/trigger/{event}", h.triggerScheduler)
}

// NewServer builds an http.Server with the handler's routes registered.
func NewServer(addr string, h *Handler) *http.Server {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok")) //nolint:errcheck
}

// writeJSON serialises v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON: encode", "err", err)
	}
}

// writeError writes {"error": msg} with the given status code.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON reads and decodes a JSON body into v.
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

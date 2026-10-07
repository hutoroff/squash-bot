package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/observability"
)

// defaultCredentialErrorCooldown is used when the caller does not pass an explicit cooldown.
const defaultCredentialErrorCooldown = 24 * time.Hour

// maxRequestBodyBytes bounds request bodies to guard against memory/CPU
// exhaustion from oversized payloads. 1 MiB is far larger than any real
// request this API handles.
const maxRequestBodyBytes = 1 << 20

// Handler wires all HTTP routes for the management service.
type Handler struct {
	featureFlags     *service.FeatureFlagService
	gameService      *service.GameService
	gameResultSvc    *service.GameResultService
	ratingService    *service.RatingService
	partService      *service.ParticipationService
	venueService     *service.VenueService
	venueCredService *service.VenueCredentialService
	groupRepo        service.GroupRepository
	playerRepo       *storage.PlayerRepo
	userRepo         userRepository
	auditSvc         *service.AuditService
	adminResolver    adminGroupsResolver
	logger           *slog.Logger
	version          string
	// credentialErrorCooldown is forwarded to GameService.BookGameCourts.
	credentialErrorCooldown time.Duration
	bookingIntents          *service.BookingIntentService
	bookingMetrics          interface {
		GetMetrics(context.Context) (*models.ManagementBookingMetrics, error)
	}
	reliableBooking *service.ReliableBookingExecutor
	bookingScope    bookingapi.Scope
	serviceRegistry *service.ServiceRegistryService
}

func NewHandler(
	gameService *service.GameService,
	gameResultSvc *service.GameResultService,
	ratingService *service.RatingService,
	partService *service.ParticipationService,
	venueService *service.VenueService,
	venueCredService *service.VenueCredentialService,
	groupRepo service.GroupRepository,
	playerRepo *storage.PlayerRepo,
	userRepo userRepository,
	auditSvc *service.AuditService,
	adminResolver adminGroupsResolver,
	logger *slog.Logger,
	version string,
	credentialErrorCooldown time.Duration,
) *Handler {
	if credentialErrorCooldown <= 0 {
		credentialErrorCooldown = defaultCredentialErrorCooldown
	}
	return &Handler{
		gameService:             gameService,
		gameResultSvc:           gameResultSvc,
		ratingService:           ratingService,
		partService:             partService,
		venueService:            venueService,
		venueCredService:        venueCredService,
		groupRepo:               groupRepo,
		playerRepo:              playerRepo,
		userRepo:                userRepo,
		auditSvc:                auditSvc,
		adminResolver:           adminResolver,
		logger:                  logger,
		version:                 version,
		credentialErrorCooldown: credentialErrorCooldown,
	}
}

func (h *Handler) SetServiceRegistry(registry *service.ServiceRegistryService) {
	h.serviceRegistry = registry
}

func (h *Handler) SetReliableBookingAdmin(intents *service.BookingIntentService, metrics interface {
	GetMetrics(context.Context) (*models.ManagementBookingMetrics, error)
}, executor *service.ReliableBookingExecutor, scope bookingapi.Scope) {
	h.bookingIntents = intents
	h.bookingMetrics = metrics
	h.reliableBooking = executor
	h.bookingScope = scope
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /version", h.getVersion)
	mux.HandleFunc("PUT /internal/discovery/v1/instances/{instance_id}", h.registerServiceInstance)
	mux.HandleFunc("POST /internal/discovery/v1/instances/{instance_id}/heartbeat", h.heartbeatServiceInstance)
	mux.HandleFunc("POST /internal/discovery/v1/instances/{instance_id}/drain", h.drainServiceInstance)
	mux.HandleFunc("DELETE /internal/discovery/v1/instances/{instance_id}", h.deregisterServiceInstance)
	mux.HandleFunc("GET /internal/discovery/v1/resolve", h.resolveServiceInstances)
	mux.HandleFunc("GET /internal/discovery/v1/catalog", h.getServiceCatalog)
	mux.HandleFunc("GET /internal/discovery/v1/prometheus-targets", h.getPrometheusTargets)

	// Games
	mux.HandleFunc("POST /api/v1/games", h.createGame)
	mux.HandleFunc("GET /api/v1/games", h.listGames)
	mux.HandleFunc("GET /api/v1/games/{id}", h.getGame)
	mux.HandleFunc("GET /api/v1/games/{id}/access", h.checkGameAccess)
	mux.HandleFunc("PATCH /api/v1/games/{id}/message-id", h.updateMessageID)
	mux.HandleFunc("PATCH /api/v1/games/{id}/courts", h.updateCourts)
	mux.HandleFunc("POST /api/v1/games/{id}/publish", h.publishGame)
	mux.HandleFunc("GET /api/v1/games/{id}/active-court-bookings", h.listActiveCourtBookings)
	mux.HandleFunc("POST /api/v1/games/{id}/book-courts", h.bookCourts)
	mux.HandleFunc("GET /internal/booking/metrics", h.getBookingMetrics)
	mux.HandleFunc("GET /internal/booking/operations/{id}", h.getBookingOperation)
	mux.HandleFunc("POST /internal/booking/operations/{id}/reconcile", h.reconcileBookingOperation)
	mux.HandleFunc("POST /internal/booking/operations/{id}/resolution", h.resolveBookingOperation)
	// Participations
	mux.HandleFunc("POST /api/v1/games/{id}/join", h.joinGame)
	mux.HandleFunc("POST /api/v1/games/{id}/skip", h.skipGame)
	mux.HandleFunc("POST /api/v1/games/{id}/guests", h.addGuest)
	mux.HandleFunc("DELETE /api/v1/games/{id}/guests", h.removeGuest)
	mux.HandleFunc("GET /api/v1/games/{id}/participations", h.getParticipations)
	mux.HandleFunc("GET /api/v1/games/{id}/guests", h.getGuests)
	mux.HandleFunc("DELETE /api/v1/games/{id}/players/{playerID}", h.kickPlayer)
	mux.HandleFunc("DELETE /api/v1/games/{id}/guests/{guestID}", h.kickGuest)

	// Game results
	mux.HandleFunc("POST /api/v1/game-results", h.submitGameResult)
	mux.HandleFunc("GET /api/v1/game-results/{id}", h.getGameResult)
	mux.HandleFunc("POST /api/v1/game-results/{id}/approval-message", h.setGameResultApprovalMessage)
	mux.HandleFunc("POST /api/v1/game-results/{id}/approve", h.approveGameResult)
	mux.HandleFunc("POST /api/v1/game-results/{id}/reject", h.rejectGameResult)
	mux.HandleFunc("POST /api/v1/game-results/{id}/cancel", h.cancelGameResult)

	// Users (canonical identity; player_id survives only for game-scoped resources)
	mux.HandleFunc("POST /api/v1/identities/resolve", h.resolveIdentity)
	mux.HandleFunc("GET /api/v1/users", h.listUsers)
	mux.HandleFunc("GET /api/v1/users/{userID}", h.getUser)
	mux.HandleFunc("PATCH /api/v1/users/{userID}/server-owner", h.setUserServerOwner)
	mux.HandleFunc("PATCH /api/v1/users/{userID}/dm-language", h.setUserDMLanguage)
	mux.HandleFunc("PATCH /api/v1/users/{userID}/results-opt-out", h.setUserResultsOptOut)
	mux.HandleFunc("GET /api/v1/users/{userID}/next-game", h.getNextGame)
	mux.HandleFunc("GET /api/v1/users/{userID}/games", h.listPlayerGames)
	mux.HandleFunc("GET /api/v1/users/{userID}/recent-completed-games", h.getRecentCompletedGames)
	mux.HandleFunc("GET /api/v1/users/{userID}/groups-with-results", h.getPlayerGroupsWithResults)
	mux.HandleFunc("GET /api/v1/users/{userID}/admin-groups", h.listAdminGroups)

	// Groups
	mux.HandleFunc("PUT /api/v1/groups/{chatID}", h.upsertGroup)
	mux.HandleFunc("PATCH /api/v1/groups/{chatID}/language", h.setGroupLanguage)
	mux.HandleFunc("PATCH /api/v1/groups/{chatID}/timezone", h.setGroupTimezone)
	mux.HandleFunc("PATCH /api/v1/groups/{chatID}/changelog", h.setGroupChangelog)
	mux.HandleFunc("PATCH /api/v1/groups/{chatID}/leaderboard-notifications", h.setGroupLeaderboardNotifications)
	mux.HandleFunc("PATCH /api/v1/groups/{chatID}/auto-booking-allowed", h.setGroupAutoBookingAllowed)
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
	mux.HandleFunc("GET /api/v1/venues/{id}/booking-readiness", h.bookingReadiness)
	mux.HandleFunc("POST /api/v1/venues/{id}/credentials", h.addCredential)
	mux.HandleFunc("GET /api/v1/venues/{id}/credentials", h.listCredentials)
	mux.HandleFunc("DELETE /api/v1/venues/{id}/credentials/{cid}", h.removeCredential)
	mux.HandleFunc("GET /api/v1/venues/{id}/credentials/priorities", h.listCredentialPriorities)

	// Leaderboard
	mux.HandleFunc("GET /api/v1/groups/{chatID}/leaderboard", h.getGroupLeaderboard)

	// Feature flags (server-owner controls)
	mux.HandleFunc("GET /api/v1/feature-flags", h.listFeatureFlags)
	mux.HandleFunc("PATCH /api/v1/feature-flags/{key}", h.setFeatureFlag)

	// Audit
	mux.HandleFunc("GET /api/v1/audit", h.listAuditEvents)
}

// NewServer builds an http.Server with the handler's routes registered.
// secret is the shared bearer token; all routes except /health and /version require it.
func NewServer(addr string, h *Handler, secret, monitoringToken string) *http.Server {
	return NewObservedServer(addr, h, secret, monitoringToken, nil)
}

func NewObservedServer(addr string, h *Handler, secret, monitoringToken string, metrics *observability.HTTPMetrics) *http.Server {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	var routes http.Handler = requireManagementBearer(secret, monitoringToken, mux)
	if metrics != nil {
		routes = metrics.WrapMuxHandler(mux, routes)
	}
	return &http.Server{
		Addr:         addr,
		Handler:      limitRequestBody(routes),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

// limitRequestBody caps every request body at maxRequestBodyBytes, returning
// an error from the body reader (surfaced as 400 by the JSON decoders) once
// the limit is exceeded, instead of buffering an unbounded payload.
func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// requireBearer is middleware that validates the Authorization: Bearer <secret> header.
// The /health and /version endpoints are exempt so container health checks and version
// probes work without credentials.
// Comparison is constant-time to prevent timing-based secret oracle attacks.
// requireBearer remains the single-token wrapper used by focused route tests.
func requireBearer(secret string, next http.Handler) http.Handler {
	return requireManagementBearer(secret, secret, next)
}

func requireManagementBearer(secret, monitoringToken string, next http.Handler) http.Handler {
	expected := []byte("Bearer " + secret)
	monitoringExpected := []byte("Bearer " + monitoringToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/version" {
			next.ServeHTTP(w, r)
			return
		}
		got := []byte(r.Header.Get("Authorization"))
		if r.URL.Path == "/internal/discovery/v1/prometheus-targets" {
			if subtle.ConstantTimeCompare(got, monitoringExpected) != 1 {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		} else if subtle.ConstantTimeCompare(got, expected) != 1 {
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
	return RunWithReady(ctx, srv, logger, nil)
}

// RunWithReady invokes listening after the TCP listener is successfully bound,
// before serving requests. It lets local readiness distinguish a constructed
// server from a process that can actually accept business traffic.
func RunWithReady(ctx context.Context, srv *http.Server, logger *slog.Logger, listening func()) error {
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	logger.Info("HTTP server listening", "addr", listener.Addr().String())
	if listening != nil {
		listening()
	}
	errCh := make(chan error, 1)
	go func() {
		if serveErr := srv.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- serveErr
		}
	}()

	select {
	case serveErr := <-errCh:
		return serveErr
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		logger.Info("HTTP server shutting down")
		return srv.Shutdown(shutCtx)
	}
}

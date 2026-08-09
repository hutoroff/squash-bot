package webserver

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
)

// Handler serves the web service HTTP API and the embedded React frontend.
type Handler struct {
	staticFS fs.FS
	logger   *slog.Logger
	version  string
	auth     *AuthHandler
	games    *GamesHandler
	audit    *AuditHandler
	groups   *GroupsHandler
	venues   *VenuesHandler
	prefs    *PrefsHandler
	users    *UsersHandler
}

// NewHandler creates a Handler that serves static files from fsys.
func NewHandler(fsys fs.FS, version string, logger *slog.Logger, auth *AuthHandler, games *GamesHandler, audit *AuditHandler, groups *GroupsHandler, venues *VenuesHandler, prefs *PrefsHandler, users *UsersHandler) *Handler {
	return &Handler{
		staticFS: fsys,
		logger:   logger,
		version:  version,
		auth:     auth,
		games:    games,
		audit:    audit,
		groups:   groups,
		venues:   venues,
		prefs:    prefs,
		users:    users,
	}
}

// RegisterRoutes wires all routes onto mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /version", h.getVersion)
	mux.HandleFunc("GET /api/config", h.handleConfig)
	mux.HandleFunc("GET /api/auth/callback", h.auth.handleCallback)
	mux.HandleFunc("GET /api/auth/me", h.auth.handleMe)
	mux.HandleFunc("POST /api/auth/logout", h.auth.handleLogout)
	mux.HandleFunc("GET /api/games", h.games.handleListGames)
	mux.HandleFunc("GET /api/games/{id}/participants", h.games.handleGetParticipants)
	mux.HandleFunc("POST /api/games/{id}/join", h.games.handleJoinGame)
	mux.HandleFunc("POST /api/games/{id}/skip", h.games.handleSkipGame)
	mux.HandleFunc("POST /api/games/{id}/guest", h.games.handleAddGuest)
	mux.HandleFunc("DELETE /api/games/{id}/guest", h.games.handleRemoveGuest)
	mux.HandleFunc("GET /api/audit", h.audit.handleListAuditEvents)
	mux.HandleFunc("GET /api/groups", h.groups.handleListGroups)
	mux.HandleFunc("GET /api/groups/{chatID}", h.groups.handleGetGroup)
	mux.HandleFunc("PATCH /api/groups/{chatID}/language", h.groups.patchGroupSetting("language"))
	mux.HandleFunc("PATCH /api/groups/{chatID}/timezone", h.groups.patchGroupSetting("timezone"))
	mux.HandleFunc("PATCH /api/groups/{chatID}/changelog", h.groups.patchGroupSetting("changelog"))
	mux.HandleFunc("PATCH /api/groups/{chatID}/leaderboard-notifications", h.groups.patchGroupSetting("leaderboard-notifications"))
	mux.HandleFunc("PATCH /api/groups/{chatID}/auto-booking-allowed", h.groups.handleSetGroupAutoBookingAllowed)

	mux.HandleFunc("GET /api/groups/{chatID}/venues", h.venues.handleListVenues)
	mux.HandleFunc("POST /api/groups/{chatID}/venues", h.venues.handleCreateVenue)
	mux.HandleFunc("GET /api/groups/{chatID}/venues/{venueID}", h.venues.handleGetVenue)
	mux.HandleFunc("PATCH /api/groups/{chatID}/venues/{venueID}", h.venues.handleUpdateVenue)
	mux.HandleFunc("DELETE /api/groups/{chatID}/venues/{venueID}", h.venues.handleDeleteVenue)
	mux.HandleFunc("GET /api/groups/{chatID}/venues/{venueID}/booking-readiness", h.venues.handleBookingReadiness)
	mux.HandleFunc("GET /api/groups/{chatID}/venues/{venueID}/credentials", h.venues.handleListCredentials)
	mux.HandleFunc("POST /api/groups/{chatID}/venues/{venueID}/credentials", h.venues.handleAddCredential)
	mux.HandleFunc("DELETE /api/groups/{chatID}/venues/{venueID}/credentials/{cid}", h.venues.handleDeleteCredential)
	mux.HandleFunc("GET /api/groups/{chatID}/venues/{venueID}/credentials/priorities", h.venues.handleCredentialPriorities)

	mux.HandleFunc("GET /api/me/preferences", h.prefs.handleGetPreferences)
	mux.HandleFunc("PATCH /api/me/dm-language", h.prefs.patchPreference("dm-language"))
	mux.HandleFunc("PATCH /api/me/results-opt-out", h.prefs.patchPreference("results-opt-out"))

	mux.HandleFunc("GET /api/users", h.users.handleListUsers)
	mux.HandleFunc("PATCH /api/users/{userID}/server-owner", h.users.handleSetServerOwner)

	mux.Handle("/", spaFileServer(h.staticFS))
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok")) //nolint:errcheck
}

func (h *Handler) getVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"version": h.version}) //nolint:errcheck
}

func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"bot_name": h.auth.botName}) //nolint:errcheck
}

// spaFileServer wraps http.FileServerFS to serve index.html for unknown paths,
// enabling client-side routing in the React app.
func spaFileServer(fsys fs.FS) http.Handler {
	fileServer := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(fsys, path); errors.Is(err, fs.ErrNotExist) {
			// Paths under assets/ are Vite-compiled outputs; return 404 if missing
			// so the browser sees a real error and caches are not poisoned with HTML.
			// All other missing paths (including dotted routes like /games/2026.04.02
			// or /users/alice@example.com) fall back to index.html for SPA routing.
			if strings.HasPrefix(path, "assets/") {
				http.NotFound(w, r)
				return
			}
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

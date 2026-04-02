package webserver

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
)

// Handler serves the squash-web HTTP API and the embedded React frontend.
type Handler struct {
	staticFS fs.FS
	logger   *slog.Logger
	version  string
	auth     *AuthHandler
}

// NewHandler creates a Handler that serves static files from fsys.
func NewHandler(fsys fs.FS, version string, logger *slog.Logger, auth *AuthHandler) *Handler {
	return &Handler{
		staticFS: fsys,
		logger:   logger,
		version:  version,
		auth:     auth,
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

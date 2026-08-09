package webserver

import (
	"fmt"
	"io"
	"net/http"
)

// PrefsHandler proxies the caller's personal preferences to the management
// service. The Telegram ID always comes from the JWT session, never the URL.
type PrefsHandler struct {
	mgmtClient
}

// NewPrefsHandler creates a PrefsHandler.
func NewPrefsHandler(auth *AuthHandler, mgmtURL, mgmtSecret string) *PrefsHandler {
	return &PrefsHandler{newMgmtClient(auth, mgmtURL, mgmtSecret)}
}

// handleGetPreferences handles GET /api/me/preferences.
func (p *PrefsHandler) handleGetPreferences(w http.ResponseWriter, r *http.Request) {
	claims, ok := p.claims(w, r)
	if !ok {
		return
	}
	p.proxy(w, r, http.MethodGet, fmt.Sprintf("/api/v1/users/%d/preferences", claims.TelegramID), nil)
}

// patchPreference returns a handler for PATCH /api/me/<suffix>, forwarding the
// client body unchanged to the caller's own preferences endpoint.
func (p *PrefsHandler) patchPreference(suffix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := p.claims(w, r)
		if !ok {
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		p.proxy(w, r, http.MethodPatch, fmt.Sprintf("/api/v1/users/%d/%s", claims.TelegramID, suffix), body)
	}
}

package webserver

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// GroupsHandler proxies GET /api/groups to the management service.
// Access is restricted to server owners (SERVICE_ADMIN_IDS).
type GroupsHandler struct {
	auth       *AuthHandler
	mgmtURL    string
	mgmtSecret string
	httpClient *http.Client
}

// NewGroupsHandler creates a GroupsHandler.
func NewGroupsHandler(auth *AuthHandler, mgmtURL, mgmtSecret string) *GroupsHandler {
	return &GroupsHandler{
		auth:       auth,
		mgmtURL:    mgmtURL,
		mgmtSecret: mgmtSecret,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// handleListGroups handles GET /api/groups.
// Requires an authenticated session belonging to a server owner.
func (g *GroupsHandler) handleListGroups(w http.ResponseWriter, r *http.Request) {
	claims, err := g.auth.claimsFromRequest(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`)) //nolint:errcheck
		return
	}

	if !g.auth.serverOwnerIDs[claims.TelegramID] {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"forbidden"}`)) //nolint:errcheck
		return
	}

	upstream := fmt.Sprintf("%s/api/v1/groups", g.mgmtURL)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`)) //nolint:errcheck
		return
	}
	req.Header.Set("Authorization", "Bearer "+g.mgmtSecret)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"upstream unavailable"}`)) //nolint:errcheck
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}

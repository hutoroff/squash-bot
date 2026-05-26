package webserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// handleSetGroupAutoBookingAllowed handles PATCH /api/groups/{chatID}/auto-booking-allowed.
// Requires an authenticated session belonging to a server owner.
func (g *GroupsHandler) handleSetGroupAutoBookingAllowed(w http.ResponseWriter, r *http.Request) {
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

	var reqBody struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid request body"}`)) //nolint:errcheck
		return
	}

	displayName := strings.TrimSpace(claims.FirstName + " " + claims.LastName)
	upstreamBody, _ := json.Marshal(map[string]any{
		"enabled":           reqBody.Enabled,
		"actor_telegram_id": claims.TelegramID,
		"actor_display":     displayName,
	})

	chatID := r.PathValue("chatID")
	upstream := fmt.Sprintf("%s/api/v1/groups/%s/auto-booking-allowed", g.mgmtURL, chatID)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPatch, upstream, bytes.NewReader(upstreamBody))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`)) //nolint:errcheck
		return
	}
	req.Header.Set("Authorization", "Bearer "+g.mgmtSecret)
	req.Header.Set("Content-Type", "application/json")

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

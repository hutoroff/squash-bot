package webserver

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// UsersHandler proxies the server-owner-only user management endpoints.
// Authority is enforced by the management service against the DB — this
// handler never grants access itself, it only forwards the caller's identity.
type UsersHandler struct {
	mgmtClient
}

// NewUsersHandler creates a UsersHandler.
func NewUsersHandler(auth *AuthHandler, mgmtURL, mgmtSecret string) *UsersHandler {
	return &UsersHandler{newMgmtClient(auth, mgmtURL, mgmtSecret)}
}

// handleListUsers handles GET /api/users.
// Owner-only: the caller's UserID is injected into X-Caller-User-Id so the
// management service can enforce the check against the DB.
func (u *UsersHandler) handleListUsers(w http.ResponseWriter, r *http.Request) {
	claims, ok := u.claims(w, r)
	if !ok {
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.mgmtURL+"/api/v1/users", nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}
	req.Header.Set("Authorization", "Bearer "+u.mgmtSecret)
	req.Header.Set("X-Caller-User-Id", strconv.FormatInt(claims.UserID, 10))

	resp, err := u.httpClient.Do(req)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "upstream unavailable")
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}

// handleSetServerOwner handles PATCH /api/users/{userID}/server-owner.
// The actor is always forced from the JWT via decodeWithActor, never trusted
// from the client body — management re-verifies the actor is a server owner
// and 409s when asked to revoke the last remaining owner.
func (u *UsersHandler) handleSetServerOwner(w http.ResponseWriter, r *http.Request) {
	claims, ok := u.claims(w, r)
	if !ok {
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	body, ok := decodeWithActor(w, r, claims, nil)
	if !ok {
		return
	}
	u.proxy(w, r, http.MethodPatch, fmt.Sprintf("/api/v1/users/%d/server-owner", userID), body)
}

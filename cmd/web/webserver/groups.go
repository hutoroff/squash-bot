package webserver

import (
	"fmt"
	"net/http"
	"strconv"
)

// GroupsHandler proxies the group settings endpoints to the management service.
// Every group-scoped route is gated by authorizeGroup: server owners may manage
// any group, other users only the groups they administer in Telegram.
type GroupsHandler struct {
	mgmtClient
}

// NewGroupsHandler creates a GroupsHandler.
func NewGroupsHandler(auth *AuthHandler, mgmtURL, mgmtSecret string) *GroupsHandler {
	return &GroupsHandler{newMgmtClient(auth, mgmtURL, mgmtSecret)}
}

// handleListGroups handles GET /api/groups.
// Returns the groups the caller may manage — all of them for a server owner,
// the ones they administer otherwise, and [] for a plain user.
func (g *GroupsHandler) handleListGroups(w http.ResponseWriter, r *http.Request) {
	claims, ok := g.claims(w, r)
	if !ok {
		return
	}
	g.proxy(w, r, http.MethodGet, fmt.Sprintf("/api/v1/users/%d/admin-groups", claims.UserID), nil)
}

// handleGetGroup handles GET /api/groups/{chatID}.
func (g *GroupsHandler) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	_, chatID, ok := g.authorizeGroup(w, r)
	if !ok {
		return
	}
	g.proxy(w, r, http.MethodGet, fmt.Sprintf("/api/v1/groups/%d", chatID), nil)
}

// patchGroupSetting returns a handler for PATCH /api/groups/{chatID}/<suffix>.
// The client body is forwarded with the actor fields overwritten from the JWT.
func (g *GroupsHandler) patchGroupSetting(suffix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, chatID, ok := g.authorizeGroup(w, r)
		if !ok {
			return
		}
		body, ok := decodeWithActor(w, r, claims, nil)
		if !ok {
			return
		}
		g.proxy(w, r, http.MethodPatch, fmt.Sprintf("/api/v1/groups/%d/%s", chatID, suffix), body)
	}
}

// handleSetGroupAutoBookingAllowed handles PATCH /api/groups/{chatID}/auto-booking-allowed.
// Server-owner only; enforced entirely by the management service against the DB
// (no local pre-check — a stale session must never be able to grant itself owner
// authority), which returns 403 for a non-owner actor.
func (g *GroupsHandler) handleSetGroupAutoBookingAllowed(w http.ResponseWriter, r *http.Request) {
	claims, ok := g.claims(w, r)
	if !ok {
		return
	}
	chatID, err := strconv.ParseInt(r.PathValue("chatID"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid chat id")
		return
	}
	body, ok := decodeWithActor(w, r, claims, nil)
	if !ok {
		return
	}
	g.proxy(w, r, http.MethodPatch, fmt.Sprintf("/api/v1/groups/%d/auto-booking-allowed", chatID), body)
}

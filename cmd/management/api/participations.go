package api

import (
	"context"
	"net/http"
)

// participationRequest is the request body used for user-bearing actions
// (join, skip, add guest, remove guest). Profile fields are gone — resolve
// owns the profile now; management looks up the display name itself for audit.
type participationRequest struct {
	UserID int64 `json:"user_id"`
	// GroupID (chat_id) associates the event with a group in the audit log.
	// Optional: if omitted (0), the audit event has no group association.
	GroupID int64 `json:"group_id"`
}

// actorDisplayForUser looks up a user's display name for audit logging.
// Best-effort: returns "" on lookup failure so audit recording (itself
// best-effort) never blocks the underlying action.
func (h *Handler) actorDisplayForUser(ctx context.Context, userID int64) string {
	user, err := h.userRepo.GetByID(ctx, userID)
	if err != nil {
		return ""
	}
	return user.DisplayName
}

// joinGame handles POST /api/v1/games/{id}/join
func (h *Handler) joinGame(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	var req participationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID == 0 {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	participations, err := h.partService.Join(r.Context(), id, req.UserID)
	if err != nil {
		h.logger.Error("joinGame", "err", err, "game_id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.auditSvc.RecordPlayerJoined(r.Context(), id, req.GroupID, req.UserID, h.actorDisplayForUser(r.Context(), req.UserID))
	writeJSON(w, http.StatusOK, participations)
}

// skipGame handles POST /api/v1/games/{id}/skip
func (h *Handler) skipGame(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	var req participationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID == 0 {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	participations, skipped, err := h.partService.Skip(r.Context(), id, req.UserID)
	if err != nil {
		h.logger.Error("skipGame", "err", err, "game_id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if skipped {
		h.auditSvc.RecordPlayerSkipped(r.Context(), id, req.GroupID, req.UserID, h.actorDisplayForUser(r.Context(), req.UserID))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"skipped":        skipped,
		"participations": participations,
	})
}

// addGuest handles POST /api/v1/games/{id}/guests
func (h *Handler) addGuest(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	var req participationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID == 0 {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	added, participations, guests, err := h.partService.AddGuest(r.Context(), id, req.UserID)
	if err != nil {
		h.logger.Error("addGuest", "err", err, "game_id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if added {
		h.auditSvc.RecordGuestAdded(r.Context(), id, req.GroupID, req.UserID, h.actorDisplayForUser(r.Context(), req.UserID))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"added":          added,
		"participations": participations,
		"guests":         guests,
	})
}

// removeGuest handles DELETE /api/v1/games/{id}/guests
// Body: {"user_id": 123}
func (h *Handler) removeGuest(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	var req participationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID == 0 {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	removed, participations, guests, err := h.partService.RemoveGuest(r.Context(), id, req.UserID)
	if err != nil {
		h.logger.Error("removeGuest", "err", err, "game_id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if removed {
		h.auditSvc.RecordGuestRemoved(r.Context(), id, req.GroupID, req.UserID, h.actorDisplayForUser(r.Context(), req.UserID))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"removed":        removed,
		"participations": participations,
		"guests":         guests,
	})
}

// getParticipations handles GET /api/v1/games/{id}/participations
func (h *Handler) getParticipations(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	participations, err := h.partService.GetParticipations(r.Context(), id)
	if err != nil {
		h.logger.Error("getParticipations", "err", err, "game_id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, participations)
}

// getGuests handles GET /api/v1/games/{id}/guests
func (h *Handler) getGuests(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	guests, err := h.partService.GetGuests(r.Context(), id)
	if err != nil {
		h.logger.Error("getGuests", "err", err, "game_id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, guests)
}

// kickPlayer handles DELETE /api/v1/games/{id}/players/{playerID}
// Optional query params: group_id, actor_user_id, actor_display (for audit).
func (h *Handler) kickPlayer(w http.ResponseWriter, r *http.Request) {
	gameID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	playerID, err := parseID(r.PathValue("playerID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid player_id")
		return
	}
	q := r.URL.Query()
	groupID, _ := parseID(q.Get("group_id"))
	actorUserID, _ := parseID(q.Get("actor_user_id"))
	actorDisp := q.Get("actor_display")

	participations, guests, removed, err := h.partService.KickPlayer(r.Context(), gameID, playerID)
	if err != nil {
		h.logger.Error("kickPlayer", "err", err, "game_id", gameID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if removed && actorUserID != 0 {
		h.auditSvc.RecordPlayerKicked(r.Context(), gameID, groupID, actorUserID, playerID, actorDisp)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"removed":        removed,
		"participations": participations,
		"guests":         guests,
	})
}

// kickGuest handles DELETE /api/v1/games/{id}/guests/{guestID}
// Optional query params: group_id, actor_user_id, actor_display (for audit).
func (h *Handler) kickGuest(w http.ResponseWriter, r *http.Request) {
	gameID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	guestID, err := parseID(r.PathValue("guestID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid guest_id")
		return
	}
	q := r.URL.Query()
	groupID, _ := parseID(q.Get("group_id"))
	actorUserID, _ := parseID(q.Get("actor_user_id"))
	actorDisp := q.Get("actor_display")

	participations, guests, removed, err := h.partService.KickGuestByID(r.Context(), gameID, guestID)
	if err != nil {
		h.logger.Error("kickGuest", "err", err, "game_id", gameID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if removed && actorUserID != 0 {
		h.auditSvc.RecordGuestKicked(r.Context(), gameID, groupID, actorUserID, guestID, actorDisp)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"removed":        removed,
		"participations": participations,
		"guests":         guests,
	})
}

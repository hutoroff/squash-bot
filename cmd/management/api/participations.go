package api

import (
	"net/http"
	"strconv"
)

// playerRequest is the request body used for player-bearing actions (join, skip, add guest).
type playerRequest struct {
	TelegramID int64  `json:"telegram_id"`
	Username   string `json:"username"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	// GroupID (chat_id) associates the event with a group in the audit log.
	// Optional: if omitted (0), the audit event has no group association.
	GroupID int64 `json:"group_id"`
}

// actorDisplay formats a display name for audit from request fields.
func actorDisplay(username, firstName, lastName string) string {
	if username != "" {
		return "@" + username
	}
	name := firstName
	if lastName != "" {
		if name != "" {
			name += " "
		}
		name += lastName
	}
	return name
}

// joinGame handles POST /api/v1/games/{id}/join
func (h *Handler) joinGame(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	var req playerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	participations, err := h.partService.Join(r.Context(), id, req.TelegramID, req.Username, req.FirstName, req.LastName)
	if err != nil {
		h.logger.Error("joinGame", "err", err, "game_id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.auditSvc.RecordPlayerJoined(r.Context(), id, req.GroupID, req.TelegramID, actorDisplay(req.Username, req.FirstName, req.LastName))
	writeJSON(w, http.StatusOK, participations)
}

// skipGame handles POST /api/v1/games/{id}/skip
func (h *Handler) skipGame(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	var req playerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	participations, skipped, err := h.partService.Skip(r.Context(), id, req.TelegramID, req.Username, req.FirstName, req.LastName)
	if err != nil {
		h.logger.Error("skipGame", "err", err, "game_id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if skipped {
		h.auditSvc.RecordPlayerSkipped(r.Context(), id, req.GroupID, req.TelegramID, actorDisplay(req.Username, req.FirstName, req.LastName))
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
	var req playerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	added, participations, guests, err := h.partService.AddGuest(r.Context(), id, req.TelegramID, req.Username, req.FirstName, req.LastName)
	if err != nil {
		h.logger.Error("addGuest", "err", err, "game_id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if added {
		h.auditSvc.RecordGuestAdded(r.Context(), id, req.GroupID, req.TelegramID, actorDisplay(req.Username, req.FirstName, req.LastName))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"added":          added,
		"participations": participations,
		"guests":         guests,
	})
}

// removeGuest handles DELETE /api/v1/games/{id}/guests
// Body: {"telegram_id": 123}
func (h *Handler) removeGuest(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	var req struct {
		TelegramID int64  `json:"telegram_id"`
		GroupID    int64  `json:"group_id"`
		Username   string `json:"username"`
		FirstName  string `json:"first_name"`
		LastName   string `json:"last_name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	removed, participations, guests, err := h.partService.RemoveGuest(r.Context(), id, req.TelegramID)
	if err != nil {
		h.logger.Error("removeGuest", "err", err, "game_id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if removed {
		h.auditSvc.RecordGuestRemoved(r.Context(), id, req.GroupID, req.TelegramID, actorDisplay(req.Username, req.FirstName, req.LastName))
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

// kickPlayer handles DELETE /api/v1/games/{id}/players/{telegramID}
// Optional query params: group_id, actor_tg_id, actor_display (for audit).
func (h *Handler) kickPlayer(w http.ResponseWriter, r *http.Request) {
	gameID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	telegramID, err := parseID(r.PathValue("telegramID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid telegram_id")
		return
	}
	q := r.URL.Query()
	groupID, _ := strconv.ParseInt(q.Get("group_id"), 10, 64)
	actorTgID, _ := strconv.ParseInt(q.Get("actor_tg_id"), 10, 64)
	actorDisp := q.Get("actor_display")

	participations, guests, removed, err := h.partService.KickPlayer(r.Context(), gameID, telegramID)
	if err != nil {
		h.logger.Error("kickPlayer", "err", err, "game_id", gameID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if removed && actorTgID != 0 {
		h.auditSvc.RecordPlayerKicked(r.Context(), gameID, groupID, actorTgID, telegramID, actorDisp)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"removed":        removed,
		"participations": participations,
		"guests":         guests,
	})
}

// kickGuest handles DELETE /api/v1/games/{id}/guests/{guestID}
// Optional query params: group_id, actor_tg_id, actor_display (for audit).
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
	groupID, _ := strconv.ParseInt(q.Get("group_id"), 10, 64)
	actorTgID, _ := strconv.ParseInt(q.Get("actor_tg_id"), 10, 64)
	actorDisp := q.Get("actor_display")

	participations, guests, removed, err := h.partService.KickGuestByID(r.Context(), gameID, guestID)
	if err != nil {
		h.logger.Error("kickGuest", "err", err, "game_id", gameID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if removed && actorTgID != 0 {
		h.auditSvc.RecordGuestKicked(r.Context(), gameID, groupID, actorTgID, guestID, actorDisp)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"removed":        removed,
		"participations": participations,
		"guests":         guests,
	})
}

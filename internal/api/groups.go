package api

import (
	"net/http"

	"github.com/vkhutorov/squash_bot/internal/models"
)

// upsertGroup handles PUT /api/v1/groups/{chatID}
func (h *Handler) upsertGroup(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	var req struct {
		Title      string `json:"title"`
		BotIsAdmin bool   `json:"bot_is_admin"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.groupRepo.Upsert(r.Context(), chatID, req.Title, req.BotIsAdmin); err != nil {
		h.logger.Error("upsertGroup", "err", err, "chat_id", chatID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// removeGroup handles DELETE /api/v1/groups/{chatID}
func (h *Handler) removeGroup(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	if err := h.groupRepo.Remove(r.Context(), chatID); err != nil {
		h.logger.Error("removeGroup", "err", err, "chat_id", chatID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listGroups handles GET /api/v1/groups
func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.groupRepo.GetAll(r.Context())
	if err != nil {
		h.logger.Error("listGroups", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if groups == nil {
		groups = []models.Group{}
	}
	writeJSON(w, http.StatusOK, groups)
}

// getGroup handles GET /api/v1/groups/{chatID}
// Returns 200 if found, 404 if not.
func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	exists, err := h.groupRepo.Exists(r.Context(), chatID)
	if err != nil {
		h.logger.Error("getGroup", "err", err, "chat_id", chatID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"exists": true})
}

package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

// upsertGroup handles PUT /api/v1/groups/{chatID}
func (h *Handler) upsertGroup(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	var req struct {
		Title        string `json:"title"`
		BotIsAdmin   bool   `json:"bot_is_admin"`
		IsNewJoin    bool   `json:"is_new_join"`
		ActorUserID  int64  `json:"actor_user_id"`
		ActorDisplay string `json:"actor_display"`
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
	if req.IsNewJoin && req.ActorUserID != 0 {
		h.auditSvc.RecordBotAddedToGroup(r.Context(), chatID, req.Title, req.ActorUserID, req.ActorDisplay)
	}
	w.WriteHeader(http.StatusNoContent)
}

// setGroupLanguage handles PATCH /api/v1/groups/{chatID}/language
func (h *Handler) setGroupLanguage(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	var req struct {
		Language     string `json:"language"`
		ActorUserID  int64  `json:"actor_user_id"`
		ActorDisplay string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch req.Language {
	case "en", "de", "ru":
		// valid
	default:
		writeError(w, http.StatusBadRequest, "unsupported language; use en, de, or ru")
		return
	}
	var oldLang string
	if req.ActorUserID != 0 {
		if g, err := h.groupRepo.GetByID(r.Context(), chatID); err == nil {
			oldLang = g.Language
		}
	}
	if err := h.groupRepo.SetLanguage(r.Context(), chatID, req.Language); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
		} else {
			h.logger.Error("setGroupLanguage", "err", err, "chat_id", chatID)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if req.ActorUserID != 0 {
		h.auditSvc.RecordGroupSettings(r.Context(), chatID, req.ActorUserID, req.ActorDisplay, "language", oldLang, req.Language)
	}
	w.WriteHeader(http.StatusNoContent)
}

// setGroupTimezone handles PATCH /api/v1/groups/{chatID}/timezone
func (h *Handler) setGroupTimezone(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	var req struct {
		Timezone     string `json:"timezone"`
		ActorUserID  int64  `json:"actor_user_id"`
		ActorDisplay string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		writeError(w, http.StatusBadRequest, "invalid IANA timezone")
		return
	}
	var oldTZ string
	if req.ActorUserID != 0 {
		if g, err := h.groupRepo.GetByID(r.Context(), chatID); err == nil {
			oldTZ = g.Timezone
		}
	}
	if err := h.groupRepo.SetTimezone(r.Context(), chatID, req.Timezone); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
		} else {
			h.logger.Error("setGroupTimezone", "err", err, "chat_id", chatID)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if req.ActorUserID != 0 {
		h.auditSvc.RecordGroupSettings(r.Context(), chatID, req.ActorUserID, req.ActorDisplay, "timezone", oldTZ, req.Timezone)
	}
	w.WriteHeader(http.StatusNoContent)
}

// setGroupChangelog handles PATCH /api/v1/groups/{chatID}/changelog
func (h *Handler) setGroupChangelog(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	var req struct {
		ChangelogEnabled bool   `json:"changelog_enabled"`
		ActorUserID      int64  `json:"actor_user_id"`
		ActorDisplay     string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.groupRepo.SetChangelogEnabled(r.Context(), chatID, req.ChangelogEnabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
		} else {
			h.logger.Error("setGroupChangelog", "err", err, "chat_id", chatID)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if req.ActorUserID != 0 {
		h.auditSvc.RecordGroupChangelogToggled(r.Context(), chatID, req.ActorUserID, req.ActorDisplay, req.ChangelogEnabled)
	}
	w.WriteHeader(http.StatusNoContent)
}

// setGroupLeaderboardNotifications handles PATCH /api/v1/groups/{chatID}/leaderboard-notifications
func (h *Handler) setGroupLeaderboardNotifications(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	var req struct {
		Enabled      bool   `json:"leaderboard_notifications_enabled"`
		ActorUserID  int64  `json:"actor_user_id"`
		ActorDisplay string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.groupRepo.SetLeaderboardNotificationsEnabled(r.Context(), chatID, req.Enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
		} else {
			h.logger.Error("setGroupLeaderboardNotifications", "err", err, "chat_id", chatID)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if req.ActorUserID != 0 {
		h.auditSvc.RecordGroupLeaderboardNotificationsToggled(r.Context(), chatID, req.ActorUserID, req.ActorDisplay, req.Enabled)
	}
	w.WriteHeader(http.StatusNoContent)
}

// setGroupAutoBookingAllowed handles PATCH /api/v1/groups/{chatID}/auto-booking-allowed
func (h *Handler) setGroupAutoBookingAllowed(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	var req struct {
		Enabled      bool   `json:"enabled"`
		ActorUserID  int64  `json:"actor_user_id"`
		ActorDisplay string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !h.isServerOwner(r.Context(), req.ActorUserID) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	group, err := h.groupRepo.GetByID(r.Context(), chatID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
		} else {
			h.logger.Error("setGroupAutoBookingAllowed: getByID", "err", err, "chat_id", chatID)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if group.AutoBookingAllowed == req.Enabled {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	cascadedIDs, err := h.groupRepo.SetAutoBookingAllowed(r.Context(), chatID, req.Enabled)
	if err != nil {
		h.logger.Error("setGroupAutoBookingAllowed", "err", err, "chat_id", chatID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.ActorUserID != 0 {
		h.auditSvc.RecordGroupAutoBookingAllowedToggled(r.Context(), chatID, req.ActorUserID, req.ActorDisplay, req.Enabled, cascadedIDs)
	}
	w.WriteHeader(http.StatusNoContent)
}

// removeGroup handles DELETE /api/v1/groups/{chatID}
// Optional query params: actor_user_id, actor_display, group_title (for audit).
func (h *Handler) removeGroup(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	q := r.URL.Query()
	actorUserID, _ := strconv.ParseInt(q.Get("actor_user_id"), 10, 64)
	actorDisp := q.Get("actor_display")
	groupTitle := q.Get("group_title")

	if actorUserID != 0 {
		h.auditSvc.RecordBotRemovedFromGroup(r.Context(), chatID, groupTitle, actorUserID, actorDisp)
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

// listAdminGroups handles GET /api/v1/users/{userID}/admin-groups
// Returns the groups userID may administer: all groups for a server owner,
// otherwise the groups where Telegram reports them as an administrator.
func (h *Handler) listAdminGroups(w http.ResponseWriter, r *http.Request) {
	userID, err := parseID(r.PathValue("userID"))
	if err != nil || userID == 0 {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	groups, err := h.groupRepo.GetAll(r.Context())
	if err != nil {
		h.logger.Error("listAdminGroups", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	result := []models.Group{}
	if h.isServerOwner(r.Context(), userID) {
		result = append(result, groups...)
	} else if h.adminResolver != nil {
		adminGroups, err := h.adminResolver.AdminGroupsFor(r.Context(), userID)
		if err != nil {
			h.logger.Error("listAdminGroups: resolver", "err", err, "user_id", userID)
			writeError(w, http.StatusInternalServerError, "failed to resolve admin groups")
			return
		}
		allowed := make(map[int64]bool, len(adminGroups))
		for _, id := range adminGroups {
			allowed[id] = true
		}
		for _, g := range groups {
			if allowed[g.ChatID] {
				result = append(result, g)
			}
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// getGroup handles GET /api/v1/groups/{chatID}
// Returns the full group object if found, 404 if not.
func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	group, err := h.groupRepo.GetByID(r.Context(), chatID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
		} else {
			h.logger.Error("getGroup", "err", err, "chat_id", chatID)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, group)
}

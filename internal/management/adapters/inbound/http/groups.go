package http

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

func (h *Handler) upsertGroup(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	var req struct {
		Title           string `json:"title"`
		BotIsAdmin      bool   `json:"bot_is_admin"`
		IsNewJoin       bool   `json:"is_new_join"`
		ActorTelegramID int64  `json:"actor_telegram_id"`
		ActorDisplay    string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.groupSvc.Upsert(r.Context(), chatID, req.Title, req.BotIsAdmin); err != nil {
		h.logger.Error("upsertGroup", "err", err, "chat_id", chatID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.IsNewJoin && req.ActorTelegramID != 0 {
		h.auditSvc.RecordBotAddedToGroup(r.Context(), chatID, req.Title, req.ActorTelegramID, req.ActorDisplay)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) setGroupLanguage(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	var req struct {
		Language        string `json:"language"`
		ActorTelegramID int64  `json:"actor_telegram_id"`
		ActorDisplay    string `json:"actor_display"`
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
	if req.ActorTelegramID != 0 {
		if g, err := h.groupSvc.GetByID(r.Context(), chatID); err == nil {
			oldLang = g.Language
		}
	}
	if err := h.groupSvc.SetLanguage(r.Context(), chatID, req.Language); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
		} else {
			h.logger.Error("setGroupLanguage", "err", err, "chat_id", chatID)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if req.ActorTelegramID != 0 {
		h.auditSvc.RecordGroupSettings(r.Context(), chatID, req.ActorTelegramID, req.ActorDisplay, "language", oldLang, req.Language)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) setGroupTimezone(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	var req struct {
		Timezone        string `json:"timezone"`
		ActorTelegramID int64  `json:"actor_telegram_id"`
		ActorDisplay    string `json:"actor_display"`
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
	if req.ActorTelegramID != 0 {
		if g, err := h.groupSvc.GetByID(r.Context(), chatID); err == nil {
			oldTZ = g.Timezone
		}
	}
	if err := h.groupSvc.SetTimezone(r.Context(), chatID, req.Timezone); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
		} else {
			h.logger.Error("setGroupTimezone", "err", err, "chat_id", chatID)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if req.ActorTelegramID != 0 {
		h.auditSvc.RecordGroupSettings(r.Context(), chatID, req.ActorTelegramID, req.ActorDisplay, "timezone", oldTZ, req.Timezone)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) setGroupChangelog(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	var req struct {
		ChangelogEnabled bool   `json:"changelog_enabled"`
		ActorTelegramID  int64  `json:"actor_telegram_id"`
		ActorDisplay     string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.groupSvc.SetChangelogEnabled(r.Context(), chatID, req.ChangelogEnabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
		} else {
			h.logger.Error("setGroupChangelog", "err", err, "chat_id", chatID)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if req.ActorTelegramID != 0 {
		h.auditSvc.RecordGroupChangelogToggled(r.Context(), chatID, req.ActorTelegramID, req.ActorDisplay, req.ChangelogEnabled)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) removeGroup(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	q := r.URL.Query()
	actorTgID, _ := strconv.ParseInt(q.Get("actor_tg_id"), 10, 64)
	actorDisp := q.Get("actor_display")
	groupTitle := q.Get("group_title")

	if actorTgID != 0 {
		h.auditSvc.RecordBotRemovedFromGroup(r.Context(), chatID, groupTitle, actorTgID, actorDisp)
	}
	if err := h.groupSvc.Remove(r.Context(), chatID); err != nil {
		h.logger.Error("removeGroup", "err", err, "chat_id", chatID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.groupSvc.GetAll(r.Context())
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

func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	group, err := h.groupSvc.GetByID(r.Context(), chatID)
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

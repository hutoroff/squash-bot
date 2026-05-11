package http

import (
	"errors"
	"net/http"

	"github.com/hutoroff/squash-bot/internal/management/application/venue"
	"github.com/hutoroff/squash-bot/internal/models"
)

func (h *Handler) credServiceAvailable(w http.ResponseWriter) bool {
	if h.venueCredSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "credential management is disabled: CREDENTIALS_ENCRYPTION_KEY is not configured")
		return false
	}
	return true
}

func (h *Handler) addCredential(w http.ResponseWriter, r *http.Request) {
	if !h.credServiceAvailable(w) {
		return
	}
	venueID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return
	}
	var req struct {
		GroupID         int64  `json:"group_id"`
		Login           string `json:"login"`
		Password        string `json:"password"`
		Priority        int    `json:"priority"`
		MaxCourts       int    `json:"max_courts"`
		ActorTelegramID int64  `json:"actor_telegram_id"`
		ActorDisplay    string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.GroupID == 0 || req.Login == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "group_id, login, and password are required")
		return
	}
	if req.MaxCourts <= 0 {
		req.MaxCourts = 3
	}

	cred, err := h.venueCredSvc.Add(r.Context(), venueID, req.GroupID, req.Login, req.Password, req.Priority, req.MaxCourts)
	if err != nil {
		if errors.Is(err, venue.ErrDuplicateCredentialLogin) {
			writeError(w, http.StatusConflict, "a credential with this login already exists for this venue")
			return
		}
		h.logger.Error("addCredential", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if req.ActorTelegramID != 0 {
		h.auditSvc.RecordCredentialAdded(r.Context(), cred.ID, venueID, req.GroupID, req.ActorTelegramID, req.ActorDisplay, req.Login)
	}
	writeJSON(w, http.StatusCreated, cred)
}

func (h *Handler) listCredentials(w http.ResponseWriter, r *http.Request) {
	if !h.credServiceAvailable(w) {
		return
	}
	venueID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return
	}
	groupIDStr := r.URL.Query().Get("group_id")
	if groupIDStr == "" {
		writeError(w, http.StatusBadRequest, "group_id query parameter is required")
		return
	}
	groupID, err := parseID(groupIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group_id")
		return
	}

	creds, err := h.venueCredSvc.List(r.Context(), venueID, groupID)
	if err != nil {
		h.logger.Error("listCredentials", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if creds == nil {
		creds = []*models.VenueCredential{}
	}
	writeJSON(w, http.StatusOK, creds)
}

func (h *Handler) removeCredential(w http.ResponseWriter, r *http.Request) {
	if !h.credServiceAvailable(w) {
		return
	}
	venueID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return
	}
	credID, err := parseID(r.PathValue("cid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	q := r.URL.Query()
	groupIDStr := q.Get("group_id")
	if groupIDStr == "" {
		writeError(w, http.StatusBadRequest, "group_id query parameter is required")
		return
	}
	groupID, err := parseID(groupIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group_id")
		return
	}
	actorTgID, _ := parseID(q.Get("actor_tg_id"))
	actorDisplay := q.Get("actor_display")

	if err := h.venueCredSvc.Remove(r.Context(), credID, venueID, groupID); err != nil {
		if errors.Is(err, venue.ErrCredentialInUse) {
			writeError(w, http.StatusConflict, "credential has active court bookings and cannot be deleted")
			return
		}
		if errors.Is(err, venue.ErrCredentialNotFound) {
			writeError(w, http.StatusNotFound, "credential not found")
			return
		}
		h.logger.Error("removeCredential", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if actorTgID != 0 {
		h.auditSvc.RecordCredentialRemoved(r.Context(), credID, venueID, groupID, actorTgID, actorDisplay, "")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listCredentialPriorities(w http.ResponseWriter, r *http.Request) {
	if !h.credServiceAvailable(w) {
		return
	}
	venueID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return
	}
	groupIDStr := r.URL.Query().Get("group_id")
	if groupIDStr == "" {
		writeError(w, http.StatusBadRequest, "group_id query parameter is required")
		return
	}
	groupID, err := parseID(groupIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group_id")
		return
	}

	priorities, err := h.venueCredSvc.PrioritiesInUse(r.Context(), venueID, groupID)
	if err != nil {
		h.logger.Error("listCredentialPriorities", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if priorities == nil {
		priorities = []int{}
	}
	writeJSON(w, http.StatusOK, priorities)
}

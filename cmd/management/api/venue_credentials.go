package api

import (
	"errors"
	"net/http"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/internal/models"
)

// credServiceAvailable writes a 503 and returns false when the credential service
// was not initialised (CREDENTIALS_ENCRYPTION_KEY not set).
func (h *Handler) credServiceAvailable(w http.ResponseWriter) bool {
	if h.venueCredService == nil {
		writeError(w, http.StatusServiceUnavailable, "credential management is disabled: CREDENTIALS_ENCRYPTION_KEY is not configured")
		return false
	}
	return true
}

// addCredential handles POST /api/v1/venues/{id}/credentials
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
		GroupID   int64  `json:"group_id"`
		Login     string `json:"login"`
		Password  string `json:"password"`
		Priority  int    `json:"priority"`
		MaxCourts int    `json:"max_courts"`
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
		req.MaxCourts = 3 // default
	}

	cred, err := h.venueCredService.Add(r.Context(), venueID, req.GroupID, req.Login, req.Password, req.Priority, req.MaxCourts)
	if err != nil {
		if errors.Is(err, service.ErrDuplicateCredentialLogin) {
			writeError(w, http.StatusConflict, "a credential with this login already exists for this venue")
			return
		}
		h.logger.Error("addCredential", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, cred)
}

// listCredentials handles GET /api/v1/venues/{id}/credentials?group_id=X
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

	creds, err := h.venueCredService.List(r.Context(), venueID, groupID)
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

// removeCredential handles DELETE /api/v1/venues/{id}/credentials/{cid}?group_id=X
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

	if err := h.venueCredService.Remove(r.Context(), credID, venueID, groupID); err != nil {
		if errors.Is(err, service.ErrCredentialInUse) {
			writeError(w, http.StatusConflict, "credential has active court bookings and cannot be deleted")
			return
		}
		h.logger.Error("removeCredential", "err", err)
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listCredentialPriorities handles GET /api/v1/venues/{id}/credentials/priorities?group_id=X
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

	priorities, err := h.venueCredService.PrioritiesInUse(r.Context(), venueID, groupID)
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

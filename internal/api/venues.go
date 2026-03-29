package api

import (
	"net/http"

	"github.com/vkhutorov/squash_bot/internal/models"
)

// createVenue handles POST /api/v1/venues
func (h *Handler) createVenue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupID          int64  `json:"group_id"`
		Name             string `json:"name"`
		Courts           string `json:"courts"`
		TimeSlots        string `json:"time_slots"`
		Address          string `json:"address"`
		GracePeriodHours int    `json:"grace_period_hours"`
		GameDays         string `json:"game_days"`
		BookingOpensDays int    `json:"booking_opens_days"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.GroupID == 0 || req.Name == "" || req.Courts == "" {
		writeError(w, http.StatusBadRequest, "group_id, name, and courts are required")
		return
	}
	if req.GracePeriodHours == 0 {
		req.GracePeriodHours = 24
	}
	if req.BookingOpensDays == 0 {
		req.BookingOpensDays = 14
	}

	venue, err := h.venueService.CreateVenue(r.Context(),
		req.GroupID, req.Name, req.Courts, req.TimeSlots, req.Address,
		req.GracePeriodHours, req.GameDays, req.BookingOpensDays,
	)
	if err != nil {
		h.logger.Error("createVenue", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, venue)
}

// listVenues handles GET /api/v1/venues?group_id=X
func (h *Handler) listVenues(w http.ResponseWriter, r *http.Request) {
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

	venues, err := h.venueService.GetVenuesByGroup(r.Context(), groupID)
	if err != nil {
		h.logger.Error("listVenues", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if venues == nil {
		venues = []*models.Venue{}
	}
	writeJSON(w, http.StatusOK, venues)
}

// getVenue handles GET /api/v1/venues/{id}
func (h *Handler) getVenue(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return
	}
	venue, err := h.venueService.GetVenueByID(r.Context(), id)
	if err != nil {
		h.logger.Error("getVenue", "err", err, "id", id)
		writeError(w, http.StatusNotFound, "venue not found")
		return
	}
	writeJSON(w, http.StatusOK, venue)
}

// updateVenue handles PATCH /api/v1/venues/{id}
func (h *Handler) updateVenue(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return
	}
	var req struct {
		GroupID          int64  `json:"group_id"`
		Name             string `json:"name"`
		Courts           string `json:"courts"`
		TimeSlots        string `json:"time_slots"`
		Address          string `json:"address"`
		GracePeriodHours int    `json:"grace_period_hours"`
		GameDays         string `json:"game_days"`
		BookingOpensDays int    `json:"booking_opens_days"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.GroupID == 0 || req.Name == "" || req.Courts == "" {
		writeError(w, http.StatusBadRequest, "group_id, name, and courts are required")
		return
	}
	if req.GracePeriodHours == 0 {
		req.GracePeriodHours = 24
	}
	if req.BookingOpensDays == 0 {
		req.BookingOpensDays = 14
	}

	venue, err := h.venueService.UpdateVenue(r.Context(),
		id, req.GroupID, req.Name, req.Courts, req.TimeSlots, req.Address,
		req.GracePeriodHours, req.GameDays, req.BookingOpensDays,
	)
	if err != nil {
		h.logger.Error("updateVenue", "err", err, "id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, venue)
}

// deleteVenue handles DELETE /api/v1/venues/{id}?group_id=X
func (h *Handler) deleteVenue(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
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

	if err := h.venueService.DeleteVenue(r.Context(), id, groupID); err != nil {
		h.logger.Error("deleteVenue", "err", err, "id", id)
		writeError(w, http.StatusNotFound, "venue not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

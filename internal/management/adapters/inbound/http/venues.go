package http

import (
	"errors"
	"net/http"

	"github.com/hutoroff/squash-bot/internal/management/application/venue"
	"github.com/hutoroff/squash-bot/internal/models"
)

func (h *Handler) createVenue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupID               int64  `json:"group_id"`
		Name                  string `json:"name"`
		Courts                string `json:"courts"`
		TimeSlots             string `json:"time_slots"`
		Address               string `json:"address"`
		GracePeriodHours      int    `json:"grace_period_hours"`
		GameDays              string `json:"game_days"`
		BookingOpensDays      int    `json:"booking_opens_days"`
		PreferredGameTimes    string `json:"preferred_game_times"`
		AutoBookingCourts     string `json:"auto_booking_courts"`
		AutoBookingEnabled    bool   `json:"auto_booking_enabled"`
		AutoBookingGamesCount int    `json:"auto_booking_games_count"`
		ActorTelegramID       int64  `json:"actor_telegram_id"`
		ActorDisplay          string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.GroupID == 0 || req.Name == "" || req.Courts == "" {
		writeError(w, http.StatusBadRequest, "group_id, name, and courts are required")
		return
	}
	if req.GracePeriodHours < 0 {
		writeError(w, http.StatusBadRequest, "grace_period_hours must be a positive integer")
		return
	}
	if req.GracePeriodHours == 0 {
		req.GracePeriodHours = 24
	}
	if req.BookingOpensDays < 0 {
		writeError(w, http.StatusBadRequest, "booking_opens_days must be a positive integer")
		return
	}
	if req.BookingOpensDays == 0 {
		req.BookingOpensDays = 14
	}

	v, err := h.venueSvc.CreateVenue(r.Context(),
		req.GroupID, req.Name, req.Courts, req.TimeSlots, req.Address,
		req.GracePeriodHours, req.GameDays, req.BookingOpensDays, req.PreferredGameTimes, req.AutoBookingCourts, req.AutoBookingEnabled, req.AutoBookingGamesCount,
	)
	if err != nil {
		h.logger.Error("createVenue", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.ActorTelegramID != 0 {
		h.auditSvc.RecordVenueCreated(r.Context(), v.ID, req.GroupID, req.ActorTelegramID, req.ActorDisplay, v.Name)
	}
	writeJSON(w, http.StatusCreated, v)
}

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

	venues, err := h.venueSvc.GetVenuesByGroup(r.Context(), groupID)
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

func (h *Handler) getVenue(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return
	}
	v, err := h.venueSvc.GetVenueByID(r.Context(), id)
	if err != nil {
		h.logger.Error("getVenue", "err", err, "id", id)
		writeError(w, http.StatusNotFound, "venue not found")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) updateVenue(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return
	}
	var req struct {
		GroupID               int64  `json:"group_id"`
		Name                  string `json:"name"`
		Courts                string `json:"courts"`
		TimeSlots             string `json:"time_slots"`
		Address               string `json:"address"`
		GracePeriodHours      int    `json:"grace_period_hours"`
		GameDays              string `json:"game_days"`
		BookingOpensDays      int    `json:"booking_opens_days"`
		PreferredGameTimes    string `json:"preferred_game_times"`
		AutoBookingCourts     string `json:"auto_booking_courts"`
		AutoBookingEnabled    bool   `json:"auto_booking_enabled"`
		AutoBookingGamesCount int    `json:"auto_booking_games_count"`
		ActorTelegramID       int64  `json:"actor_telegram_id"`
		ActorDisplay          string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.GroupID == 0 || req.Name == "" || req.Courts == "" {
		writeError(w, http.StatusBadRequest, "group_id, name, and courts are required")
		return
	}
	if req.GracePeriodHours < 0 {
		writeError(w, http.StatusBadRequest, "grace_period_hours must be a positive integer")
		return
	}
	if req.GracePeriodHours == 0 {
		req.GracePeriodHours = 24
	}
	if req.BookingOpensDays < 0 {
		writeError(w, http.StatusBadRequest, "booking_opens_days must be a positive integer")
		return
	}
	if req.BookingOpensDays == 0 {
		req.BookingOpensDays = 14
	}

	v, err := h.venueSvc.UpdateVenue(r.Context(),
		id, req.GroupID, req.Name, req.Courts, req.TimeSlots, req.Address,
		req.GracePeriodHours, req.GameDays, req.BookingOpensDays, req.PreferredGameTimes, req.AutoBookingCourts, req.AutoBookingEnabled, req.AutoBookingGamesCount,
	)
	if err != nil {
		h.logger.Error("updateVenue", "err", err, "id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.ActorTelegramID != 0 {
		h.auditSvc.RecordVenueUpdated(r.Context(), v.ID, req.GroupID, req.ActorTelegramID, req.ActorDisplay, v.Name)
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) deleteVenue(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
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

	v, err := h.venueSvc.GetVenueByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "venue not found")
		return
	}

	if err := h.venueSvc.DeleteVenue(r.Context(), id, groupID); err != nil {
		if errors.Is(err, venue.ErrVenueHasActiveBookings) {
			writeError(w, http.StatusConflict, "venue has active court bookings and cannot be deleted")
			return
		}
		h.logger.Error("deleteVenue", "err", err, "id", id)
		writeError(w, http.StatusNotFound, "venue not found")
		return
	}
	if actorTgID != 0 {
		h.auditSvc.RecordVenueDeleted(r.Context(), id, groupID, actorTgID, actorDisplay, v.Name)
	}
	w.WriteHeader(http.StatusNoContent)
}

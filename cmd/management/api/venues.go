package api

import (
	"errors"
	"net/http"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

// createVenue handles POST /api/v1/venues
func (h *Handler) createVenue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupID                int64  `json:"group_id"`
		Name                   string `json:"name"`
		Courts                 string `json:"courts"`
		TimeSlots              string `json:"time_slots"`
		Address                string `json:"address"`
		GracePeriodHours       int    `json:"grace_period_hours"`
		GameDays               string `json:"game_days"`
		BookingOpensDays       int    `json:"booking_opens_days"`
		PreferredGameTimes     string `json:"preferred_game_times"`
		AutoBookingCourts      string `json:"auto_booking_courts"`
		AutoBookingEnabled     bool   `json:"auto_booking_enabled"`
		AutoBookingCourtsCount int    `json:"auto_booking_courts_count"`
		ActorTelegramID        int64  `json:"actor_telegram_id"`
		ActorDisplay           string `json:"actor_display"`
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

	if req.AutoBookingEnabled {
		group, err := h.groupRepo.GetByID(r.Context(), req.GroupID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "group not found")
			} else {
				h.logger.Error("createVenue: group lookup", "err", err)
				writeError(w, http.StatusInternalServerError, "failed to verify group")
			}
			return
		}
		if !group.AutoBookingAllowed {
			writeError(w, http.StatusBadRequest, "auto_booking_disallowed_by_owner")
			return
		}
	}

	venue, err := h.venueService.CreateVenue(r.Context(),
		req.GroupID, req.Name, req.Courts, req.TimeSlots, req.Address,
		req.GracePeriodHours, req.GameDays, req.BookingOpensDays, req.PreferredGameTimes, req.AutoBookingCourts, req.AutoBookingEnabled, req.AutoBookingCourtsCount,
	)
	if err != nil {
		h.logger.Error("createVenue", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.ActorTelegramID != 0 {
		h.auditSvc.RecordVenueCreated(r.Context(), venue.ID, req.GroupID, req.ActorTelegramID, req.ActorDisplay, venue.Name)
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
		GroupID                int64  `json:"group_id"`
		Name                   string `json:"name"`
		Courts                 string `json:"courts"`
		TimeSlots              string `json:"time_slots"`
		Address                string `json:"address"`
		GracePeriodHours       int    `json:"grace_period_hours"`
		GameDays               string `json:"game_days"`
		BookingOpensDays       int    `json:"booking_opens_days"`
		PreferredGameTimes     string `json:"preferred_game_times"`
		AutoBookingCourts      string `json:"auto_booking_courts"`
		AutoBookingEnabled     bool   `json:"auto_booking_enabled"`
		AutoBookingCourtsCount int    `json:"auto_booking_courts_count"`
		ActorTelegramID        int64  `json:"actor_telegram_id"`
		ActorDisplay           string `json:"actor_display"`
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

	if req.AutoBookingEnabled {
		group, err := h.groupRepo.GetByID(r.Context(), req.GroupID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "group not found")
			} else {
				h.logger.Error("updateVenue: group lookup", "err", err)
				writeError(w, http.StatusInternalServerError, "failed to verify group")
			}
			return
		}
		if !group.AutoBookingAllowed {
			writeError(w, http.StatusBadRequest, "auto_booking_disallowed_by_owner")
			return
		}
	}

	venue, err := h.venueService.UpdateVenue(r.Context(),
		id, req.GroupID, req.Name, req.Courts, req.TimeSlots, req.Address,
		req.GracePeriodHours, req.GameDays, req.BookingOpensDays, req.PreferredGameTimes, req.AutoBookingCourts, req.AutoBookingEnabled, req.AutoBookingCourtsCount,
	)
	if err != nil {
		h.logger.Error("updateVenue", "err", err, "id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.ActorTelegramID != 0 {
		h.auditSvc.RecordVenueUpdated(r.Context(), venue.ID, req.GroupID, req.ActorTelegramID, req.ActorDisplay, venue.Name)
	}
	writeJSON(w, http.StatusOK, venue)
}

// bookingReadiness handles GET /api/v1/venues/{id}/booking-readiness?group_id=X
func (h *Handler) bookingReadiness(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return
	}
	groupID, err := parseID(r.URL.Query().Get("group_id"))
	if err != nil || groupID == 0 {
		writeError(w, http.StatusBadRequest, "group_id query parameter is required")
		return
	}
	if h.venueCredService == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ready":      false,
			"max_courts": 0,
			"reason":     "credentials_not_configured",
		})
		return
	}

	group, err := h.groupRepo.GetByID(r.Context(), groupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
		} else {
			h.logger.Error("bookingReadiness: group lookup", "err", err)
			writeError(w, http.StatusInternalServerError, "failed to verify group")
		}
		return
	}
	if !group.AutoBookingAllowed {
		writeJSON(w, http.StatusOK, map[string]any{
			"ready":      false,
			"max_courts": 0,
			"reason":     "auto_booking_disallowed_by_owner",
		})
		return
	}

	venue, err := h.venueService.GetVenueByIDAndGroupID(r.Context(), id, groupID)
	if err != nil {
		writeError(w, http.StatusNotFound, "venue not found")
		return
	}
	if !venue.AutoBookingEnabled {
		writeJSON(w, http.StatusOK, map[string]any{
			"ready":      false,
			"max_courts": 0,
			"reason":     "auto_booking_disabled",
		})
		return
	}

	ready, maxCourts, err := h.venueCredService.HasUsableCredentials(r.Context(), id, h.credentialErrorCooldown)
	if err != nil {
		h.logger.Error("bookingReadiness: HasUsableCredentials", "err", err, "venue_id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	reason := ""
	if !ready {
		reason = "no_usable_credentials"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ready":      ready,
		"max_courts": maxCourts,
		"reason":     reason,
	})
}

// deleteVenue handles DELETE /api/v1/venues/{id}?group_id=X[&actor_tg_id=Y&actor_display=Z]
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

	venue, err := h.venueService.GetVenueByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "venue not found")
		return
	}

	if err := h.venueService.DeleteVenue(r.Context(), id, groupID); err != nil {
		if errors.Is(err, service.ErrVenueHasActiveBookings) {
			writeError(w, http.StatusConflict, "venue has active court bookings and cannot be deleted")
			return
		}
		h.logger.Error("deleteVenue", "err", err, "id", id)
		writeError(w, http.StatusNotFound, "venue not found")
		return
	}
	if actorTgID != 0 {
		h.auditSvc.RecordVenueDeleted(r.Context(), id, groupID, actorTgID, actorDisplay, venue.Name)
	}
	w.WriteHeader(http.StatusNoContent)
}

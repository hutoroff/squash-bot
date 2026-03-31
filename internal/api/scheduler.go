package api

import (
	"log/slog"
	"net/http"
)

// triggerScheduler handles POST /api/v1/scheduler/trigger/{event}
// Runs the scheduled job asynchronously and returns 202 immediately.
func (h *Handler) triggerScheduler(w http.ResponseWriter, r *http.Request) {
	event := r.PathValue("event")

	var job func()
	switch event {
	case "cancellation_reminder":
		job = h.scheduler.RunCancellationReminders
	case "day_after_cleanup":
		job = h.scheduler.RunDayAfterCleanup
	case "booking_reminder":
		job = h.scheduler.RunBookingReminders
	case "auto_booking":
		job = h.scheduler.RunAutoBooking
	default:
		writeError(w, http.StatusBadRequest, "unknown event: "+event)
		return
	}

	go func() {
		slog.Info("manual trigger started", "event", event)
		job()
		slog.Info("manual trigger completed", "event", event)
	}()

	w.WriteHeader(http.StatusAccepted)
}

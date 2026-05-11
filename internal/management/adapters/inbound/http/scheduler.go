package http

import (
	"log/slog"
	"net/http"
)

func (h *Handler) triggerScheduler(w http.ResponseWriter, r *http.Request) {
	event := r.PathValue("event")

	if !h.scheduler.HasJob(event) {
		writeError(w, http.StatusBadRequest, "unknown event: "+event)
		return
	}

	go func() {
		slog.Info("manual trigger started", "event", event)
		h.scheduler.ForceRun(event)
		slog.Info("manual trigger completed", "event", event)
	}()

	w.WriteHeader(http.StatusAccepted)
}

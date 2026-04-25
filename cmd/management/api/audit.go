package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// listAuditEvents handles GET /api/v1/audit
//
// Visibility rules enforced server-side:
//   - Server owner (caller TG ID in serverOwnerIDs): sees all events; may filter by any field.
//   - Everyone else: sees only their own events with visibility="player".
//
// Query params: limit, before_id, event_type, from (RFC3339), to (RFC3339).
// Server-owner-only params: group_id, actor_tg_id.
// Required header: X-Caller-Tg-Id.
func (h *Handler) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	callerTgIDStr := r.Header.Get("X-Caller-Tg-Id")
	callerTgID, err := strconv.ParseInt(callerTgIDStr, 10, 64)
	if err != nil || callerTgID == 0 {
		writeError(w, http.StatusBadRequest, "X-Caller-Tg-Id header required")
		return
	}

	q := r.URL.Query()
	filter := models.AuditQueryFilter{Limit: 50}

	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Limit = n
		}
	}
	if v := q.Get("before_id"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.BeforeID = &n
		}
	}
	if v := q.Get("event_type"); v != "" {
		filter.EventType = v
	}
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.From = &t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.To = &t
		}
	}

	if h.isServerOwner(callerTgID) {
		if v := q.Get("group_id"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				filter.GroupID = &n
			}
		}
		if v := q.Get("actor_tg_id"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				filter.ActorTgID = &n
			}
		}
	} else {
		filter.ActorTgID = &callerTgID
		filter.Visibilities = []models.AuditVisibility{models.AuditVisibilityPlayer}
	}

	events, err := h.auditSvc.Query(r.Context(), filter)
	if err != nil {
		h.logger.Error("listAuditEvents", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to query audit events")
		return
	}
	if events == nil {
		events = []*models.AuditEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (h *Handler) isServerOwner(tgID int64) bool {
	return h.serverOwnerIDs[tgID]
}

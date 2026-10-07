package api

import (
	"log/slog"
	"net/http"

	"github.com/hutoroff/squash-bot/internal/session"
)

// riskActor requires a signed web session in addition to the management
// transport bearer (enforced by server middleware). A caller-supplied actor
// header/body is not proof of identity or authority for duplicate risk.
func (h *Handler) riskActor(w http.ResponseWriter, r *http.Request) (int64, bool) {
	actorID, err := session.VerifyUserID(h.riskSessionSecret, r.Header.Get("X-Web-Session"))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "valid web session required")
		return 0, false
	}
	if h.userRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "owner authorization unavailable")
		return 0, false
	}
	owner, err := h.userRepo.IsServerOwner(r.Context(), actorID)
	if err != nil {
		slog.Error("check duplicate-risk owner", "err", err)
		writeError(w, http.StatusServiceUnavailable, "owner authorization unavailable")
		return 0, false
	}
	if !owner {
		writeError(w, http.StatusForbidden, "server owner required")
		return 0, false
	}
	return actorID, true
}

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/internal/featureflags"
)

func (h *Handler) SetFeatureFlags(s *service.FeatureFlagService) { h.featureFlags = s }

func flagScope(r *http.Request) (*int64, error) {
	values, exists := r.URL.Query()["group_id"]
	if !exists {
		return nil, nil
	}
	if len(values) != 1 {
		return nil, service.ErrFeatureFlagScope
	}
	id, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || id == 0 {
		return nil, service.ErrFeatureFlagScope
	}
	return &id, nil
}
func (h *Handler) listFeatureFlags(w http.ResponseWriter, r *http.Request) {
	group, err := flagScope(r)
	if err != nil {
		h.flagError(w, err)
		return
	}
	actor, _ := strconv.ParseInt(r.Header.Get("X-Caller-User-Id"), 10, 64)
	states, err := h.featureFlags.List(r.Context(), actor, group)
	if err != nil {
		h.flagError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, states)
}
func (h *Handler) setFeatureFlag(w http.ResponseWriter, r *http.Request) {
	group, err := flagScope(r)
	if err != nil {
		h.flagError(w, err)
		return
	}
	var req struct {
		Enabled      json.RawMessage `json:"enabled"`
		ActorUserID  int64           `json:"actor_user_id"`
		ActorDisplay string          `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil || len(req.Enabled) == 0 {
		writeError(w, http.StatusBadRequest, "enabled must be true, false, or null (reset)")
		return
	}
	var enabled *bool
	if err := json.Unmarshal(req.Enabled, &enabled); err != nil {
		writeError(w, http.StatusBadRequest, "invalid enabled value")
		return
	}
	err = h.featureFlags.Set(r.Context(), req.ActorUserID, req.ActorDisplay, featureflags.Key(r.PathValue("key")), group, enabled)
	if err != nil {
		h.flagError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) flagError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrFeatureFlagForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, service.ErrFeatureFlagScope), errors.Is(err, featureflags.ErrUnknown):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		h.logger.Error("feature flags", "err", err)
		writeError(w, http.StatusInternalServerError, "feature flags unavailable")
	}
}

package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// getUserPreferences handles GET /api/v1/users/{telegramID}/preferences.
// Returns 404 when no preferences row exists yet for the user.
func (h *Handler) getUserPreferences(w http.ResponseWriter, r *http.Request) {
	telegramID, err := strconv.ParseInt(r.PathValue("telegramID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid telegram ID")
		return
	}

	prefs, err := h.userPrefsRepo.GetByTelegramID(r.Context(), telegramID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "preferences not found")
		return
	}
	if err != nil {
		h.logger.Error("getUserPreferences", "telegram_id", telegramID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

// setUserDMLanguage handles PATCH /api/v1/users/{telegramID}/dm-language.
// Body: {"language": "en"|"de"|"ru"}. Returns 204 on success.
func (h *Handler) setUserDMLanguage(w http.ResponseWriter, r *http.Request) {
	telegramID, err := strconv.ParseInt(r.PathValue("telegramID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid telegram ID")
		return
	}

	var req struct {
		Language string `json:"language"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch req.Language {
	case "en", "de", "ru":
		// valid
	default:
		writeError(w, http.StatusBadRequest, "unsupported language; use en, de, or ru")
		return
	}

	if err := h.userPrefsRepo.SetDMLanguage(r.Context(), telegramID, req.Language); err != nil {
		h.logger.Error("setUserDMLanguage", "telegram_id", telegramID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setUserResultsOptOut handles PATCH /api/v1/users/{telegramID}/results-opt-out.
// Body: {"opt_out": bool}. Returns 204 on success.
func (h *Handler) setUserResultsOptOut(w http.ResponseWriter, r *http.Request) {
	telegramID, err := strconv.ParseInt(r.PathValue("telegramID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid telegram ID")
		return
	}

	var req struct {
		OptOut *bool `json:"opt_out"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.OptOut == nil {
		writeError(w, http.StatusBadRequest, "opt_out field is required")
		return
	}

	if err := h.userPrefsRepo.SetResultsOptOut(r.Context(), telegramID, *req.OptOut); err != nil {
		h.logger.Error("setUserResultsOptOut", "telegram_id", telegramID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
